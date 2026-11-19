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
	"github.com/PVKonovalov/df17-decoder/internal/version"
)

func main() {
	configPath := flag.String("config", "", "path to the YAML config file (empty: built-in defaults)")
	logLevel := flag.String("log-level", "", "override log.level (debug|info|warn|error)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("df17-decoder", version.Get())
		return
	}

	cfg, err := config.Load(*configPath, config.Overrides{LogLevel: *logLevel})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := app.NewLogger(cfg.Log, os.Stderr)
	v := version.Get()
	log.Info("df17-decoder starting", "version", v.Version, "commit", v.Commit, "go", v.GoVersion)
	svc, err := app.New(cfg, log)
	if err == nil {
		err = svc.Run(ctx)
	}
	if err != nil {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
}
