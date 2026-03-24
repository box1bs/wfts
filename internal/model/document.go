package model

import "time"

type Document struct {
	Id 				[32]byte	`json:"id"`
	URL				string		`json:"url"`
	TokenCount 		int			`json:"words_count"`
}

const (
	BodyType = 'b'
	HeaderType = 'h'
)

type Passage struct {
	Text string
	Type byte
}

type CrawlFeatures struct {
	DomDepth int
	TagCount int
	UrlLen 	 int
	UrlCount int
	PathLen	 int
	HostLen	 int
}

type CompletionState int
const (
	Done CompletionState = iota
	Canceled
	Error
)

type WordCountAndPositions struct {
	Count 		int
	Positions 	[]Position
}

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

type Position struct {
	I 		int
	Type 	byte
}

func NewTypeTextObj[T Passage | Position](t byte, text string, i int) T {
	switch t {
	case BodyType, HeaderType:

	default:
		panic("unnamed passage type")
	}

	switch any(*new(T)).(type) {
	case Passage:
		out := Passage{Text: text, Type: t}
		return any(out).(T)
	case Position:
		out := Position{I: i, Type: t}
		return any(out).(T)
	default:
		panic("unnamed passage type")
	}
}

type CrawlNode struct {
	Activation 	func() CompletionState
	CrawlToken  any
	Priority 	float64
}