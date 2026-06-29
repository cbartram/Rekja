// Package sidecar implements the gRPC server that manages Valheim mods on the
// dedicated server. It runs as a sidecar container alongside the Valheim pod
// and exposes operations for mod sync, restart, and log retrieval.
package sidecar

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	"github.com/cbartram/rekja/internal/config"
	"github.com/cbartram/rekja/internal/inventory"
	"github.com/cbartram/rekja/internal/kube"
	"github.com/cbartram/rekja/internal/manifest"
	"github.com/cbartram/rekja/internal/resolve"
	syncengine "github.com/cbartram/rekja/internal/sync"
	"github.com/cbartram/rekja/internal/thunderstore"
	v1 "github.com/cbartram/rekja/proto/rekja/v1"
)

// Config holds the configuration for the sidecar server.
type Config struct {
	PluginsDir          string
	ThunderstoreBaseURL string
	Kubernetes          config.KubernetesConfig
}

// server holds the sidecar's internal dependencies.
type server struct {
	v1.UnimplementedRekjaServiceServer
	pluginsDir         string
	store              *inventory.Store
	scanner            *inventory.Scanner
	thunderstoreClient *thunderstore.Client
	syncEngine         *syncengine.Engine
	kubeClient         kube.Client
}

// NewServer creates a new sidecar server with the given config.
func NewServer(cfg Config) (*server, error) {
	thunderstoreClient := thunderstore.NewClient(cfg.ThunderstoreBaseURL, nil)
	store := inventory.NewStore(cfg.PluginsDir + "/.rekja/installed.json")
	scanner := inventory.NewScanner(cfg.PluginsDir, store)
	syncEngine := syncengine.NewEngine(cfg.PluginsDir, cfg.PluginsDir+"/.rekja", thunderstoreClient, store)

	var kubeClient kube.Client = noopKubeClient{cfg: cfg.Kubernetes}
	realClient, err := kube.New(cfg.Kubernetes)
	if err == nil {
		kubeClient = realClient
	}

	return &server{
		pluginsDir:         cfg.PluginsDir,
		store:              store,
		scanner:            scanner,
		thunderstoreClient: thunderstoreClient,
		syncEngine:         syncEngine,
		kubeClient:         kubeClient,
	}, nil
}

// Register registers the server on a gRPC server instance.
func (s *server) Register(grpcServer *grpc.Server) {
	v1.RegisterRekjaServiceServer(grpcServer, s)
}

// ---------------------------------------------------------------------------
// LoadState
// ---------------------------------------------------------------------------

func (s *server) LoadState(_ context.Context, _ *v1.LoadStateRequest) (*v1.StateResponse, error) {
	snapshot, err := s.scanner.Scan()
	if err != nil {
		return nil, err
	}

	packages, err := s.thunderstoreClient.FetchIndex(context.Background())
	if err != nil {
		return nil, err
	}
	index := thunderstore.IndexByFullName(packages)

	var updates []*v1.Update
	for _, tracked := range snapshot.Manifest.Tracked {
		pkg, ok := index[tracked.Key()]
		if !ok {
			continue
		}
		latest, ok, err := thunderstore.FindVersion(pkg, tracked.DesiredVersion)
		if err != nil {
			return nil, err
		}
		if !ok || tracked.InstalledVersion == "" {
			continue
		}
		compare, err := thunderstore.CompareVersions(latest.VersionNumber, tracked.InstalledVersion)
		if err != nil {
			return nil, err
		}
		if compare > 0 {
			updates = append(updates, &v1.Update{
				Package:          trackedToProto(tracked),
				InstalledVersion: tracked.InstalledVersion,
				LatestVersion:    latest.VersionNumber,
			})
		}
	}

	target, _ := s.kubeClient.ResolveTarget(context.Background())

	return &v1.StateResponse{
		Inventory: &v1.Inventory{
			Manifest:        manifestToProto(snapshot.Manifest),
			TrackedDrift:    filesToProto(snapshot.TrackedDrift),
			UntrackedFiles:  filesToProto(snapshot.UntrackedFiles),
		},
		Packages: packagesToProto(packages),
		Updates:  updates,
		Target:   targetToProto(target),
		Warnings: nil,
	}, nil
}

// ---------------------------------------------------------------------------
// StartSync (server-side streaming)
// ---------------------------------------------------------------------------

func (s *server) StartSync(req *v1.StartSyncRequest, stream v1.RekjaService_StartSyncServer) error {
	tracked := trackedModsFromProto(req.Roots)
	if len(tracked) == 0 {
		current, loadErr := s.store.Load(s.pluginsDir)
		if loadErr != nil {
			return stream.Send(&v1.SyncProgress{
				Message: "manifest load failed",
				Error:   loadErr.Error(),
			})
		}
		tracked = current.Tracked
	}

	pkgs, fetchErr := s.thunderstoreClient.FetchIndex(context.Background())
	if fetchErr != nil {
		return stream.Send(&v1.SyncProgress{
			Message: "fetch index failed",
			Error:   fetchErr.Error(),
		})
	}

	plan, err := resolve.Resolve(thunderstore.IndexByFullName(pkgs), tracked)
	if err != nil {
		return stream.Send(&v1.SyncProgress{
			Message: "resolve failed",
			Error:   err.Error(),
		})
	}

	events := make(chan syncengine.Event, 64)
	go func() {
		if err := s.syncEngine.Apply(context.Background(), plan, events); err != nil {
			// Error is already sent through the channel by the engine.
		}
	}()

	for event := range events {
		progress := &v1.SyncProgress{
			Package: event.Package,
			Message: event.Message,
			Done:    event.Done,
		}
		if event.Err != nil {
			progress.Error = event.Err.Error()
		}
		if sendErr := stream.Send(progress); sendErr != nil {
			return sendErr
		}
		if event.Done || event.Err != nil {
			for more := range events {
				p := &v1.SyncProgress{
					Package: more.Package,
					Message: more.Message,
					Done:    more.Done,
				}
				if more.Err != nil {
					p.Error = more.Err.Error()
				}
				if s := stream.Send(p); s != nil {
					return s
				}
			}
			return nil
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Restart
// ---------------------------------------------------------------------------

func (s *server) Restart(_ context.Context, _ *v1.RestartRequest) (*v1.RestartResponse, error) {
	target, err := s.kubeClient.ResolveTarget(context.Background())
	if err != nil {
		return nil, err
	}
	output, err := s.kubeClient.Restart(context.Background(), target)
	if err != nil {
		return nil, err
	}
	return &v1.RestartResponse{Output: output}, nil
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

func (s *server) Logs(_ context.Context, req *v1.LogsRequest) (*v1.LogsResponse, error) {
	target, err := s.kubeClient.ResolveTarget(context.Background())
	if err != nil {
		return nil, err
	}
	lines := int64(200)
	if req.Lines > 0 {
		lines = req.Lines
	}
	content, err := s.kubeClient.Logs(context.Background(), target, lines)
	if err != nil {
		return nil, err
	}
	return &v1.LogsResponse{Content: content}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func manifestToProto(m manifest.Manifest) *v1.Manifest {
	return &v1.Manifest{
		SchemaVersion: int32(m.SchemaVersion),
		Target:        targetFromManifest(m.Target),
		Tracked:       trackedModsToProto(m.Tracked),
		UntrackedFiles: filesToProto(m.Untracked),
		UpdatedAt:     m.UpdatedAt.Format(time.RFC3339),
	}
}

func targetFromManifest(t manifest.Target) *v1.Target {
	return &v1.Target{
		Namespace:         t.PluginsDir,
		Pod:               t.ValheimPlusConfig,
		PluginsDir:        t.PluginsDir,
		ValheimplusConfig: t.ValheimPlusConfig,
	}
}

func targetToProto(t kube.Target) *v1.Target {
	return &v1.Target{
		Namespace: t.Namespace,
		Pod:       t.Pod,
		Container: t.Container,
	}
}

func trackedModsToProto(mods []manifest.TrackedMod) []*v1.TrackedMod {
	result := make([]*v1.TrackedMod, len(mods))
	for i, m := range mods {
		result[i] = trackedModToProto(m)
	}
	return result
}

func trackedModToProto(m manifest.TrackedMod) *v1.TrackedMod {
	return &v1.TrackedMod{
		Namespace:        m.Namespace,
		Name:             m.Name,
		FullName:         m.FullName,
		DesiredVersion:   m.DesiredVersion,
		InstalledVersion: m.InstalledVersion,
		InstalledAt:      m.InstalledAt.Format(time.RFC3339),
		Source:           m.Source,
		DependencyMode:   m.DependencyMode,
		Files:            filesToProto(m.Files),
		Dependencies:     dependenciesToProto(m.Dependencies),
	}
}

func trackedModsFromProto(protos []*v1.TrackedMod) []manifest.TrackedMod {
	result := make([]manifest.TrackedMod, len(protos))
	for i, p := range protos {
		result[i] = trackedModFromProto(p)
	}
	return result
}

func trackedModFromProto(p *v1.TrackedMod) manifest.TrackedMod {
	installedAt, _ := time.Parse(time.RFC3339, p.InstalledAt)
	return manifest.TrackedMod{
		Namespace:        p.Namespace,
		Name:             p.Name,
		FullName:         p.FullName,
		DesiredVersion:   p.DesiredVersion,
		InstalledVersion: p.InstalledVersion,
		InstalledAt:      installedAt,
		Source:           p.Source,
		DependencyMode:   p.DependencyMode,
		Files:            filesFromProto(p.Files),
		Dependencies:     dependenciesFromProto(p.Dependencies),
	}
}

func filesToProto(records []manifest.FileRecord) []*v1.FileRecord {
	result := make([]*v1.FileRecord, len(records))
	for i, r := range records {
		result[i] = &v1.FileRecord{
			Path:             r.Path,
			Sha256:           r.SHA256,
			Kind:             r.Kind,
			PreserveOnUpdate: r.PreserveOnUpdate,
			DetectedVersion:  r.DetectedVersion,
		}
	}
	return result
}

func filesFromProto(protos []*v1.FileRecord) []manifest.FileRecord {
	result := make([]manifest.FileRecord, len(protos))
	for i, p := range protos {
		result[i] = manifest.FileRecord{
			Path:             p.Path,
			SHA256:           p.Sha256,
			Kind:             p.Kind,
			PreserveOnUpdate: p.PreserveOnUpdate,
			DetectedVersion:  p.DetectedVersion,
		}
	}
	return result
}

func dependenciesToProto(deps []manifest.Dependency) []*v1.Dependency {
	result := make([]*v1.Dependency, len(deps))
	for i, d := range deps {
		result[i] = &v1.Dependency{
			Namespace:    d.Namespace,
			Name:         d.Name,
			Version:      d.Version,
			ResolvedFrom: d.ResolvedFrom,
		}
	}
	return result
}

func dependenciesFromProto(protos []*v1.Dependency) []manifest.Dependency {
	result := make([]manifest.Dependency, len(protos))
	for i, p := range protos {
		result[i] = manifest.Dependency{
			Namespace:    p.Namespace,
			Name:         p.Name,
			Version:      p.Version,
			ResolvedFrom: p.ResolvedFrom,
		}
	}
	return result
}

func packagesToProto(pkgs []thunderstore.Package) []*v1.Package {
	result := make([]*v1.Package, len(pkgs))
	for i, p := range pkgs {
		versions := make([]*v1.Version, len(p.Versions))
		for j, v := range p.Versions {
			versions[j] = &v1.Version{
				Name:          v.Name,
				FullName:      v.FullName,
				Description:   v.Description,
				Icon:          v.Icon,
				VersionNumber: v.VersionNumber,
				Dependencies:  v.Dependencies,
				DownloadUrl:   v.DownloadURL,
				Downloads:     int32(v.Downloads),
				DateCreated:   v.DateCreated.Format(time.RFC3339),
				WebsiteUrl:    v.WebsiteURL,
				IsActive:      v.IsActive,
				FileSize:      v.FileSize,
			}
		}
		result[i] = &v1.Package{
			Name:             p.Name,
			FullName:         p.FullName,
			Owner:            p.Owner,
			PackageUrl:       p.PackageURL,
			DateUpdated:      p.DateUpdated.Format(time.RFC3339),
			IsDeprecated:     p.IsDeprecated,
			HasNsfwContent:   p.HasNSFWContent,
			Categories:       p.Categories,
			Versions:         versions,
		}
	}
	return result
}

func trackedToProto(m manifest.TrackedMod) *v1.TrackedMod {
	return &v1.TrackedMod{
		Namespace:        m.Namespace,
		Name:             m.Name,
		FullName:         m.FullName,
		DesiredVersion:   m.DesiredVersion,
		InstalledVersion: m.InstalledVersion,
		InstalledAt:      m.InstalledAt.Format(time.RFC3339),
		Source:           m.Source,
		DependencyMode:   m.DependencyMode,
		Files:            filesToProto(m.Files),
		Dependencies:     dependenciesToProto(m.Dependencies),
	}
}

// noopKubeClient implements kube.Client for when no cluster is reachable.
type noopKubeClient struct {
	cfg config.KubernetesConfig
}

func (n noopKubeClient) ResolveTarget(ctx context.Context) (kube.Target, error) {
	return kube.Target{
		Namespace: n.cfg.Namespace,
		Pod:       n.cfg.PodName,
	}, nil
}

func (n noopKubeClient) Restart(ctx context.Context, target kube.Target) (string, error) {
	return "", fmt.Errorf("no kubernetes connection available")
}

func (n noopKubeClient) Logs(ctx context.Context, target kube.Target, lines int64) (string, error) {
	return "", fmt.Errorf("no kubernetes connection available")
}
