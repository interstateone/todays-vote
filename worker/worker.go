package main

import (
	"bytes"
	"code.google.com/p/goauth2/oauth"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/baliw/moverss"
	_ "github.com/bmizerany/pq"
	"github.com/coopernurse/gorp"
	"github.com/darkhelmet/env"
	"github.com/interstateone/bufferapi"
	"github.com/interstateone/translate"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	config      Config
	goEnv       string
	templates   *template.Template
	templateDir string = "templates/"
	outputDir   string = "public/"
)

type Config struct {
	DigestUrl string
}

type Votes struct {
	XMLName xml.Name `xml:"Votes" db:"-" json:"-"`
	Votes   []Vote   `xml:"Vote"`
}

type JSONFeed struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	Description string `json:"description"`
	Items       []Vote `json:"items"`
}

type Vote struct {
	XMLName            xml.Name `xml:"Vote" db:"-" json:"-"`
	Id                 int64    `xml:"-" db:"id" json:"-"`
	Number             int64    `xml:"number,attr"`
	Parliament         int64    `xml:"parliament,attr"`
	Session            int64    `xml:"session,attr"`
	Sitting            int64    `xml:"sitting,attr"`
	Date               string   `xml:"date,attr"`
	DescriptionEnglish string   `xml:"Description"`
	DescriptionFrench  string   `xml:"-"`
	Decision           string   `xml:"Decision"`
	RelatedBill        string   `xml:"RelatedBill"`
	TotalYeas          int64    `xml:"TotalYeas"`
	TotalNays          int64    `xml:"TotalNays"`
	TotalPaired        int64    `xml:"TotalPaired"`
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
		"Booleanize": func(decision string) string {
			if decision == "Agreed to" {
				return "Yes"
			}
			return "No"
		},
	}).ParseGlob(templateDir + "*.tmpl"))

	err := readEnvfile()
	if err != nil {
		log.Printf("v", err)
	}

	goEnv = env.String("GO_ENV")

	config = Config{}
	err = loadConfig(&config)
	if err != nil {
		log.Printf("v", err)
	}
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

func loadConfig(config *Config) error {
	filename := "worker/config.json"
	body, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	err = json.Unmarshal(body, &config)
	if err != nil {
		return err
	}
	return nil
}

func getRequest(client *http.Client, url string) (body []byte, err error) {
	response, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	bytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

func downloadVotes(uri string) (votes *Votes) {
	client := http.Client{}
	xmlBody, err := getRequest(&client, uri)
	if err != nil {
		log.Printf("%v", err)
	}

	// Switch the RelatedBill number attribute to it's value
	pattern, err := regexp.Compile("<RelatedBill number=\"([a-zA-Z0-9-]+)\" />")
	if err != nil {
		log.Printf("%v", err)
	}
	xmlBody = pattern.ReplaceAll(xmlBody, []byte("<RelatedBill>$1</RelatedBill>"))

	xml.Unmarshal(xmlBody, &votes)
	return
}

func initDatabase() (dbmap *gorp.DbMap, err error) {
	host := env.String(goEnv + "_POSTGRES_HOST")
	database := env.String(goEnv + "_POSTGRES_DB")
	user := env.String(goEnv + "_POSTGRES_USER")
	port := env.String(goEnv + "_POSTGRES_PORT")
	password := env.String(goEnv + "_POSTGRES_PASSWORD")
	ssl := env.String(goEnv + "_POSTGRES_SSL")
	connectionInfo := fmt.Sprintf("dbname=%s user=%s password=%s host=%s port=%s sslmode=%s", database, user, password, host, port, ssl)
	db, err := sql.Open("postgres", connectionInfo)
	if err != nil {
		return nil, err
	}
	dbmap = &gorp.DbMap{Db: db, Dialect: gorp.PostgresDialect{}}
	return
}

func main() {
	setup()
	dbmap, err := initDatabase()
	if err != nil {
		log.Fatalf("%v", err)
	}
	table := dbmap.AddTable(Vote{}).SetKeys(true, "Id")
	table.ColMap("DescriptionEnglish").SetMaxSize(2048)
	table.ColMap("DescriptionFrench").SetMaxSize(2048)
	dbmap.CreateTables()

	query := "SELECT parliament, number FROM vote ORDER BY parliament DESC, number DESC LIMIT 1"
	rows, err := dbmap.Select(Vote{}, query)
	if err != nil {
		log.Printf("%v", err)
	}
	var maxVoteNumber int64 = 0
	var maxParliament int64 = 0
	if len(rows) > 0 {
		maxVoteNumber = rows[0].(*Vote).Number
		maxParliament = rows[0].(*Vote).Parliament
	}

	votes := downloadVotes(config.DigestUrl)
	newVotes := []Vote{}
	for _, v := range votes.Votes {
		if (v.Parliament == maxParliament && v.Number > maxVoteNumber) || v.Parliament > maxParliament {
			newVotes = append(newVotes, v)
		}
	}
	if len(newVotes) > 10 {
		newVotes = newVotes[0:10]
	}

	wordsToTranslate := []string{}
	for _, vote := range newVotes {
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
		}
	}
	if len(newVotes) > 0 {
		log.Printf("%+v", wordsToTranslate)
		log.Printf("%+v", translatedWords)
	}

	for index, vote := range newVotes {
		splitIndex := strings.Index(vote.DescriptionEnglish, translatedWords[index])
		if splitIndex < 0 {
			splitIndex = len(vote.DescriptionEnglish)
		}
		log.Printf("whole: %v", vote.DescriptionEnglish)
		english := strings.Join(strings.Split(vote.DescriptionEnglish, "")[0:splitIndex], "")
		log.Printf("en: %v", english)
		french := strings.Join(strings.Split(vote.DescriptionEnglish, "")[splitIndex:], "")
		log.Printf("fr: %v", french)
		newVotes[index].DescriptionEnglish = english
		newVotes[index].DescriptionFrench = french
	}

	// Insert and push to Buffer oldest votes first
	for i, j := 0, len(newVotes)-1; i < j; i, j = i+1, j-1 {
		newVotes[i], newVotes[j] = newVotes[j], newVotes[i]
	}

	transaction, err := dbmap.Begin()
	if err != nil {
		log.Printf("%v", err)
	}
	for _, v := range newVotes {
		err := transaction.Insert(&v)
		if err != nil {
			log.Printf("%s", err)
		}
	}
	transaction.Commit()
	log.Printf("[worker] Inserted %d rows", len(newVotes))

	// Push new votes to Buffer, oldest first
	authToken := env.String("BUFFER_AUTH_TOKEN")
	clientId := env.String("BUFFER_CLIENT_ID")
	clientSecret := env.String("BUFFER_CLIENT_SECRET")
	config := &oauth.Config{
		ClientId:     clientId,
		ClientSecret: clientSecret,
		Scope:        "",
		AuthURL:      "",
		TokenURL:     "",
		TokenCache:   oauth.CacheFile(""),
	}
	transport := &oauth.Transport{Config: config}
	buffer := bufferapi.ClientFactory(authToken, transport)
	profiles, err := buffer.Profiles()
	if err != nil {
		log.Printf("%v", err)
	}

	bufferCount := 0
	for _, vote := range newVotes {
		var link string
		if vote.RelatedBill == "" {
			link = "http://www.todaysvote.ca"
		} else {
			link = "http://www.parl.gc.ca/LegisInfo/BillDetails.aspx?Mode=1&Language=E&bill=" + vote.RelatedBill
		}
		u := bufferapi.NewUpdate{Text: vote.DescriptionEnglish, Media: map[string]string{"Link": link}, ProfileIds: []string{(*profiles)[0].Id}, Shorten: true, Now: false}
		_, err := buffer.Update(&u)
		if err != nil {
			log.Printf("%v", err)
		} else {
			bufferCount++
		}
	}
	log.Printf("[worker] Pushed %d tweets to Buffer", bufferCount)

	query = "SELECT * FROM vote ORDER BY id DESC LIMIT 10"
	rows, err = dbmap.Select(Vote{}, query)
	if err != nil {
		log.Printf("%v", err)
	}
	latestTenVotes := []Vote{}
	for _, row := range rows {
		latestTenVotes = append(latestTenVotes, *(row.(*Vote)))
	}

	// Render JSON file
	j := JSONFeed{Title: "Today's Vote", Link: "http://www.todaysvote.ca", Description: "Stay up to date with what Canada's House of Commons is voting on each day.", Items: latestTenVotes}
	filename := outputDir + "feed.json"
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		log.Printf("%v", err)
	}
	defer file.Close()
	jsonBody, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		log.Printf("%v", err)
	}
	jsonArray := [][]byte{[]byte("jsonVoteFeed("), jsonBody, []byte(")")}
	jsonBody = bytes.Join(jsonArray, []byte(""))
	ioutil.WriteFile(filename, jsonBody, 0666)
	log.Println("[worker] Rendered JSON feed")

	// Write feed to RSS file
	c := moverss.ChannelFactory("Today's Vote", "http://www.todaysvote.ca", "Stay up to date with what Canada's House of Commons is voting on each day.")
	for _, v := range latestTenVotes {
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
		c.AddItem(&moverss.Item{Title: title, Link: link, Description: v.Decision + ": " + v.DescriptionEnglish, PubDate: date})
	}
	rssBody := c.PublishIndent()
	filename = outputDir + "feed.xml"
	ioutil.WriteFile(filename, rssBody, 0666)
	log.Println("[worker] Rendered RSS feed")

	// Render index.html
	filename = "index"
	file, err = os.Create(outputDir + filename + ".html")
	if err != nil {
		log.Printf("%v", err)
	}
	defer file.Close()
	err = templates.ExecuteTemplate(file, filename+".tmpl", latestTenVotes)
	if err != nil {
		log.Printf("%v", err)
	}
	log.Println("[worker] Rendered HTML page")
}
