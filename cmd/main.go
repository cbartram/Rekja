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
	"github.com/cbartram/rekja/internal/grpcclient"
	"github.com/cbartram/rekja/internal/inventory"
	"github.com/cbartram/rekja/internal/resolve"
	"github.com/cbartram/rekja/internal/sync"
	"github.com/cbartram/rekja/internal/thunderstore"
	"github.com/cbartram/rekja/internal/tui"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to a Rekja config file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	thunderstoreClient := thunderstore.NewClient(cfg.ThunderstoreBaseURL, &http.Client{Timeout: 20 * time.Second})
	store := inventory.NewStore(cfg.ManifestPath())
	scanner := inventory.NewScanner(cfg.PluginsDir, store)
	syncEngine := sync.NewEngine(cfg.PluginsDir, cfg.WorkDir(), thunderstoreClient, store)

	// Build server client: prefer gRPC if REKJA_SERVER is set, otherwise local.
	var serverClient app.ServerClient
	serverAddr := os.Getenv("REKJA_SERVER")
	if serverAddr != "" {
		client, err := grpcclient.New(serverAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to connect to sidecar at %s: %v (using local mode)\n", serverAddr, err)
		} else {
			serverClient = client
		}
	}

	// Local fallback for server operations when no sidecar is available.
	if serverClient == nil {
		serverClient = &localServerClient{
			config:       cfg,
			scanner:      scanner,
			store:        store,
			thunderstore: thunderstoreClient,
			sync:         syncEngine,
		}
	}

	service := app.NewService(app.Dependencies{
		Config:       cfg,
		Scanner:      scanner,
		Store:        store,
		Thunderstore: thunderstoreClient,
		ServerClient: serverClient,
	})

	model := tui.NewModel(context.Background(), service)
	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "rekja failed: %v\n", err)
		os.Exit(1)
	}
}

// localServerClient implements app.ServerClient using local components
// when no gRPC sidecar is available.
type localServerClient struct {
	config       config.Config
	scanner      *inventory.Scanner
	store        *inventory.Store
	thunderstore *thunderstore.Client
	sync         *sync.Engine
}

func (l *localServerClient) LoadState(ctx context.Context) (app.State, error) {
	snapshot, err := l.scanner.Scan()
	if err != nil {
		return app.State{}, err
	}

	packages, err := l.thunderstore.FetchIndex(ctx)
	if err != nil {
		return app.State{}, err
	}
	index := thunderstore.IndexByFullName(packages)

	var updates []app.Update
	for _, tracked := range snapshot.Manifest.Tracked {
		pkg, ok := index[tracked.Key()]
		if !ok {
			continue
		}
		latest, ok, err := thunderstore.FindVersion(pkg, tracked.DesiredVersion)
		if err != nil {
			return app.State{}, err
		}
		if !ok || tracked.InstalledVersion == "" {
			continue
		}
		compare, err := thunderstore.CompareVersions(latest.VersionNumber, tracked.InstalledVersion)
		if err != nil {
			return app.State{}, err
		}
		if compare > 0 {
			updates = append(updates, app.Update{
				Package:          tracked,
				InstalledVersion: tracked.InstalledVersion,
				LatestVersion:    latest.VersionNumber,
			})
		}
	}

	return app.State{
		Inventory: snapshot,
		Packages:  packages,
		Updates:   updates,
	}, nil
}

func (l *localServerClient) BuildSyncPlan(state app.State) (resolve.Plan, error) {
	tracked := state.Inventory.Manifest.Tracked
	if len(tracked) == 0 {
		tracked = l.config.Tracked
	}
	if len(tracked) == 0 {
		return resolve.Plan{}, fmt.Errorf("no tracked mods configured")
	}
	return resolve.Resolve(thunderstore.IndexByFullName(state.Packages), tracked)
}

func (l *localServerClient) StartSync(ctx context.Context, events chan<- sync.Event) error {
	return l.sync.Apply(ctx, resolve.Plan{}, events)
}

func (l *localServerClient) Restart(ctx context.Context) (string, error) {
	return "", fmt.Errorf("restart requires a server sidecar")
}

func (l *localServerClient) Logs(ctx context.Context, lines int64) (string, error) {
	return "", fmt.Errorf("logs require a server sidecar")
}
