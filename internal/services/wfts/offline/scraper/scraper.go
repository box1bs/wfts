package scraper

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"io"
	"log/slog"
	"math"

	"wfts/internal/model"
	lrucache "wfts/internal/services/wfts/offline/scraper/lruCache"
	"wfts/internal/utils/scheduler"

	"context"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type indexer interface {
	HandleDocumentWords(context.Context, *model.Document, *model.CrawlFeatures, *float64, []model.Passage) error
	IndexUrlsByHash([32]byte, []byte) error
	GetPageUrlsByHash([32]byte) ([]byte, error)
	SaveVisitedUrls(*sync.Map) error
	LoadVisitedUrls(*sync.Map) error
	SaveHashArrays() error
}

type WebScraper struct {
	indexer
	client    	*http.Client
	visited   	*sync.Map
	cfg       	*configData
	lru       	*lrucache.LRUCache
	rlCache   	*lrucache.LRUCache
	rulesCache 	*lrucache.LRUCache
	mu 			*sync.Mutex
	pool      	*scheduler.WorkerPool
	globalCtx 	context.Context
}

type configData struct {
	StartURLs      []string
	LocalCachePath string
	LogOutput      io.Writer
	WorkersNum     int
	OnlySameDomain bool
}

const canceled = "context canceled"

func NewScrapeConfig(baseUrls []string, cachePath string, logWriter io.Writer, workerNum int, onlySameDomain bool) *configData {
	return &configData{
		StartURLs:      baseUrls,
		LocalCachePath: cachePath,
		LogOutput:      logWriter,
		WorkersNum:     workerNum,
		OnlySameDomain: onlySameDomain,
	}
}

const (
	userAgent    = "WFTSBot/1.0"
	crawlTime    = 300 * time.Second
	deadlineTime = 15 * time.Second
	numOfTries   = 3 // если кто-то решил поменять это на 0, чтож, удачи
)

func NewScraper(cfg *configData, idx indexer, c context.Context) (*WebScraper, error) {
	ws := &WebScraper{
		indexer: idx,
		client: &http.Client{
			Timeout: 2 * deadlineTime,
			Transport: &http.Transport{
				IdleConnTimeout:   deadlineTime,
				DisableKeepAlives: false,
				ForceAttemptHTTP2: true,
			},
		},
		visited:   new(sync.Map),
		cfg:       cfg,
		lru:       lrucache.NewLRUCache(cfg.WorkersNum * 25),
		rlCache:   lrucache.NewLRUCache(cfg.WorkersNum * 25),
		rulesCache:lrucache.NewLRUCache(cfg.WorkersNum * 10),
		mu: 	   &sync.Mutex{},
		globalCtx: c,
	}
	stack, err := InitStack(cfg.LocalCachePath, 128 << 20)
	if err != nil {
		return nil, err
	}
	ws.pool = scheduler.NewWorkerPool(stack, ws.Packed, cfg.WorkersNum, cfg.WorkersNum*50)
	return ws, nil
}

func (ws *WebScraper) PrepareChan(rawUrls chan string) chan *model.LinkToken {
	out := make(chan *model.LinkToken, ws.cfg.WorkersNum*10)
	go func() {
		for {
			select {
			case <-ws.globalCtx.Done():
				return

			case uri, ok := <-rawUrls:
				if !ok {
					break
				}
				parsed, err := url.Parse(uri)
				if err != nil {
					continue
				}
				out <- &model.LinkToken{
					Link:     parsed,
					Priority: 1,
				}
			}
		}
	}()
	return out
}

func (ws *WebScraper) Run(urls chan *model.LinkToken) error {
	if err := ws.LoadVisitedUrls(ws.visited); err != nil {
		return err
	}

	defer func() {
		ws.SaveVisitedUrls(ws.visited)
		ws.SaveHashArrays()
	}()

	go func() {
		for _, uri := range ws.cfg.StartURLs {
			parsed, err := url.Parse(uri)
			if err != nil {
				continue
			}
			urls <- &model.LinkToken{
				Link:     parsed,
				Priority: 1,
			}
		}
	}()

	ws.dispatch(urls)
	ws.pool.Wait()
	ws.pool.Stop()
	return nil
}

func (ws *WebScraper) dispatch(urls chan *model.LinkToken) {
	for {
		select {
		case <-ws.globalCtx.Done():
			return

		case uri := <-urls:
			ws.pool.Submit(uri)

		}
	}
}

func (ws *WebScraper) Packed(link *model.LinkToken) model.CompletionState {
	log := model.NewLogger(slog.New(slog.NewJSONHandler(ws.cfg.LogOutput, &slog.HandlerOptions{
		ReplaceAttr: model.Replacer,
		Level:       slog.LevelDebug,
	})).With(
		slog.Group("node_properties",
			slog.String("url", link.Link.String()),
			slog.Int("depth", link.Depth),
			slog.Float64("priority", link.Priority),
		),
	))
	c, cancel := context.WithTimeout(context.WithValue(ws.globalCtx, model.DefLogKey, log), crawlTime)
	defer cancel()
	return ws.ScrapeWithContext(c, link)
}

func (ws *WebScraper) ScrapeWithContext(ctx context.Context, curLink *model.LinkToken) model.CompletionState {
	if ws.checkContext(ctx) {
		return model.Canceled
	}

	normalized, err := normalizeUrl(curLink.Link)
	if err != nil {
		return model.Error
	}

	links, rls, err := ws.fetchPageRulesAndOffers(ctx, curLink.Link)
	if err.Error() == BaseXMLPageError || ws.checkContext(ctx) {
		return model.Done
	}
	host := curLink.Link.Hostname()
	ws.mu.Lock()
	if rls != nil && ws.rulesCache.Get(host) == nil {
		ws.rulesCache.Put(host, rls)
	}
	ws.mu.Unlock()
	hashed := sha256.Sum256([]byte(normalized))
	load := false

	log := ctx.Value(model.DefLogKey).(*model.Logger)
	if log == nil {
		return model.Done
	}

	priority := 1.0
	visPenalty := 0

	if len(links) == 0 && (err == nil || err.Error() != BaseXMLPageError) {
		if prevDepth, loaded := ws.visited.LoadOrStore(normalized, curLink.Depth); loaded && prevDepth.(int) <= curLink.Depth {
			return model.Done
		} else if loaded {
			load = true
			ws.visited.Swap(normalized, curLink.Depth)
			if v := ws.lru.Get(hashed); v != nil {
				links = v.([]*model.LinkToken)
			} else {
				encoded, err := ws.GetPageUrlsByHash(hashed)
				if err != nil {
					if err.Error() != "pebble: not found" {
						log.Errorf("error getting urls, from db: %v", err)
					}
					return model.Error
				}
				if err := gob.NewDecoder(bytes.NewBuffer(encoded)).Decode(&links); err != nil {
					log.Errorf("error unmarshalling urls from db: %v", err)
					return model.Error
				}
				if len(links) != 0 {
					ws.lru.Put(hashed, links)
				}
			}
			visPenalty = prevDepth.(int) - curLink.Depth
		} else {
			if links, err = ws.fetchHTMLcontent(ctx, &priority, curLink.Link, normalized, curLink.Depth); err != nil {
				return model.Error
			}
			visPenalty = 0
		}

		if len(links) == 0 {
			log.Debugf("empty links")
			return model.Done
		}

		if !load {
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(links); err != nil {
				log.Errorf("error marshalling urls: %v", err)
				return model.Error
			}

			if err := ws.IndexUrlsByHash(hashed, buf.Bytes()); err != nil {
				log.Errorf("error saving urls: %v", err)
				return model.Error
			}
		}
	}

	for _, link := range links {
		if ws.cfg.OnlySameDomain && !link.SameDomain {
			continue
		}

		if ws.checkContext(ctx) {
			return model.Canceled
		}

		link.Depth = curLink.Depth + 1
		link.Priority = priority
		if link.SameDomain {
			link.Priority *= 2
		}
		depthPriority := 1.0 / float64(link.Depth) / float64(visPenalty)
		link.Priority = link.Priority * 3 * depthPriority * math.Pow(curLink.Priority, 0.6)

		ws.pool.Submit(link)
	}
	return model.Done
}

func (ws *WebScraper) checkContext(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
	}
	return false
}