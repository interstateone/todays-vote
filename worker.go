package main

import (
	"bytes"
	"code.google.com/p/goauth2/oauth"
	"encoding/json"
	"github.com/baliw/moverss"
	"github.com/darkhelmet/env"
	"github.com/interstateone/bufferapi"
	"github.com/interstateone/todays-vote/votes"
	"github.com/interstateone/translate"
	"html/template"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"
)

var (
	templates   *template.Template
	templateDir string = "templates/"
	outputDir   string = "public/"
)

type JSONFeed struct {
	Title       string       `json:"title"`
	Link        string       `json:"link"`
	Description string       `json:"description"`
	Items       []votes.Vote `json:"items"`
}

func setup() {
	templates = template.Must(template.New("funcs").Funcs(template.FuncMap{
		"FormatDate": func(date string) string {
			parsedDate, _ := time.Parse("2006-01-02", date)
			return parsedDate.Format("Monday, January 2, 2006")
		},
		"FirstLetter": func(input string) string {
			return strings.Split(input, "")[0]
		},
		"Booleanize": Booleanize,
		"HTML": func(html string) template.HTML {
			return template.HTML(html)
		},
	}).ParseGlob(templateDir + "*.tmpl"))

	err := readEnvfile()
	if err != nil {
		log.Printf("v", err)
	}
}

func Booleanize(decision string) string {
	if decision == "Agreed to" {
		return "Yea"
	}
	return "Nay"
}

func readEnvfile() error {
	content, err := ioutil.ReadFile(".env")
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(string(content), "\n") {
		tokens := strings.SplitN(line, "=", 2)
		if len(tokens) == 2 && tokens[0][0] != '#' {
			k, v := strings.TrimSpace(tokens[0]), strings.TrimSpace(tokens[1])
			os.Setenv(k, v)
		}
	}
	return nil
}

func initBuffer() (buffer *bufferapi.Client) {
	authToken := env.String("BUFFER_AUTH_TOKEN")
	clientId := env.String("BUFFER_CLIENT_ID")
	clientSecret := env.String("BUFFER_CLIENT_SECRET")
	bufferConfig := &oauth.Config{
		ClientId:     clientId,
		ClientSecret: clientSecret,
		Scope:        "",
		AuthURL:      "",
		TokenURL:     "",
		TokenCache:   oauth.CacheFile(""),
	}
	transport := &oauth.Transport{Config: bufferConfig}
	buffer = bufferapi.ClientFactory(authToken, transport)
	return
}

func renderRSS(filename string, channel *moverss.Channel, data []votes.Vote) (err error) {
	for _, v := range data {
		var title string
		if v.RelatedBill == "" {
			title = "Vote"
		} else {
			title = v.RelatedBill
		}
		var link string
		if v.RelatedBill == "" {
			link = "http://www.todaysvote.ca"
		} else {
			link = "http://www.parl.gc.ca/LegisInfo/BillDetails.aspx?Mode=1&Language=E&bill=" + v.RelatedBill
		}
		parsedDate, _ := time.Parse("2006-01-02", v.Date)
		date := parsedDate.Format(time.RFC822)
		channel.AddItem(&moverss.Item{Title: title, Link: link, Description: v.Decision + ": " + v.DescriptionEnglish, PubDate: date})
	}
	rssBody := channel.PublishIndent()
	filename = outputDir + filename + ".xml"
	ioutil.WriteFile(filename, rssBody, 0666)
	return nil
}

func renderJSON(filename string, feed JSONFeed) (err error) {
	filename = outputDir + filename + ".json"
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer file.Close()
	jsonBody, err := json.MarshalIndent(feed, "", "  ")
	if err != nil {
		return err
	}
	jsonArray := [][]byte{[]byte("jsonVoteFeed("), jsonBody, []byte(")")}
	jsonBody = bytes.Join(jsonArray, []byte(""))
	ioutil.WriteFile(filename, jsonBody, 0666)
	return nil
}

func renderHTML(filename string, data interface{}) (err error) {
	file, err := os.Create(outputDir + filename + ".html")
	if err != nil {
		return err
	}
	defer file.Close()
	err = templates.ExecuteTemplate(file, filename+".tmpl", data)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	setup()

	latestVotes, err := votes.FetchLatestVotes()
	if err != nil {
		log.Printf("%+v", err)
	}

	wordsToTranslate := []string{}
	for _, vote := range latestVotes {
		firstWord := strings.Split(vote.DescriptionEnglish, " ")[0]
		wordsToTranslate = append(wordsToTranslate, firstWord)
	}

	translateConfig := translate.Config{
		GrantType:    "client_credentials",
		ScopeUrl:     "http://api.microsofttranslator.com",
		ClientId:     env.String("BING_CLIENT_ID"),
		ClientSecret: env.String("BING_CLIENT_SECRET"),
		AuthUrl:      "https://datamarket.accesscontrol.windows.net/v2/OAuth2-13/",
	}
	token, err := translate.GetToken(&translateConfig)
	if err != nil {
		log.Printf("%v", err)
	}
	translatedWords, err := token.TranslateArray(wordsToTranslate, "", "fr")
	if err != nil {
		log.Printf("%v", err)
	}
	for index, word := range translatedWords {
		switch word {
		case "Assentiment":
			translatedWords[index] = "Adoption"
		case "2ème":
			translatedWords[index] = "2e"
		case "Privé":
			translatedWords[index] = "Affaires"
		case "Temps":
			translatedWords[index] = "Attribution"
		}
	}
	if len(latestVotes) > 0 {
		log.Printf("%+v", wordsToTranslate)
		log.Printf("%+v", translatedWords)
	}

	for index, vote := range latestVotes {
		splitIndex := strings.LastIndex(vote.DescriptionEnglish, translatedWords[index])
		if splitIndex < 0 {
			splitIndex = len(vote.DescriptionEnglish) - 1
		}
		log.Printf("whole: %v", vote.DescriptionEnglish)
		english := strings.Join(strings.Split(vote.DescriptionEnglish, "")[0:splitIndex], "")
		log.Printf("en: %v", english)
		french := strings.Join(strings.Split(vote.DescriptionEnglish, "")[splitIndex:], "")
		log.Printf("fr: %v", french)
		latestVotes[index].DescriptionEnglish = english
		latestVotes[index].DescriptionFrench = french
	}

	// Insert oldest votes first
	latestVotes.Reverse()
	votes.InsertVotes(latestVotes)
	log.Printf("[worker] Inserted %d rows", len(latestVotes))

	// Push new votes to Buffer, oldest first
	buffer := initBuffer()
	profiles, err := buffer.Profiles()
	if err != nil {
		log.Printf("%v", err)
	}

	bufferCount := 0
	for _, vote := range latestVotes {
		u := bufferapi.NewUpdate{Text: vote.ShortDescription() + " " + vote.Link(),
			Media:      map[string]string{"link": vote.Link()},
			ProfileIds: []string{(*profiles)[0].Id},
			Shorten:    true,
			Now:        false}
		_, err := buffer.Update(&u)
		if err != nil {
			log.Printf("%v", err)
		} else {
			bufferCount++
		}
	}
	log.Printf("[worker] Pushed %d tweets to Buffer", bufferCount)

	latestTenVotes, err := votes.LatestTenVotes()
	if err != nil {
		log.Printf("%+v", err)
	}

	// Render JSON file
	j := JSONFeed{Title: "Today's Vote", Link: "http://www.todaysvote.ca", Description: "Stay up to date with what Canada's House of Commons is voting on each day.", Items: latestTenVotes}
	err = renderJSON("feed", j)
	if err != nil {
		log.Printf("%v", err)
	}
	log.Println("[worker] Rendered JSON feed")

	// Write feed to RSS file
	c := moverss.ChannelFactory("Today's Vote", "http://www.todaysvote.ca", "Stay up to date with what Canada's House of Commons is voting on each day.")
	err = renderRSS("feed", c, latestTenVotes)
	if err != nil {
		log.Printf("%v", err)
	}
	log.Println("[worker] Rendered RSS feed")

	// Render index.html
	err = renderHTML("index", latestTenVotes)
	if err != nil {
		log.Printf("%v", err)
	}
	log.Println("[worker] Rendered HTML page")
}
