package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"insider-one/internal/app"
	"insider-one/internal/config"
	"insider-one/internal/logging"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		panic(err)
	}
	application, err := app.NewWithConfig(cfg, logger)
	if err != nil {
		logger.Error("init app", "err", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           application.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("notification service listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}

	_ = application.Close()
}
