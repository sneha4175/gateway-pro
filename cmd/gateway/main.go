package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sneha4175/gateway-pro/internal/admin"
	"github.com/sneha4175/gateway-pro/internal/config"
	"github.com/sneha4175/gateway-pro/internal/middleware"
	"github.com/sneha4175/gateway-pro/internal/proxy"
	"go.uber.org/zap"
)

var (
	version   = "dev"
	buildTime = "unknown"
	commit    = "none"
)

func main() {
	var (
		configPath  = flag.String("config", "configs/gateway.yaml", "path to config file")
		showVersion = flag.Bool("version", false, "show version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("gateway-pro version=%s commit=%s buildTime=%s\n", version, commit, buildTime)
		os.Exit(0)
	}

	// Bootstrap logger
	rawLogger, _ := zap.NewProduction()
	log := rawLogger.Sugar()
	defer log.Sync() //nolint:errcheck

	log.Infow("starting gateway-pro", "version", version, "config", *configPath)

	// Load config (supports hot-reload)
	cfg, watcher, err := config.LoadAndWatch(*configPath, log)
	if err != nil {
		log.Fatalw("failed to load config", "err", err)
	}
	defer watcher.Close()

	// Initialize trace store
	var traceStore *middleware.TraceStore
	if cfg.Tracing.Enabled {
		traceStore = middleware.NewTraceStore(100)
		log.Infow("tracing enabled", "service", cfg.Tracing.ServiceName, "exporter", cfg.Tracing.Exporter)
	}

	// Initialize auth validator
	var authCfg *config.AuthConfig
	if cfg.Auth.Enabled {
		authCfg = &cfg.Auth
		log.Infow("auth enabled", "skip_paths", cfg.Auth.SkipPaths)
	}

	// Build the handler chain
	gw, err := proxy.NewGateway(cfg, log, authCfg, traceStore)
	if err != nil {
		log.Fatalw("failed to build gateway", "err", err)
	}

	// Wire hot-reload: when config changes, swap backends live
	go func() {
		for newCfg := range watcher.Updates() {
			log.Infow("config reloaded, applying changes")
			if err := gw.Reload(newCfg); err != nil {
				log.Errorw("reload failed", "err", err)
			}
		}
	}()

	// Metrics + health on a separate port so it's never behind auth middleware
	adminMux := http.NewServeMux()
	gw.RegisterAdminHandlers(adminMux)

	// Register trace handlers if tracing enabled
	if traceStore != nil {
		admin.RegisterTraceHandlers(adminMux, traceStore)
	}

	adminSrv := &http.Server{
		Addr:         cfg.Admin.Addr,
		Handler:      adminMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Main proxy server
	mainSrv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      middleware.Recovery(log)(gw),
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start both servers
	go func() {
		log.Infow("admin server listening", "addr", cfg.Admin.Addr)
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalw("admin server failed", "err", err)
		}
	}()

	go func() {
		log.Infow("proxy server listening", "addr", cfg.Server.Addr)
		if err := mainSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalw("proxy server failed", "err", err)
		}
	}()

	// Graceful shutdown on SIGTERM / SIGINT
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Infow("shutting down gracefully…")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_ = adminSrv.Shutdown(ctx)
	if err := mainSrv.Shutdown(ctx); err != nil {
		log.Errorw("graceful shutdown failed", "err", err)
	}
	log.Infow("goodbye")
}
