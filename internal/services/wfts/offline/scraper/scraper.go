package scraper

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"io"
	"log/slog"
	"math"

	"wfts/internal/model"
	"wfts/internal/services/wfts/offline/scraper/lruCache"
	"wfts/internal/utils/parser"
	"wfts/internal/utils/workerPool"

	"context"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type indexer interface {
    HandleDocumentWords(context.Context, *model.Document, *float64, []model.Passage) error
	SaveUrlsToBank([32]byte, []byte) error
	GetUrlsByHash([32]byte) ([]byte, error)
}

type WebScraper struct {
	client         	*http.Client
	visited        	*sync.Map
	cfg 		  	*configData
	rlMu         	*sync.RWMutex
	lru 			*lrucache.LRUCache
	pool           	*workerPool.WorkerPool
	idx 			indexer
	globalCtx		context.Context
	rlMap			map[string]*rateLimiter
	rulesMap		map[string]*parser.RobotsTxt
}

type configData struct {
	StartURLs     	[]string
	LogOutput 		io.Writer
	WorkersNum 		int
	Depth       	int
	OnlySameDomain  bool
}

func NewScrapeConfig(baseUrls []string, logWriter io.Writer, workerNum, depth int, onlySameDomain bool) *configData {
	return &configData{
		StartURLs: baseUrls,
		LogOutput: logWriter,
		WorkersNum: workerNum,
		Depth: depth,
		OnlySameDomain: onlySameDomain,
	}
}

const (
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0"
 	crawlTime = 300 * time.Second
 	deadlineTime = 15 * time.Second
	numOfTries = 3 // если кто то решил поменять это на 0, чтож, удачи
)

func NewScraper(mp *sync.Map, cfg *configData, idx indexer, c context.Context) *WebScraper {
	return &WebScraper{
		client: &http.Client{
			Timeout: deadlineTime,
			Transport: &http.Transport{
				IdleConnTimeout:   15 * time.Second,
				DisableKeepAlives: false,
				ForceAttemptHTTP2: true,
			},
		},
		visited:        mp,
		cfg: 			cfg,
		rlMu:           new(sync.RWMutex),
		lru: 			lrucache.NewLRUCache(cfg.WorkersNum * 10),
		pool:           workerPool.NewWorkerPool(cfg.WorkersNum, cfg.WorkersNum * 100, c),
		idx: 			idx,
		globalCtx:		c,
		rlMap: 			make(map[string]*rateLimiter),
		rulesMap: 		make(map[string]*parser.RobotsTxt),
	}
}

func (ws *WebScraper) Run() {
	defer ws.putDownLimiters()
	for _, uri := range ws.cfg.StartURLs {
		parsed, err := url.Parse(uri)
		log := model.NewLogger(slog.New(slog.NewJSONHandler(ws.cfg.LogOutput, &slog.HandlerOptions{
			ReplaceAttr: model.Replacer,
		})).With("url", uri))
		if err != nil {
			log.Errorf("parsing url failed: %v", err)
			continue
		}
		ws.pool.Submit(model.CrawlNode{Activation: func() {
			ws.rlMu.Lock()
			rl := NewRateLimiter(DefaultDelay)
			ws.rlMap[parsed.Host] = rl
			ws.rlMu.Unlock()
			ctx, cancel := context.WithTimeout(context.WithValue(ws.globalCtx, 0, log), crawlTime)
			defer cancel()
			ws.ScrapeWithContext(ctx, parsed, 0)
		}})
	}
	ws.pool.Wait()
	ws.pool.Stop()
}

func (ws *WebScraper) ScrapeWithContext(ctx context.Context, currentURL *url.URL, depth int) {
    if ws.checkContext(ctx) {return}

    if depth >= ws.cfg.Depth {
        return
    }
	
    normalized, err := normalizeUrl(currentURL)
    if err != nil {
		return
    }
	
	links, rls, err := ws.fetchPageRulesAndOffers(ctx, currentURL)
	if err.Error() == BaseXMLPageError || ws.checkContext(ctx) {
		return
	}
	host := truncatePort(currentURL)
	ws.rlMu.Lock()
	if rls != nil && ws.rulesMap[host] == nil {
		ws.rulesMap[host] = rls
	}
	ws.rlMu.Unlock()
	hashed := sha256.Sum256([]byte(normalized))
	load := false

	log := ctx.Value(0).(*model.Logger)
	if log == nil {
		return
	}

	priority := 1.0
    
	if len(links) == 0 && (err == nil || err.Error() != BaseXMLPageError) {
		if prevDepth, loaded := ws.visited.LoadOrStore(normalized, depth); loaded && prevDepth.(int) <= depth {
			return
		} else if loaded {
			load = true
			if v := ws.lru.Get(hashed); v != nil {
				links = v.([]*linkToken)
			} else {
				encoded, err := ws.idx.GetUrlsByHash(hashed)
				if err != nil {
					if err.Error() != "Key not found" {
						log.Errorf("error getting urls, from db: %v", err)
					}
					return
				}
				if err := gob.NewDecoder(bytes.NewBuffer(encoded)).Decode(&links); err != nil {
					log.Errorf("error unmarshalling urls from db: %v", err)
					return
				}
				if len(links) != 0 {
					ws.lru.Put(hashed, links)
				}
			}
		} else {
			if links, err = ws.fetchHTMLcontent(ctx, &priority, currentURL, normalized, depth); err != nil {
				return
			}
		}

		l := len(links)
		priority += math.Log(float64(l) + 1)
		
		if l == 0 {
			log.Debugf("empty links")
			return
		}

		if !load {
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(links); err != nil {
				log.Errorf("error marshalling urls: %v", err)
				return
			}

			if err := ws.idx.SaveUrlsToBank(hashed, buf.Bytes()); err != nil {
				log.Errorf("error saving urls: %v", err)
				return
			}
		}
	}
	
	for _, link := range links {
		if ws.cfg.OnlySameDomain && !link.SameDomain {
			continue
		}

		if ws.checkContext(ctx) { return }

		pr := priority
		if link.SameDomain {
			pr *= 2
		}

        ws.pool.Submit(model.CrawlNode{Activation: func() {
			ws.rlMu.Lock()
			if ws.rlMap[link.Link.Host] == nil {
				ws.rlMap[link.Link.Host] = NewRateLimiter(DefaultDelay)
			}
			ws.rlMu.Unlock()
			log := model.NewLogger(slog.New(slog.NewJSONHandler(ws.cfg.LogOutput, &slog.HandlerOptions{
				ReplaceAttr: model.Replacer,
			})).With("url", link.Link.String()))
			c, cancel := context.WithTimeout(context.WithValue(ws.globalCtx, 0, log), crawlTime)
			defer cancel()
			ws.ScrapeWithContext(c, link.Link, depth + 1)
		},
			Priority: pr / float64(depth + 1),
		})
    }
}

func (ws *WebScraper) putDownLimiters() {
	ws.rlMu.Lock()
	defer ws.rlMu.Unlock()
	for _, limiter := range ws.rlMap {
		limiter.Shutdown()
	}
}

func (ws *WebScraper) checkContext(ctx context.Context) bool {
	select {
		case <-ctx.Done():
			return true
		default:
	}
	return false
}