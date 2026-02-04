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
	"sync"
	"time"

	"wfts/configs"
	"wfts/internal/model"
	"wfts/internal/repository"
	"wfts/internal/services/ui"
	"wfts/internal/services/wfts/offline/indexer"
	"wfts/internal/services/wfts/offline/scraper"
	"wfts/internal/services/wfts/online/searcher"
)

func main() {
	var (
		configFile = flag.String("config", "configs/app_config.json", "Path to configuration file")
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
	if cfg.InfoLogPath != "-" {
		out, err = os.Create(cfg.InfoLogPath)
		if err != nil {
			panic(err)
		}
	}
	defer out.Close()

	log := model.NewLogger(slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{
		ReplaceAttr: model.Replacer,
	})))

	ir, err := repository.NewIndexRepository(cfg.IndexPath, log, cfg.ChunkSize)
	if err != nil {
		panic(err)
	}
	defer ir.DB.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		fmt.Println("\nShutting down...")
		cancel()
		//os.Exit(1)
	}()

	i := indexer.NewIndexer(ir, cfg)
	if !*indexFlag {
		mp := new(sync.Map)
		ws := scraper.NewScraper(mp, scraper.NewScrapeConfig(cfg.BaseURLs, out, cfg.WorkersCount, cfg.MaxDepth, cfg.OnlySameDomain), i, ctx)
		if err := i.Index(mp, ws); err != nil {
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
		t := time.Now()
		r, _ := s.Search(out, query, 100)
		Present(r)
		fmt.Printf("--Search time: %v--\n", time.Since(t))
	}
}

func Present(docs []*model.Document) {
	if len(docs) == 0 {
		fmt.Println("No results found.")
		return
	}
	
	fmt.Printf("Found %d results:\n", len(docs))
	for i, doc := range docs {
		fmt.Printf("%d. URL: %s\n\n", 
			i+1, doc.URL)
	}
}

func initGUI(cfg *configs.ConfigData, indexF bool) {
	lw := ui.NewLogWriter(1000)
	log := model.NewLogger(slog.New(slog.NewJSONHandler(lw, &slog.HandlerOptions{
		ReplaceAttr: model.Replacer,
	})))
	ir, err := repository.NewIndexRepository(cfg.IndexPath, log, cfg.ChunkSize)
	if err != nil {
		panic(err)
	}
	defer ir.DB.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan struct{}, 1)
	go func() {
		<-c
		cancel()
		//os.Exit(1)
	}()

	i := indexer.NewIndexer(ir, cfg)
	if !indexF {
		go func() {
			mp := new(sync.Map)
			ws := scraper.NewScraper(mp, scraper.NewScrapeConfig(cfg.BaseURLs, lw, cfg.WorkersCount, cfg.MaxDepth, cfg.OnlySameDomain), i, ctx)
			if err := i.Index(mp, ws); err != nil {
				panic(err)
			}
		}()
	}

	manager := ui.New(0.3, 0.4, 0.15, lw, ir.GetDocumentsCount, searcher.NewSearcher(i, ir).Search)
	if err := manager.Run(); err != nil {
		panic(err)
	}
}