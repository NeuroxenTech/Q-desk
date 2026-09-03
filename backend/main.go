package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := loadConfig()

	// Connect to Upstash Redis. The URL (UPSTASH_REDIS_URL) already encodes
	// host, port, password and TLS; ParseURL must not be split up.
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		// Do not hard-fail so the server can still come up in dev without Redis.
		log.Printf("warning: redis not reachable: %v", err)
	}

	// Connect to Supabase Postgres.
	pool, err := newPgxPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	allowed := func(origin string) bool {
		for _, o := range cfg.AllowedOrigins {
			if origin == o {
				return true
			}
		}
		return false
	}

	app := &app{
		cfg:      cfg,
		st:       &store{rdb: rdb, db: pool},
		sessions: make(map[string]*session),
		up:       newWSUpgrader(allowed),
		stg:      newStorageClient(cfg.SupabaseURL, cfg.SupabaseServiceKey, cfg.MaxUploadSizeMB),
		malware:  unconfiguredScanner{},
	}
	cry, err := newServerCrypto(cfg)
	if err != nil {
		log.Fatalf("init crypto: %v", err)
	}
	app.cry = cry

	// Create the private evidence bucket at startup (idempotent; no-op when the
	// service role key is missing).
	if err := app.stg.ensureBucket(ctx); err != nil {
		log.Printf("warning: evidence storage bucket not ensured: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/handshake", app.handleHandshake)
	mux.HandleFunc("/api/ticket/request", app.handleTicketRequest)
	mux.HandleFunc("/api/stream", app.handleStream)
	mux.HandleFunc("/api/append", app.handleAppend)
	mux.HandleFunc("/api/breach", app.handleBreach)
	mux.HandleFunc("/api/versions", app.handleVersions)
	mux.HandleFunc("/api/versions/branch", app.handleBranch)
	mux.HandleFunc("/api/versions/merge", app.handleMerge)
	mux.HandleFunc("/api/versions/{documentId}/tree", app.handleVersionTree)
	mux.HandleFunc("/api/upload", app.handleUpload)
	mux.HandleFunc("/api/files/{versionID}/download-url", app.handleDownloadURL)
	mux.HandleFunc("/api/files/{versionID}/content", app.handleFileContent)
	mux.HandleFunc("/api/stream/versions", app.handleStreamVersions)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      app.corsMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Printf("q-desk backend listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = rdb.Close()
}
