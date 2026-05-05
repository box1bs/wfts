package model

import (
	"net/url"
	"time"
	"bytes"
	"encoding/gob"
)

// General structure for describe any web page
type Document struct {
	Id 				[32]byte	`json:"id"`
	URL				string		`json:"url"`
	TokenCount 		int			`json:"words_count"`
}

// Scraping part
const (
	BodyType = 'b'
	HeaderType = 'h'
)

type CrawlFeatures struct {
	DomDepth int
	TagCount int
	UrlLen 	 int
	UrlCount int
	PathLen	 int
	HostLen	 int
}

type CrawlState struct {
	LastStart 	string
	Uptime 		string
	Allocated 	uint64
	MemFromOS 	uint64
	HeapIdle 	uint64
	HeapInUse	uint64
	DocsInIndex int
	IsRunning 	bool
}

type LinkToken struct {
	Link 		*url.URL
	Priority 	float64
	Depth 		int
	// Ancore		string
	SameDomain 	bool
}

type linkTokenGob struct {
	Link 		string
	Priority 	float64
	Depth 		int
	SameDomain 	bool
}

func (lt *LinkToken) toGob() *linkTokenGob {
	return &linkTokenGob{
		Link: lt.Link.String(),
		Priority: lt.Priority,
		Depth: lt.Depth,
		SameDomain: lt.SameDomain,
	}
}

func (lt *LinkToken) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(*lt.toGob()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DeserializeToken(data []byte) (*LinkToken, error) {
	var tokenGob linkTokenGob
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&tokenGob); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(tokenGob.Link)
	if err != nil {
		return nil, err
	}
	lt := &LinkToken{}
	lt.Link = parsed
	lt.Depth = tokenGob.Depth
	lt.Priority = tokenGob.Priority
	lt.SameDomain = tokenGob.SameDomain
	return lt, nil
}

type CrawlNode struct {
	CrawlToken  Serializable
	Priority 	float64
}

type Serializable interface {
	Serialize() ([]byte, error)
}

type CompletionState int
const (
	Done CompletionState = iota
	Canceled
	Error
)

type SearchResult struct {
	Docs 	[]*Document
	Rels 	[]*DocRanking
	Metrics *SearchMetrics
}

// Searching part
type SearchMetrics struct {
	HandleQuery 	time.Duration
	FetchAndProcess time.Duration
	Sort       		time.Duration
	Total      		time.Duration
	TotalResults 	int
}

type DocRanking struct {
	Tf_Idf 				float64
	BM25 				float64
	LogLenWordInURL 	float64
	TermProximity 		int
	HasWordInHeader 	bool
	//any ranking scores
}