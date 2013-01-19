package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/baliw/moverss"
	_ "github.com/bmizerany/pq"
	"github.com/coopernurse/gorp"
	"github.com/darkhelmet/env"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"
)

var (
	templates *template.Template
	config    Config
	goEnv     string
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
	XMLName     xml.Name `xml:"Vote" db:"-" json:"-"`
	Number      int64    `xml:"number,attr"`
	Parliament  int64    `xml:"parliament,attr"`
	Session     int64    `xml:"session,attr"`
	Sitting     int64    `xml:"sitting,attr"`
	Date        string   `xml:"date,attr"`
	Description string   `xml:"Description"`
	Decision    string   `xml:"Decision"`
	RelatedBill string   `xml:"RelatedBill"`
	TotalYeas   int64    `xml:"TotalYeas"`
	TotalNays   int64    `xml:"TotalNays"`
	TotalPaired int64    `xml:"TotalPaired"`
}

func setup() {
	templates = template.Must(template.New("funcs").Funcs(template.FuncMap{
		"FormatDate": func(date string) string {
			parsedDate, _ := time.Parse("2006-01-02", date)
			return parsedDate.Format("Monday, January 2, 2006")
		},
	}).ParseGlob("templates/*.tmpl"))

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
	filename := "config.json"
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

func main() {
	setup()

	client := http.Client{}
	xmlBody, err := getRequest(&client, config.DigestUrl)
	fmt.Sprintf("%s", xmlBody)
	if err != nil {
		log.Printf("%v", err)
	}

	// Switch the RelatedBill number attribute to it's value
	pattern, err := regexp.Compile("<RelatedBill number=\"([a-zA-Z0-9-]+)\" />")
	if err != nil {
		log.Printf("%v", err)
	}
	xmlBody = pattern.ReplaceAll(xmlBody, []byte("<RelatedBill>$1</RelatedBill>"))

	votes := &Votes{}
	xml.Unmarshal(xmlBody, &votes)

	host := env.String(goEnv + "_POSTGRES_HOST")
	database := env.String(goEnv + "_POSTGRES_DB")
	user := env.String(goEnv + "_POSTGRES_USER")
	port := env.String(goEnv + "_POSTGRES_PORT")
	password := env.String(goEnv + "_POSTGRES_PASSWORD")
	ssl := env.String(goEnv + "_POSTGRES_SSL")
	connectionInfo := fmt.Sprintf("dbname=%s user=%s password=%s host=%s port=%s sslmode=%s", database, user, password, host, port, ssl)
	db, err := sql.Open("postgres", connectionInfo)
	dbmap := &gorp.DbMap{Db: db, Dialect: gorp.PostgresDialect{}}

	table := dbmap.AddTable(Vote{}).SetKeys(false, "Number")
	table.ColMap("Description").SetMaxSize(2048)
	dbmap.CreateTables()

	query := "SELECT number FROM vote ORDER BY number DESC LIMIT 1"
	vote := Vote{}
	rows, err := dbmap.Select(vote, query)
	if err != nil {
		log.Printf("%v", err)
	}
	var currentMaxVoteNumber int64 = 0
	if len(rows) > 0 {
		currentMaxVoteNumber = reflect.ValueOf(rows[0]).Elem().FieldByName("Number").Int()
	}

	count := 0
	transaction, err := dbmap.Begin()
	if err != nil {
		log.Printf("%v", err)
	}
	for _, v := range votes.Votes {
		if v.Number > currentMaxVoteNumber {
			err := transaction.Insert(&v)
			if err != nil {
				log.Printf("%s", err)
			}
		}
		count++
	}
	transaction.Commit()
	log.Printf("[digester]: inserted %d rows", count)

	// Render JSON file
	j := JSONFeed{Title: "Today's Vote", Link: "http://www.todaysvote.ca", Description: "Stay up to date with what Canada's House of Commons is voting on each day."}
	j.Items = votes.Votes[0:10]

	filename := "public/feed.json"
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

	// Write feed to RSS file
	c := moverss.ChannelFactory("Today's Vote", "http://www.todaysvote.ca", "Stay up to date with what Canada's House of Commons is voting on each day.")
	for _, v := range votes.Votes[0:10] {
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
		c.AddItem(&moverss.Item{Title: title, Link: link, Description: v.Description})
	}
	rssBody := c.PublishIndent()
	filename = "public/feed.xml"
	ioutil.WriteFile(filename, rssBody, 0666)

	// Render index.html
	filename = "index"
	file, err = os.Create("public/" + filename + ".html")
	if err != nil {
		log.Printf("%v", err)
	}
	defer file.Close()
	err = templates.ExecuteTemplate(file, filename+".tmpl", votes.Votes[0:10])
	if err != nil {
		log.Printf("%v", err)
	}
}
