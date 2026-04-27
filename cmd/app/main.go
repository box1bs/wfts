package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"wfts/configs"
	"wfts/internal/app"
	"wfts/internal/model"
	"wfts/internal/services/api"
)

func main() {
	ctx := context.Background()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error occured: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	backupWG := &sync.WaitGroup{}
	var (
		configFile = flag.String("config", "default.json", "Path to configuration file")
		// indexFlag = flag.Bool("i", false, "disable indexing")
	)
	flag.Parse()

	cfg, err := configs.UploadLocalConfiguration(*configFile)
	if err != nil {
		return err
	}

	log := model.NewLogger(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: model.Replacer,
		Level: slog.LevelDebug,
	})))
	valued := context.WithValue(ctx, model.DefLogKey, log)
	factory, err := app.Init(valued, backupWG, cfg, 10)
	if err != nil {
		return err
	}
	if err := api.NewServer(valued, cfg, factory).Start(); err != nil && err != http.ErrServerClosed {
		return err
	}
	backupWG.Wait()
	return nil
}