// Package grpcclient provides a gRPC client that communicates with the
// Rekja sidecar server over gRPC.
package grpcclient

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cbartram/rekja/internal/app"
	"github.com/cbartram/rekja/internal/inventory"
	"github.com/cbartram/rekja/internal/kube"
	"github.com/cbartram/rekja/internal/manifest"
	"github.com/cbartram/rekja/internal/resolve"
	"github.com/cbartram/rekja/internal/sync"
	"github.com/cbartram/rekja/internal/thunderstore"
	v1 "github.com/cbartram/rekja/proto/rekja/v1"
)

// Client connects to the Rekja sidecar via gRPC.
type Client struct {
	client v1.RekjaServiceClient
}

// New creates a gRPC client that connects to addr.
func New(addr string, opts ...grpc.DialOption) (*Client, error) {
	if len(opts) == 0 {
		opts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{client: v1.NewRekjaServiceClient(conn)}, nil
}

// NewWithConn creates a gRPC client from an existing connection.
func NewWithConn(conn *grpc.ClientConn) *Client {
	return &Client{client: v1.NewRekjaServiceClient(conn)}
}

// ---------------------------------------------------------------------------
// LoadState
// ---------------------------------------------------------------------------

func (c *Client) LoadState(ctx context.Context) (app.State, error) {
	resp, err := c.client.LoadState(ctx, &v1.LoadStateRequest{})
	if err != nil {
		return app.State{}, err
	}

	// Convert proto packages to thunderstore.Package for BuildSyncPlan.
	packages := make([]thunderstore.Package, len(resp.Packages))
	for i, p := range resp.Packages {
		packages[i] = protoToPackage(p)
	}

	// Convert proto updates to app.Update.
	var updates []app.Update
	for _, u := range resp.Updates {
		updates = append(updates, app.Update{
			Package:          protoToTrackedMod(u.Package),
			InstalledVersion: u.InstalledVersion,
			LatestVersion:    u.LatestVersion,
		})
	}

	return app.State{
		Inventory: inventory.Snapshot{
			Manifest:       protoToManifest(resp.Inventory.Manifest),
			TrackedDrift:   protoToFiles(resp.Inventory.TrackedDrift),
			UntrackedFiles: protoToFiles(resp.Inventory.UntrackedFiles),
		},
		Packages: packages,
		Updates:  updates,
		Target: kube.Target{
			Namespace: resp.Target.Namespace,
			Pod:       resp.Target.Pod,
			Container: resp.Target.Container,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// BuildSyncPlan
// ---------------------------------------------------------------------------

func (c *Client) BuildSyncPlan(state app.State) (resolve.Plan, error) {
	tracked := state.Inventory.Manifest.Tracked
	if len(tracked) == 0 {
		return resolve.Plan{}, nil
	}
	return resolve.Resolve(thunderstore.IndexByFullName(state.Packages), tracked)
}

// ---------------------------------------------------------------------------
// StartSync
// ---------------------------------------------------------------------------

func (c *Client) StartSync(ctx context.Context, events chan<- sync.Event) error {
	stream, err := c.client.StartSync(ctx, &v1.StartSyncRequest{})
	if err != nil {
		return err
	}

	// Consume the stream in a goroutine and forward events.
	go func() {
		defer close(events)
		for {
			progress, err := stream.Recv()
			if err != nil {
				return
			}

			event := sync.Event{
				Package: progress.Package,
				Message: progress.Message,
				Done:    progress.Done,
			}
			if progress.Error != "" {
				event.Err = syncErr(progress.Error)
			}
			events <- event

			if progress.Done || progress.Error != "" {
				// Server closes stream after completion.
			}
		}
	}()

	return nil
}

// ---------------------------------------------------------------------------
// Restart
// ---------------------------------------------------------------------------

func (c *Client) Restart(ctx context.Context) (string, error) {
	resp, err := c.client.Restart(ctx, &v1.RestartRequest{})
	if err != nil {
		return "", err
	}
	return resp.Output, nil
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

func (c *Client) Logs(ctx context.Context, lines int64) (string, error) {
	resp, err := c.client.Logs(ctx, &v1.LogsRequest{Lines: lines})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// ---------------------------------------------------------------------------
// Proto converters
// ---------------------------------------------------------------------------

func protoToManifest(p *v1.Manifest) manifest.Manifest {
	m := manifest.Manifest{
		SchemaVersion: int(p.SchemaVersion),
		UpdatedAt:     parseTime(p.UpdatedAt),
		Target: manifest.Target{
			PluginsDir:        p.Target.PluginsDir,
			ValheimPlusConfig: p.Target.ValheimplusConfig,
		},
	}
	if p.Tracked != nil {
		m.Tracked = make([]manifest.TrackedMod, len(p.Tracked))
		for i, t := range p.Tracked {
			m.Tracked[i] = protoToTrackedMod(t)
		}
	}
	if p.UntrackedFiles != nil {
		m.Untracked = protoToFiles(p.UntrackedFiles)
	}
	return m
}

func protoToTrackedMod(p *v1.TrackedMod) manifest.TrackedMod {
	return manifest.TrackedMod{
		Namespace:        p.Namespace,
		Name:             p.Name,
		FullName:         p.FullName,
		DesiredVersion:   p.DesiredVersion,
		InstalledVersion: p.InstalledVersion,
		InstalledAt:      parseTime(p.InstalledAt),
		Source:           p.Source,
		DependencyMode:   p.DependencyMode,
		Files:            protoToFiles(p.Files),
		Dependencies:     protoToDeps(p.Dependencies),
	}
}

func protoToFiles(protos []*v1.FileRecord) []manifest.FileRecord {
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

func protoToDeps(protos []*v1.Dependency) []manifest.Dependency {
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

func protoToPackage(p *v1.Package) thunderstore.Package {
	versions := make([]thunderstore.Version, len(p.Versions))
	for i, v := range p.Versions {
		versions[i] = thunderstore.Version{
			Name:          v.Name,
			FullName:      v.FullName,
			Description:   v.Description,
			Icon:          v.Icon,
			VersionNumber: v.VersionNumber,
			Dependencies:  v.Dependencies,
			DownloadURL:   v.DownloadUrl,
			Downloads:     int(v.Downloads),
			DateCreated:   parseTime(v.DateCreated),
			WebsiteURL:    v.WebsiteUrl,
			IsActive:      v.IsActive,
			FileSize:      v.FileSize,
		}
	}
	return thunderstore.Package{
		Name:           p.Name,
		FullName:       p.FullName,
		Owner:          p.Owner,
		PackageURL:     p.PackageUrl,
		DateUpdated:    parseTime(p.DateUpdated),
		IsDeprecated:   p.IsDeprecated,
		HasNSFWContent: p.HasNsfwContent,
		Categories:     p.Categories,
		Versions:       versions,
	}
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

type syncErr string

func (e syncErr) Error() string { return string(e) }
