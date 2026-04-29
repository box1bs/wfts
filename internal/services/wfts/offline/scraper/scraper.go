package scraper

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"

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
	ScratchPath    string
	LogOutput      io.Writer
	WorkersNum     int
	Depth          int
	OnlySameDomain bool
}

const canceled = "context canceled"

func NewScrapeConfig(baseUrls []string, ScratchPath string, logWriter io.Writer, workerNum, depth int, onlySameDomain bool) *configData {
	return &configData{
		StartURLs:      baseUrls,
		ScratchPath:    ScratchPath,
		LogOutput:      logWriter,
		WorkersNum:     workerNum,
		Depth:          depth,
		OnlySameDomain: onlySameDomain,
	}
}

const (
	userAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0"
	crawlTime    = 300 * time.Second
	deadlineTime = 15 * time.Second
	numOfTries   = 3 // если кто то решил поменять это на 0, чтож, удачи
)

func NewScraper(cfg *configData, idx indexer, c context.Context) *WebScraper {
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
	ws.pool = scheduler.NewWorkerPool(cfg.WorkersNum, cfg.WorkersNum*50)
	return ws
}

func (ws *WebScraper) PrepareChan(rawUrls chan string) chan *linkToken {
	out := make(chan *linkToken, ws.cfg.WorkersNum*10)
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
				out <- &linkToken{
					Link:     parsed,
					Priority: 1,
				}
			}
		}
	}()
	return out
}

func (ws *WebScraper) Run(urls chan *linkToken) error {
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
			urls <- &linkToken{
				Link:     parsed,
				Priority: 1,
			}
		}
		links, _ := ws.fromScratchMark()
		p, len := 0, len(links)
		t := time.NewTicker(time.Millisecond * 500)
		defer t.Stop()

		for range t.C {
			if p == len {
				return
			}

			select {
			case <-ws.globalCtx.Done():
				return
			case urls <- links[p]:
				p++
			default:

			}
		}
	}()

	ws.dispatch(urls)
	ws.pool.Wait()
	ws.pool.Stop()
	return ws.makeScratchMark(ws.pool.Backup())
}

func (ws *WebScraper) dispatch(urls chan *linkToken) {
	for {
		select {
		case <-ws.globalCtx.Done():
			return

		case uri := <-urls:
			log := model.NewLogger(slog.New(slog.NewJSONHandler(ws.cfg.LogOutput, &slog.HandlerOptions{
				ReplaceAttr: model.Replacer,
				Level:       slog.LevelDebug,
			})).With(
				slog.Group("node_properties",
					slog.String("url", uri.Link.String()),
					slog.Int("depth", uri.Depth),
					slog.Float64("priority", uri.Priority),
				),
			))
			ws.pool.Submit(&model.CrawlNode{Activation: func() model.CompletionState {
				ws.rlCache.Put(uri.Link.Hostname(), NewRateLimiter(DefaultDelay))
				ctx, cancel := context.WithTimeout(context.WithValue(ws.globalCtx, model.DefLogKey, log), crawlTime)
				defer cancel()
				return ws.ScrapeWithContext(ctx, uri)
			}, Priority: uri.Priority, CrawlToken: uri})

		}
	}
}

func (ws *WebScraper) ScrapeWithContext(ctx context.Context, curLink *linkToken) model.CompletionState {
	if ws.checkContext(ctx) {
		return model.Canceled
	}

	if curLink.Depth >= ws.cfg.Depth {
		return model.Error
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
		return model.Canceled
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
				links = v.([]*linkToken)
			} else {
				encoded, err := ws.GetPageUrlsByHash(hashed)
				if err != nil {
					if err.Error() != "Key not found" {
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
			visPenalty = ws.cfg.Depth - (prevDepth.(int) - curLink.Depth)
		} else {
			if links, err = ws.fetchHTMLcontent(ctx, &priority, curLink.Link, normalized, curLink.Depth); err != nil {
				return model.Error
			}
			visPenalty = ws.cfg.Depth - (prevDepth.(int) - curLink.Depth)
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
		link.Priority = link.Priority / (float64(curLink.Depth) + 1) * (curLink.Priority * 10) * math.Exp(-0.6*float64(visPenalty))

		ws.pool.Submit(&model.CrawlNode{Activation: func() model.CompletionState {
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
		},
			Priority:   link.Priority,
			CrawlToken: link,
		})
	}
	return model.Done
}

func (ws *WebScraper) makeScratchMark(toMark []any) error {
	var buf bytes.Buffer
	for _, t := range toMark {
		token := t.(*linkToken)
		if _, err := fmt.Fprintf(&buf, "%s|%.16f|%d\n", token.Link.String(), token.Priority, token.Depth); err != nil {
			return err
		}
	}
	return os.WriteFile(ws.cfg.ScratchPath, buf.Bytes(), 0600)
}

func (ws *WebScraper) fromScratchMark() ([]*linkToken, error) {
	file, err := os.OpenFile(ws.cfg.ScratchPath, os.O_RDONLY|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	saved := string(data[:len(data)-1])
	tokens := strings.Split(saved, "\n")
	tlen := len(tokens)
	result := make([]*linkToken, 0)
	for i := range tlen {
		parts := strings.Split(tokens[i], "|")
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid backup format")
		}
		token := linkToken{}
		token.Link, err = url.Parse(parts[0])
		if err != nil {
			return nil, err
		}
		token.Priority, err = strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return nil, err
		}
		token.Depth, err = strconv.Atoi(parts[2])
		if err != nil {
			return nil, err
		}
		result = append(result, &token)
	}
	return result, nil
}

func (ws *WebScraper) checkContext(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
	}
	return false
}
