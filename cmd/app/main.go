package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"wfts/configs"
	"wfts/internal/model"
	"wfts/internal/repository"
	"wfts/internal/services/ui"
	"wfts/internal/services/wfts/offline/indexer"
	"wfts/internal/services/wfts/offline/scraper"
	lrucache "wfts/internal/services/wfts/offline/scraper/lruCache"
	"wfts/internal/services/wfts/online/searcher"
)

func main() {
	var (
		configFile = flag.String("config", "default.json", "Path to configuration file")
		indexFlag = flag.Bool("i", false, "disable indexing")
		interfaceFlag = flag.Bool("gui", false, "use terminal UI")
	)
	flag.Parse()

	cfg, err := configs.UploadLocalConfiguration(*configFile)
	if err != nil {
		panic(err)
	}

	if *interfaceFlag {
		initGUI(cfg, *indexFlag)
		return
	}
	
	out := os.Stdout
	if cfg.LogPath != "" {
		out, err = os.OpenFile(cfg.LogPath, os.O_WRONLY | os.O_CREATE, 0600)
		if err != nil {
			panic(err)
		}
	}
	defer out.Close()

	log := model.NewLogger(slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{
		ReplaceAttr: model.Replacer,
		Level: slog.LevelDebug,
	})))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ir, err := repository.NewIndexRepository(ctx, cfg.IndexPath, cfg.WorkersCount, log, func(i int) repository.Cacher { // мне вообще не нравится импортить внутренний пакет основного пакета, но я не хочу lru дважды описывать
		return lrucache.NewLRUCache(i)
	})
	if err != nil {
		panic(err)
	}
	defer ir.DB.Close()

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		fmt.Println("\nShutting down...")
		cancel()
		//os.Exit(1)
	}()

	i, err := indexer.NewIndexer(ir, cfg)
	if err != nil {
		panic(err)
	}
	if !*indexFlag {
		ws := scraper.NewScraper(scraper.NewScrapeConfig(cfg.BaseURLs, cfg.BackupPath, out, cfg.WorkersCount, cfg.MaxDepth, cfg.OnlySameDomain), i, ctx)
		if err := ws.Run(); err != nil {
			panic(err)
		}
	}

	count, err := ir.GetDocumentsCount()
	if err != nil {
		panic(err)
	}

	fmt.Printf("Index built with %d documents. Enter search queries (q to exit):\n", count)
	s := searcher.NewSearcher(i, ir)
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\n> ")
		query, _ := reader.ReadString('\n')
		query = strings.TrimSpace(query)
		if query == "q" {
			return
		}
		topN, topNMetrics, metrics := s.Search(out, query, 100)
		Present(topN, topNMetrics, metrics)
	}
}

func Present(docs []*model.Document, docMetrics []*model.DocRanking, metrics *model.SearchMetrics) {
	if len(docs) == 0 {
		fmt.Println("No results found.")
		return
	}
	
	fmt.Printf("Found %d results:\n", len(docs))
	fmt.Printf("\ntime costs:\nquery handling: %v\nfetching and processing: %v\nsort: %v\n\ntotal: %v\n\ntotal results: %d\n\n", metrics.HandleQuery, metrics.FetchAndProcess, metrics.Sort, metrics.Total, metrics.TotalResults)
	for i, doc := range docs {
		fmt.Printf("%d. URL: %s\nmetrics: tf idf: %.4f, bm25: %.10f, log length words in url: %.4f, term proximity: %d, has word in header: %t\n", 
			i+1, doc.URL, docMetrics[i].Tf_Idf, docMetrics[i].BM25, docMetrics[i].LogLenWordInURL, docMetrics[i].TermProximity, docMetrics[i].HasWordInHeader)
	}
}

func initGUI(cfg *configs.ConfigData, indexF bool) {
	lw := ui.NewLogWriter(1000)
	log := model.NewLogger(slog.New(slog.NewJSONHandler(lw, &slog.HandlerOptions{
		ReplaceAttr: model.Replacer,
	})))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ir, err := repository.NewIndexRepository(ctx, cfg.IndexPath, cfg.WorkersCount, log, func(i int) repository.Cacher {
		return lrucache.NewLRUCache(i)
	})
	if err != nil {
		panic(err)
	}
	defer ir.DB.Close()

	done := make(chan struct{})
	i, err := indexer.NewIndexer(ir, cfg)
	if err != nil {
		panic(err)
	}

	if !indexF {
		go func() {
			ws := scraper.NewScraper(scraper.NewScrapeConfig(cfg.BaseURLs, cfg.BackupPath, lw, cfg.WorkersCount, cfg.MaxDepth, cfg.OnlySameDomain), i, ctx)
			if err := ws.Run(); err != nil {
				model.NewLogger(slog.New(slog.NewJSONHandler(lw, &slog.HandlerOptions{
					ReplaceAttr: model.Replacer,
				}))).Errorf("%v, scraping canceled", err)
			}
			close(done)
		}()
	} else {
		close(done)
	}
	manager := ui.New(0.3, 0.4, 0.15, lw, ir.GetDocumentsCount, searcher.NewSearcher(i, ir).Search)
	if err := manager.Run(cancel); err != nil && err.Error() != "quit" {
		model.NewLogger(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			ReplaceAttr: model.Replacer,
		}))).Errorf("%v", err)
	}
	cancel()
	<-done
}