package votes

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	_ "github.com/bmizerany/pq"
	"github.com/coopernurse/gorp"
	"github.com/darkhelmet/env"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
)

var (
	digestUrl string = "http://www.parl.gc.ca/HouseChamberBusiness/Chambervotelist.aspx?Language=E&xml=True"
	goEnv     string
	dbmap     *gorp.DbMap
)

type Votes struct {
	XMLName xml.Name `xml:"Votes" db:"-" json:"-"`
	Votes   []Vote   `xml:"Vote"`
}

type Vote struct {
	XMLName            xml.Name `xml:"Vote" db:"-" json:"-"`
	Id                 int64    `xml:"-" db:"id" json:"-"`
	Number             int64    `xml:"number,attr" db:"number"`
	Parliament         int64    `xml:"parliament,attr" db:"parliament"`
	Session            int64    `xml:"session,attr" db:"session"`
	Sitting            int64    `xml:"sitting,attr" db:"sitting"`
	Date               string   `xml:"date,attr" db:"date"`
	DescriptionEnglish string   `xml:"Description" db:"description_english"`
	DescriptionFrench  string   `xml:"-" db:"description_french"`
	Decision           string   `xml:"Decision" db:"decision"`
	RelatedBill        string   `xml:"RelatedBill" db:"related_bill"`
	TotalYeas          int64    `xml:"TotalYeas" db:"total_yeas"`
	TotalNays          int64    `xml:"TotalNays" db:"total_nays"`
	TotalPaired        int64    `xml:"TotalPaired" db:"total_paired"`
}

type VoteSlice []Vote

func (v VoteSlice) Len() int           { return len(v) }
func (v VoteSlice) Less(i, j int) bool { return v[i].Number < v[j].Number }
func (v VoteSlice) Swap(i, j int)      { v[i], v[j] = v[j], v[i] }
func (v VoteSlice) Reverse()           { sort.Reverse(v) }

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

func latestVoteNumbers() (voteNumber, parliamentNumber int64, err error) {
	query := "SELECT * FROM vote ORDER BY parliament DESC, number DESC LIMIT 1"
	rows, err := dbmap.Select(Vote{}, query)
	if err != nil {
		return 0, 0, err
	}
	voteNumber = 0
	parliamentNumber = 0
	if len(rows) > 0 {
		voteNumber = rows[0].(*Vote).Number
		parliamentNumber = rows[0].(*Vote).Parliament
	}
	return voteNumber, parliamentNumber, nil
}

func download(uri string) (votes *Votes, err error) {
	client := http.Client{}
	xmlBody, err := getRequest(&client, uri)
	if err != nil {
		return nil, err
	}

	// Switch the RelatedBill number attribute to it's value
	pattern, err := regexp.Compile("<RelatedBill number=\"([a-zA-Z0-9-]+)\" />")
	if err != nil {
		return nil, err
	}
	xmlBody = pattern.ReplaceAll(xmlBody, []byte("<RelatedBill>$1</RelatedBill>"))

	xml.Unmarshal(xmlBody, &votes)
	return votes, nil
}

func FetchLatestVotes() (latestVotes VoteSlice, err error) {
	allVotes, err := download(digestUrl)
	if err != nil {
		return nil, err
	}

	maxVoteNumber, maxParliament, err := latestVoteNumbers()
	if err != nil {
		return nil, err
	}

	for _, v := range allVotes.Votes {
		if (v.Parliament == maxParliament && v.Number > maxVoteNumber) || v.Parliament > maxParliament {
			latestVotes = append(latestVotes, v)
		}
	}
	return latestVotes, nil
}

func LatestTenVotes() (latestVotes VoteSlice, err error) {
	query := "SELECT * FROM vote ORDER BY id DESC LIMIT 10"
	rows, err := dbmap.Select(Vote{}, query)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		latestVotes = append(latestVotes, *(row.(*Vote)))
	}
	return latestVotes, nil
}

func InsertVotes(votes VoteSlice) (err error) {
	transaction, err := dbmap.Begin()
	if err != nil {
		return err
	}
	for _, v := range votes {
		err := transaction.Insert(&v)
		if err != nil {
			return err
		}
	}
	transaction.Commit()
	return nil
}

func (vote *Vote) ShortDescription() (shortDescription string) {
	shortDescription = vote.DescriptionEnglish
	if len(vote.DescriptionEnglish) > 110 {
		end := math.Min(110, (float64)(len(vote.DescriptionEnglish)-1))
		shortDescription = vote.DescriptionEnglish[:(int64)(end)]
		shortDescription += "..."
	}
	return
}

func (vote *Vote) Link() (url string) {
	url = "http://www.parl.gc.ca/LegisInfo/BillDetails.aspx?Mode=1&Language=E&bill=" + vote.RelatedBill
	if vote.RelatedBill == "" {
		url = "http://www.todaysvote.ca"
	}
	return
}

func init() {
	goEnv = env.String("GO_ENV")

	host := env.String(goEnv + "_POSTGRES_HOST")
	database := env.String(goEnv + "_POSTGRES_DB")
	user := env.String(goEnv + "_POSTGRES_USER")
	port := env.String(goEnv + "_POSTGRES_PORT")
	password := env.String(goEnv + "_POSTGRES_PASSWORD")
	ssl := env.String(goEnv + "_POSTGRES_SSL")
	connectionInfo := fmt.Sprintf("dbname=%s user=%s password=%s host=%s port=%s sslmode=%s", database, user, password, host, port, ssl)
	db, err := sql.Open("postgres", connectionInfo)
	if err != nil {
		log.Fatalf("%+v", err)
	}
	dbmap = &gorp.DbMap{Db: db, Dialect: gorp.PostgresDialect{}}
	table := dbmap.AddTable(Vote{}).SetKeys(true, "Id")
	table.ColMap("DescriptionEnglish").SetMaxSize(2048)
	table.ColMap("DescriptionFrench").SetMaxSize(2048)
	dbmap.CreateTables()
}
