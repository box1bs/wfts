package app

import (
	"context"
	"errors"
	"os"
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
}

const (
	queueIsFull = "queue is full"
	queryTooBig = "query is too big"
	isNotExist = "scraper is not running"
	isExist = "scraper not running"
	maxUrls = 50
)

func Init(outerCtx context.Context, config *configs.ConfigData, capacityMult int) (*Factory, error) {
	f := &Factory{}
	repos, err := repository.NewIndexRepository(outerCtx, config.IndexPath, config.WorkersCount, outerCtx.Value(model.DefLogKey).(*model.Logger), func(i int) repository.Cacher {
		return lru.NewLRUCache(i)
	})
	if err != nil {
		return nil, err
	}
	f.indexer, err = indexer.NewIndexer(repos, config)
	if err != nil {
		return nil, err
	}
	f.searcher = searcher.NewSearcher(f.indexer)
	f.complCh = make(chan string, config.WorkersCount * capacityMult)
	return f, nil
}

func (f *Factory) Search(outerCtx context.Context, query string, resultsCap int) *model.SearchResult {
	ans := &model.SearchResult{}
	ans.Docs, ans.Rels, ans.Metrics = f.searcher.Search(outerCtx.Value(model.DefLogKey).(*model.Logger), query, resultsCap)
	return ans
}

func (f *Factory) AddUrlsToProcess(outerCtx context.Context, urls []string) error {
	select {
	case <-f.innerCtx.Done():
		return errors.New(isNotExist)

	default:
	}

	log := outerCtx.Value(model.DefLogKey).(*model.Logger)
	for i, url := range urls {
		if i > maxUrls {
			log.Debugf("urls count is too big, stopped after %d urls", i+1)
			return errors.New(queryTooBig)
		}

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
	if f.innerCtx != nil {
		select {
		case <-f.innerCtx.Done():
		default:
			return errors.New(isExist)
	
		}
	}
	f.innerCtx, f.controller = context.WithCancel(outerCtx)
	f.scraper = scraper.NewScraper(scraper.NewScrapeConfig(config.BaseURLs, config.BackupPath, os.Stdout, config.WorkersCount, config.MaxDepth, config.OnlySameDomain), f.indexer, f.innerCtx)
	return f.scraper.Run(f.scraper.PrepareChan(f.complCh))
}

func (f *Factory) StopCrawling(outerCtx context.Context) error {
	if f.innerCtx != nil {
		select {
		case <-f.innerCtx.Done():
			return errors.New(isNotExist)

		default:
		}
	}

	log := outerCtx.Value(model.DefLogKey).(*model.Logger)
	f.controller()
	log.Infof("scraper stopping...")
	return nil
}