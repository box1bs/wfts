package main

import (
	"context"
	"flag"
	"wfts/configs"
	"wfts/internal/app"
	"wfts/internal/services/api"
)

func main() {
	var (
		configFile = flag.String("config", "default.json", "Path to configuration file")
		// indexFlag = flag.Bool("i", false, "disable indexing")
		port = flag.String("p", ":8080", "server port")
	)
	flag.Parse()

	cfg, err := configs.UploadLocalConfiguration(*configFile)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // дефер в main да да, да да да
	factory, err := app.Init(ctx, cfg, 10)
	if err != nil {
		panic(err)
	}
	srv := api.NewServer(ctx, cfg, factory)
	srv.Start(*port)
}