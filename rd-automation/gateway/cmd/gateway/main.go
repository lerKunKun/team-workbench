package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"rd-gateway/internal/automation"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg, err := automation.Load()
	if err != nil {
		log.Fatal(err)
	}
	start, stop := context.WithTimeout(ctx, 30*time.Second)
	st, err := automation.OpenStore(start, cfg.DBPassword)
	stop()
	if err != nil {
		log.Fatal("database connection/migration failed; check app role and PostgreSQL health")
	}
	defer st.Close()
	s := automation.NewServer(cfg, st)
	var jobs sync.WaitGroup
	jobs.Go(func() { s.Work(ctx) })
	go s.Stream(ctx)
	log.Printf("features: demo=%t V2-configured=%t stream-requested=%t", cfg.Demo, cfg.V2Ready(), cfg.Stream)
	h := &http.Server{Addr: cfg.Listen, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 95 * time.Second, IdleTimeout: 45 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		if err := h.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP listener failed: %v", err)
			cancel()
		}
	}()
	<-ctx.Done()
	shutdown, done := context.WithTimeout(context.Background(), 6*time.Second)
	defer done()
	if h.Shutdown(shutdown) != nil {
		_ = h.Close()
	}
	jobs.Wait()
}
