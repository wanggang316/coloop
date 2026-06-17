// Command the-loop is the daemon and CLI entry point. With no subcommand (or
// "serve") it runs the scheduler/HTTP server; any other subcommand is handled
// by the thin HTTP client in internal/cli.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wanggang316/the-loop/internal/api"
	"github.com/wanggang316/the-loop/internal/cli"
	"github.com/wanggang316/the-loop/internal/config"
	"github.com/wanggang316/the-loop/internal/engine"
	"github.com/wanggang316/the-loop/internal/executor"
	"github.com/wanggang316/the-loop/internal/model"
	"github.com/wanggang316/the-loop/internal/notify"
	"github.com/wanggang316/the-loop/internal/store"
	"github.com/wanggang316/the-loop/internal/web"
)

// defaultConfig is the config path used when --config is not given.
const defaultConfig = "~/.the-loop/config.yaml"

// shutdownTimeout bounds graceful server shutdown and engine drain.
const shutdownTimeout = 30 * time.Second

func main() {
	log.SetFlags(0)

	fs := flag.NewFlagSet("the-loop", flag.ExitOnError)
	cfgFlag := fs.String("config", defaultConfig, "path to config.yaml")
	_ = fs.Parse(os.Args[1:])

	cfgPath := expandHome(*cfgFlag)

	args := fs.Args()
	cmd := "serve"
	var rest []string
	if len(args) > 0 {
		cmd = args[0]
		rest = args[1:]
	}

	if cmd == "serve" {
		runServe(cfgPath)
		return
	}
	os.Exit(cli.Dispatch(cmd, rest, cfgPath))
}

// runServe boots the daemon: load config, build the engine and HTTP handler,
// watch the config for changes, and serve until a termination signal arrives.
func runServe(cfgPath string) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("the-loop: %v", err)
	}

	st, err := store.New(cfg.DataDir)
	if err != nil {
		log.Fatalf("the-loop: %v", err)
	}

	disp := notify.NewDispatcher(cfg.Notifiers)
	eng := engine.New(cfg, st, disp, executor.NewShell(cfg.Shell), executor.NewAgent(cfg.Shell))
	eng.SetConfigPath(cfgPath) // enable task create/update/delete via the API
	eng.Start()

	reload := func() error {
		nc, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		return eng.Reload(nc)
	}

	h := api.New(eng, web.FS(), reload)

	if w, err := config.Watch(cfgPath, func() {
		if err := reload(); err != nil {
			log.Printf("the-loop: config reload failed: %v", err)
		} else {
			log.Printf("the-loop: config reloaded")
		}
	}); err != nil {
		log.Printf("the-loop: config watch disabled: %v", err)
	} else {
		defer w.Close()
	}

	// Timeouts harden against slowloris / connection exhaustion. WriteTimeout is
	// deliberately left at 0 (unbounded) so long-lived SSE streams (/stream) are
	// not cut off mid-response.
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	banner(cfg, disp)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Fatalf("the-loop: serve: %v", err)
	case sig := <-sigCh:
		log.Printf("the-loop: received %s, shutting down", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("the-loop: server shutdown: %v", err)
	}
	if err := eng.Stop(ctx); err != nil {
		log.Printf("the-loop: engine stop: %v", err)
	}
}

// banner prints a one-line startup summary.
func banner(cfg *model.Config, disp *notify.Dispatcher) {
	channels := strings.Join(disp.Names(), ",")
	if channels == "" {
		channels = "none"
	}
	log.Printf("the-loop listening on %s  tasks=%d  notifiers=%s  data=%s",
		cfg.Listen, len(cfg.Tasks), channels, cfg.DataDir)
	// An empty host (e.g. ":8765") binds every interface, exposing the
	// unauthenticated API to the LAN. Warn so a non-loopback bind is noticed.
	if host, _, err := net.SplitHostPort(cfg.Listen); err == nil && host == "" {
		log.Printf("the-loop: WARNING: listen address %q has no host and binds all interfaces; the API is unauthenticated and reachable from the network", cfg.Listen)
	}
}

// expandHome rewrites a leading "~/" (or a bare "~") to the user's home dir.
func expandHome(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}
