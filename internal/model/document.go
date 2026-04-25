package model

import (
	"time"
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
	DocsInIndex int
	IsRunning 	bool
}

type CrawlNode struct {
	Activation 	func() CompletionState
	CrawlToken  any
	Priority 	float64
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