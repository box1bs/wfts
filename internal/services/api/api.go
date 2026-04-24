package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"
	"wfts/configs"
	"wfts/internal/model"
)

type Factory interface {
	crawler
	searcher
}

type crawler interface {
	AddUrlsToProcess(outerCtx context.Context, urls []string) error
	StartCrawling(outerCtx context.Context, config *configs.ConfigData) error
	StopCrawling(outerCtx context.Context) error

	GetCurrentState() (*model.CrawlState, error)
}

type searcher interface {
	Search(outerCtx context.Context, query string, resultsCap int) *model.SearchResult
}

type server struct {
	factory 	Factory
	global  	context.Context
	cfg 		*configs.ConfigData
}

func NewServer(ctx context.Context, cfg *configs.ConfigData, f Factory) *server {
	return &server{
		factory: f,
		global: ctx,
		cfg: cfg,
	}
}

func (s *server) Start(addr string, port int) error {
	mux := http.NewServeMux()
	srv := &http.Server{Addr: addr + ":" + strconv.Itoa(port), Handler: mux}
	go func() {
		<-s.global.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5 * time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	defaultLogger := s.global.Value(model.DefLogKey).(*model.Logger)
	mux.HandleFunc("POST /crawl/start", func(w http.ResponseWriter, r *http.Request) {
		if err := s.factory.StartCrawling(s.global, s.cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
	})

	mux.HandleFunc("PATCH /crawl/add", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Urls []string `json:"urls" validate:"len=1:50"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := configs.New().Validate(payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := s.factory.AddUrlsToProcess(s.global, payload.Urls); err != nil {
			http.Error(w, err.Error(), http.StatusExpectationFailed)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	})

	mux.HandleFunc("GET /crawl/state", func(w http.ResponseWriter, r *http.Request) {
		state, err := s.factory.GetCurrentState()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			defaultLogger.Errorf("crawling state error: %v", err)
			return
		}

		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(state); err != nil {
			defaultLogger.Errorf("state response sending failed: %v", err)
		}
	})

	mux.HandleFunc("POST /crawl/stop", func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(s.global, model.DefLogKey, defaultLogger.AddAttr(slog.String("user_agent", r.UserAgent())))
		if err := s.factory.StopCrawling(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		params := r.URL.Query()
		query := params["query"]
		if len(query) == 0 {
			http.Error(w, "empty query", http.StatusBadRequest)
			return
		}

		cap := 100
		if capParam := params["cap"]; len(capParam) != 0 {
			tmp, err := strconv.Atoi(capParam[0])
			if err == nil {
				cap = tmp
			}
		}

		local := defaultLogger.AddAttr(slog.Group("search_request", slog.String("query", query[0]), slog.String("user_agent", r.UserAgent())))
		localCtx := context.WithValue(s.global, model.DefLogKey, local)
		result := s.factory.Search(localCtx, query[0], max(cap, 10))
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(result); err != nil {
			local.Errorf("search response sending failed: %v", err)
		}
	})
	return srv.ListenAndServe()
}