package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/cbartram/rekja/internal/config"
	"github.com/cbartram/rekja/internal/inventory"
	"github.com/cbartram/rekja/internal/kube"
	"github.com/cbartram/rekja/internal/manifest"
	"github.com/cbartram/rekja/internal/resolve"
	"github.com/cbartram/rekja/internal/sync"
	"github.com/cbartram/rekja/internal/thunderstore"
)

// Dependencies groups app service collaborators.
type Dependencies struct {
	Config       config.Config
	Scanner      *inventory.Scanner
	Store        *inventory.Store
	Thunderstore *thunderstore.Client
	Sync         *sync.Engine
	Kubernetes   kube.Client
}

// Service coordinates inventory, Thunderstore, sync, and Kubernetes actions.
type Service struct {
	deps Dependencies
}

// State is the aggregate state rendered by the TUI.
type State struct {
	Inventory inventory.Snapshot
	Packages  []thunderstore.Package
	Updates   []Update
	Warnings  []string
	Target    kube.Target
}

// Update is an available package update.
type Update struct {
	Package          manifest.TrackedMod
	InstalledVersion string
	LatestVersion    string
}

// NewService creates an app service.
func NewService(deps Dependencies) *Service {
	return &Service{deps: deps}
}

// LoadState scans local state, fetches Thunderstore, computes updates, and
// resolves the Kubernetes target if available.
func (s *Service) LoadState(ctx context.Context) (State, error) {
	snapshot, err := s.deps.Scanner.Scan()
	if err != nil {
		return State{}, err
	}

	packages, err := s.deps.Thunderstore.FetchIndex(ctx)
	if err != nil {
		return State{}, err
	}
	index := thunderstore.IndexByFullName(packages)

	var updates []Update
	for _, tracked := range snapshot.Manifest.Tracked {
		pkg, ok := index[tracked.Key()]
		if !ok {
			continue
		}
		latest, ok, err := thunderstore.FindVersion(pkg, tracked.DesiredVersion)
		if err != nil {
			return State{}, err
		}
		if !ok || tracked.InstalledVersion == "" {
			continue
		}
		compare, err := thunderstore.CompareVersions(latest.VersionNumber, tracked.InstalledVersion)
		if err != nil {
			return State{}, err
		}
		if compare > 0 {
			updates = append(updates, Update{
				Package:          tracked,
				InstalledVersion: tracked.InstalledVersion,
				LatestVersion:    latest.VersionNumber,
			})
		}
	}

	target, _ := s.deps.Kubernetes.ResolveTarget(ctx)
	return State{
		Inventory: snapshot,
		Packages:  packages,
		Updates:   updates,
		Target:    target,
	}, nil
}

// BuildSyncPlan resolves tracked packages and dependencies against the supplied
// Thunderstore index.
func (s *Service) BuildSyncPlan(state State) (resolve.Plan, error) {
	tracked := state.Inventory.Manifest.Tracked
	if len(tracked) == 0 {
		tracked = s.deps.Config.Tracked
	}
	if len(tracked) == 0 {
		return resolve.Plan{}, fmt.Errorf("no tracked mods configured")
	}
	return resolve.Resolve(thunderstore.IndexByFullName(state.Packages), tracked)
}

// ApplySync applies a resolved plan.
func (s *Service) ApplySync(ctx context.Context, plan resolve.Plan, events chan<- sync.Event) error {
	return s.deps.Sync.Apply(ctx, plan, events)
}

// RestartServer restarts the Valheim server process via Kubernetes exec.
func (s *Service) RestartServer(ctx context.Context, target kube.Target) (string, error) {
	if target.Pod == "" {
		resolved, err := s.deps.Kubernetes.ResolveTarget(ctx)
		if err != nil {
			return "", err
		}
		target = resolved
	}
	return s.deps.Kubernetes.Restart(ctx, target)
}

// Logs reads recent server logs.
func (s *Service) Logs(ctx context.Context, target kube.Target, lines int64) (string, error) {
	if target.Pod == "" {
		resolved, err := s.deps.Kubernetes.ResolveTarget(ctx)
		if err != nil {
			return "", err
		}
		target = resolved
	}
	return s.deps.Kubernetes.Logs(ctx, target, lines)
}

// TrackMod adds or updates a tracked Thunderstore package in the Rekja
// manifest. The spec format is Namespace-Name or Namespace-Name@Version.
func (s *Service) TrackMod(spec string) error {
	tracked, err := parseTrackedSpec(spec)
	if err != nil {
		return err
	}
	current, err := s.deps.Store.Load(s.deps.Config.PluginsDir)
	if err != nil {
		return err
	}
	for index, existing := range current.Tracked {
		if existing.Key() == tracked.Key() {
			tracked.InstalledVersion = existing.InstalledVersion
			tracked.InstalledAt = existing.InstalledAt
			tracked.Source = "thunderstore"
			tracked.DependencyMode = existing.DependencyMode
			tracked.Files = existing.Files
			tracked.Dependencies = existing.Dependencies
			current.Tracked[index] = tracked
			return s.deps.Store.Save(current)
		}
	}
	current.Tracked = append(current.Tracked, tracked)
	return s.deps.Store.Save(current)
}

// UntrackMod removes a package from the tracked list without deleting files.
func (s *Service) UntrackMod(fullName string) error {
	current, err := s.deps.Store.Load(s.deps.Config.PluginsDir)
	if err != nil {
		return err
	}
	filtered := current.Tracked[:0]
	for _, tracked := range current.Tracked {
		if tracked.Key() != fullName {
			filtered = append(filtered, tracked)
		}
	}
	current.Tracked = filtered
	return s.deps.Store.Save(current)
}

// Config returns the runtime config.
func (s *Service) Config() config.Config {
	return s.deps.Config
}

// parseTrackedSpec parses a mod spec in the format Namespace-Name or Namespace-Name@Version into a TrackedMod struct.
func parseTrackedSpec(spec string) (manifest.TrackedMod, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return manifest.TrackedMod{}, fmt.Errorf("tracked mod spec is required")
	}
	desired := "latest"
	if before, after, ok := strings.Cut(spec, "@"); ok {
		spec = strings.TrimSpace(before)
		desired = strings.TrimSpace(after)
		if desired == "" {
			return manifest.TrackedMod{}, fmt.Errorf("desired version cannot be empty")
		}
	}
	namespace, name, ok := strings.Cut(spec, "/")
	if !ok || namespace == "" || name == "" {
		return manifest.TrackedMod{}, fmt.Errorf("tracked mod must be Namespace/Name or Namespace/Name@Version")
	}
	return manifest.TrackedMod{
		Namespace:      namespace,
		Name:           name,
		FullName:       namespace + "-" + name,
		DesiredVersion: desired,
		Source:         "thunderstore",
		DependencyMode: "auto-approved",
	}, nil
}
