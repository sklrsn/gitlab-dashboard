package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitlab-dashboard/internal/config"
	"gitlab-dashboard/internal/dashboard"
	"gitlab-dashboard/internal/gitlab"
	"gitlab-dashboard/internal/server"
	"gitlab-dashboard/web"
)

// Set at build time with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = "unknown"
)

const usage = `gitlab-dashboard — read-only GitLab pipeline dashboard

Usage:
  gitlab-dashboard [serve]   start the server (configured by environment variables)
  gitlab-dashboard version   print version information
`

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		if err := serve(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Printf("gitlab-dashboard %s (commit %s)\n", version, commit)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func serve() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	gl, err := gitlab.New(gitlab.Options{BaseURL: cfg.GitLabURL, Token: cfg.GitLabToken, CAFile: cfg.GitLabCAFile, Timeout: cfg.RequestTimeout})
	if err != nil {
		return err
	}
	svc := dashboard.NewService(gl, cfg.CacheTTL, cfg.GroupFanoutLimit)
	srv, err := server.New(cfg, svc, web.FS(), log)
	if err != nil {
		return err
	}

	hs := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		tls := cfg.TLSCertFile != ""
		log.Info("starting", "version", version, "addr", cfg.ListenAddr, "tls", tls, "gitlab", cfg.GitLabURL.String())
		if tls {
			errc <- hs.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			errc <- hs.ListenAndServe()
		}
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return hs.Shutdown(shutCtx)
	}
	return nil
}
