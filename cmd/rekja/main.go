package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cbartram/rekja/internal/app"
	"github.com/cbartram/rekja/internal/config"
	"github.com/cbartram/rekja/internal/inventory"
	"github.com/cbartram/rekja/internal/kube"
	"github.com/cbartram/rekja/internal/sync"
	"github.com/cbartram/rekja/internal/thunderstore"
	"github.com/cbartram/rekja/internal/tui"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to a Rekja config file")
	flag.Parse()

	fmt.Fprintf(os.Stdout, "Loading config from: %s", configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	httpClient := &http.Client{Timeout: 90 * time.Second}
	thunderstoreClient := thunderstore.NewClient(cfg.ThunderstoreBaseURL, httpClient)
	store := inventory.NewStore(cfg.ManifestPath())
	scanner := inventory.NewScanner(cfg.PluginsDir, store)
	syncEngine := sync.NewEngine(cfg.PluginsDir, cfg.WorkDir(), thunderstoreClient, store)

	kubeClient, err := kube.New(cfg.Kubernetes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize kubernetes client: %v\n", err)
		os.Exit(1)
	}

	service := app.NewService(app.Dependencies{
		Config:       cfg,
		Scanner:      scanner,
		Store:        store,
		Thunderstore: thunderstoreClient,
		Sync:         syncEngine,
		Kubernetes:   kubeClient,
	})

	model := tui.NewModel(context.Background(), service)
	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "rekja failed: %v\n", err)
		os.Exit(1)
	}
}
