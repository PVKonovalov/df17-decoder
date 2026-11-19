package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/PVKonovalov/df17-decoder/internal/app"
	"github.com/PVKonovalov/df17-decoder/internal/config"
)

func main() {
	configPath := flag.String("config", "", "path to the YAML config file (empty: built-in defaults)")
	logLevel := flag.String("log-level", "", "override log.level (debug|info|warn|error)")
	flag.Parse()

	cfg, err := config.Load(*configPath, config.Overrides{LogLevel: *logLevel})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := app.NewLogger(cfg.Log, os.Stderr)
	if _, err = app.Run(ctx, cfg, log); err != nil {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
}
