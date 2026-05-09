package app

import (
	"context"
	"errors"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
	"wfts/configs"
	"wfts/internal/model"
	"wfts/internal/repository"
	"wfts/internal/services/wfts/offline/indexer"
	"wfts/internal/services/wfts/offline/scraper"
	lru "wfts/internal/services/wfts/offline/scraper/lruCache"
	"wfts/internal/services/wfts/online/searcher"
)

type Factory struct {
	scraper 	*scraper.WebScraper
	searcher 	*searcher.Searcher
	indexer  	*indexer.Indexer
	complCh 	chan string
	innerCtx 	context.Context
	controller	context.CancelFunc
	startPoint  time.Time
	backupWG 	*sync.WaitGroup
}

const (
	queueIsFull = "queue is full"
	isNotExist = "scraper is not running"
	isExist = "scraper is running"
	maxUrls = 50
)

func Init(outerCtx context.Context, wg *sync.WaitGroup, config *configs.ConfigData, capacityMult int) (*Factory, error) {
	f := &Factory{backupWG: wg}
	repos, err := repository.NewIndexRepository(outerCtx, wg, config.IndexPath, config.WorkersCount, outerCtx.Value(model.DefLogKey).(*model.Logger), func(i int) repository.Cacher {
		return lru.NewLRUCache(i)
	})
	debug.SetMemoryLimit(600 << 20)
	if err != nil {
		return nil, err
	}
	f.indexer, err = indexer.NewIndexer(repos, config)
	if err != nil {
		return nil, err
	}
	f.searcher = searcher.NewSearcher(f.indexer)
	f.complCh = make(chan string, config.WorkersCount * capacityMult)
	go func() {
		<-outerCtx.Done()
		close(f.complCh)
	}()
	return f, nil
}

func (f *Factory) Search(outerCtx context.Context, query string, resultsCap int) *model.SearchResult {
	ans := &model.SearchResult{}
	ans.Docs, ans.Rels, ans.Metrics = f.searcher.Search(outerCtx.Value(model.DefLogKey).(*model.Logger), query, resultsCap)
	return ans
}

func (f *Factory) AddUrlsToProcess(outerCtx context.Context, urls []string) error {
	if !f.isRunning() {
		return errors.New(isNotExist)
	}

	log := outerCtx.Value(model.DefLogKey).(*model.Logger)
	for i, url := range urls {
		select {
		case f.complCh <- url:
			log.Infof("url %s added", url)

		case <-f.innerCtx.Done():
			log.Debugf("context canceled after %d urls", i+1)
			return f.innerCtx.Err()

		default:
			log.Debugf("queue is full, urls after %dth will be dropped", i + 1)
			return errors.New(queueIsFull)

		}
	}
	return nil
}

func (f *Factory) StartCrawling(outerCtx context.Context, config *configs.ConfigData) error {
	if f.isRunning() {
		return errors.New(isExist)
	}
	var err error
	f.startPoint = time.Now()
	f.innerCtx, f.controller = context.WithCancel(outerCtx)
	f.scraper, err = scraper.NewScraper(scraper.NewScrapeConfig(config.BaseURLs, config.BackupPath, os.Stdout, config.WorkersCount, config.OnlySameDomain), f.indexer, f.innerCtx)
	if err != nil {
		return err
	}
	f.backupWG.Go(func() {
		f.scraper.Run(f.scraper.PrepareChan(f.complCh))
	})
	return nil
}

func (f *Factory) GetCurrentState() (*model.CrawlState, error) {
	docs, err := f.indexer.GetDocumentsCount()
	if err != nil {
		return nil, err
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return &model.CrawlState{
		LastStart: f.startPoint.Format("15:04:05 01-02-2006"),
		Uptime: f.getUptimeTime().String(),
		Allocated: mem.Alloc >> 20,
		MemFromOS: mem.Sys >> 20,
		HeapIdle: mem.HeapIdle >> 20,
		HeapInUse: mem.HeapInuse >> 20,
		DocsInIndex: docs,
		IsRunning: f.isRunning(),
	}, nil
}

func (f *Factory) getUptimeTime() time.Duration {
	if !f.isRunning() {
		return 0
	}
	return time.Since(f.startPoint)
}

func (f *Factory) isRunning() bool {
	if f.innerCtx == nil {
		return false
	}
	select {
		case <-f.innerCtx.Done():
			return false
		default:
	
	}
	return true
}

func (f *Factory) StopCrawling(outerCtx context.Context) error {
	if !f.isRunning() {
		return errors.New(isNotExist)
	}

	log := outerCtx.Value(model.DefLogKey).(*model.Logger)
	f.controller()
	f.scraper = nil
	f.innerCtx = nil
	log.Infof("scraper stopping...")
	return nil
}