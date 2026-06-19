package sync

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cbartram/rekja/internal/inventory"
	"github.com/cbartram/rekja/internal/manifest"
	"github.com/cbartram/rekja/internal/resolve"
)

// Downloader is the Thunderstore download subset used by Engine.
type Downloader interface {
	OpenDownload(ctx context.Context, downloadURL string) (*http.Response, error)
}

// Engine downloads, stages, and installs package zips into the plugin PVC.
type Engine struct {
	pluginsDir string
	workDir    string
	downloader Downloader
	store      *inventory.Store
}

// Event describes sync progress.
type Event struct {
	Package string
	Message string
	Done    bool
	Err     error
}

// NewEngine creates a sync engine.
func NewEngine(pluginsDir string, workDir string, downloader Downloader, store *inventory.Store) *Engine {
	return &Engine{
		pluginsDir: pluginsDir,
		workDir:    workDir,
		downloader: downloader,
		store:      store,
	}
}

// Apply installs all packages in the resolved plan and updates the manifest.
func (e *Engine) Apply(ctx context.Context, plan resolve.Plan, events chan<- Event) error {
	defer close(events)

	current, err := e.store.Load(e.pluginsDir)
	if err != nil {
		events <- Event{Message: "manifest load failed", Err: err}
		return err
	}
	if err := os.MkdirAll(e.pluginsDir, 0o755); err != nil {
		events <- Event{Message: "plugins directory setup failed", Err: err}
		return err
	}
	if err := os.MkdirAll(e.workDir, 0o755); err != nil {
		events <- Event{Message: "work directory setup failed", Err: err}
		return err
	}

	all := append([]resolve.ResolvedPackage{}, plan.Roots...)
	all = append(all, plan.Dependencies...)
	for _, item := range all {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := e.installOne(ctx, item, &current, events); err != nil {
			events <- Event{Package: item.Version.FullName, Message: "failed", Err: err}
			return err
		}
	}
	if err := e.store.Save(current); err != nil {
		events <- Event{Message: "manifest save failed", Err: err}
		return err
	}
	return nil
}

func (e *Engine) installOne(ctx context.Context, item resolve.ResolvedPackage, current *manifest.Manifest, events chan<- Event) error {
	events <- Event{Package: item.Version.FullName, Message: "downloading"}
	response, err := e.downloader.OpenDownload(ctx, item.Version.DownloadURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	archivePath := filepath.Join(e.workDir, "staging", item.Version.FullName+".zip")
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return err
	}
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(archive, response.Body); err != nil {
		archive.Close()
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}

	events <- Event{Package: item.Version.FullName, Message: "extracting"}
	files, err := e.extractPackage(archivePath, item, *current)
	if err != nil {
		return err
	}

	e.upsertManifest(current, item, files)
	events <- Event{Package: item.Version.FullName, Message: "installed", Done: true}
	return nil
}

func (e *Engine) extractPackage(archivePath string, item resolve.ResolvedPackage, current manifest.Manifest) ([]manifest.FileRecord, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	ownership := fileOwnership(current)
	var records []manifest.FileRecord
	for _, zipped := range reader.File {
		if zipped.FileInfo().IsDir() || skipPackageMetadata(zipped.Name) {
			continue
		}
		cleanName, err := cleanArchivePath(zipped.Name)
		if err != nil {
			return nil, err
		}
		cleanName = normalizeInstallPath(cleanName)
		destination := filepath.Join(e.pluginsDir, cleanName)
		if !strings.HasPrefix(destination, filepath.Clean(e.pluginsDir)+string(os.PathSeparator)) {
			return nil, fmt.Errorf("archive entry escapes plugins directory: %s", zipped.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return nil, err
		}
		owner, known := ownership[filepath.ToSlash(cleanName)]
		if _, err := os.Stat(destination); err == nil {
			if !known {
				return nil, fmt.Errorf("refusing to overwrite untracked file: %s", cleanName)
			}
			if owner.Package != item.Package.FullName {
				return nil, fmt.Errorf("refusing to overwrite %s owned by %s", cleanName, owner.Package)
			}
			if owner.Record.PreserveOnUpdate && isConfig(cleanName) {
				hash, err := hashExisting(destination)
				if err != nil {
					return nil, err
				}
				records = append(records, manifest.FileRecord{
					Path:             filepath.ToSlash(cleanName),
					SHA256:           hash,
					Kind:             classify(cleanName),
					PreserveOnUpdate: true,
				})
				continue
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}

		hash, err := writeZipFile(zipped, destination)
		if err != nil {
			return nil, err
		}
		records = append(records, manifest.FileRecord{
			Path:             filepath.ToSlash(cleanName),
			SHA256:           hash,
			Kind:             classify(cleanName),
			PreserveOnUpdate: isConfig(cleanName),
		})
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("package %s did not contain installable files", item.Version.FullName)
	}
	return records, nil
}

type ownerRecord struct {
	Package string
	Record  manifest.FileRecord
}

func fileOwnership(current manifest.Manifest) map[string]ownerRecord {
	ownership := map[string]ownerRecord{}
	for _, tracked := range current.Tracked {
		for _, file := range tracked.Files {
			ownership[filepath.ToSlash(file.Path)] = ownerRecord{
				Package: tracked.Key(),
				Record:  file,
			}
		}
	}
	return ownership
}

func writeZipFile(zipped *zip.File, destination string) (string, error) {
	source, err := zipped.Open()
	if err != nil {
		return "", err
	}
	defer source.Close()

	temp := destination + ".rekja-tmp"
	target, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, zipped.Mode())
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(target, io.TeeReader(source, hash)); err != nil {
		target.Close()
		return "", err
	}
	if err := target.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temp, destination); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashExisting(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (e *Engine) upsertManifest(current *manifest.Manifest, item resolve.ResolvedPackage, files []manifest.FileRecord) {
	tracked := manifest.TrackedMod{
		Namespace:        item.Package.Owner,
		Name:             item.Package.Name,
		FullName:         item.Package.FullName,
		DesiredVersion:   "latest",
		InstalledVersion: item.Version.VersionNumber,
		InstalledAt:      time.Now().UTC(),
		Source:           "thunderstore",
		DependencyMode:   "auto-approved",
		Files:            files,
	}
	if item.DependencyOf != "" {
		tracked.DependencyMode = "transitive"
		tracked.Dependencies = []manifest.Dependency{{
			Namespace:    item.Package.Owner,
			Name:         item.Package.Name,
			Version:      item.Version.VersionNumber,
			ResolvedFrom: item.DependencyOf,
		}}
	}

	for index, existing := range current.Tracked {
		if existing.Key() == tracked.Key() {
			if existing.DesiredVersion != "" {
				tracked.DesiredVersion = existing.DesiredVersion
			}
			current.Tracked[index] = tracked
			return
		}
	}
	current.Tracked = append(current.Tracked, tracked)
}

func cleanArchivePath(name string) (string, error) {
	name = filepath.ToSlash(name)
	name = strings.TrimPrefix(name, "./")
	cleaned := filepath.Clean(name)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}
	return cleaned, nil
}

func skipPackageMetadata(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	return base == "manifest.json" || base == "readme.md" || base == "icon.png" || base == "changelog.md"
}

func normalizeInstallPath(path string) string {
	path = filepath.ToSlash(path)
	for _, prefix := range []string{
		"BepInEx/plugins/",
		"bepinex/plugins/",
		"plugins/",
		"BepInEx/config/",
		"bepinex/config/",
		"config/",
	} {
		if strings.HasPrefix(path, prefix) {
			rest := strings.TrimPrefix(path, prefix)
			if strings.Contains(strings.ToLower(prefix), "config") {
				return filepath.ToSlash(filepath.Join("config", rest))
			}
			return rest
		}
	}
	return path
}

func classify(path string) string {
	if isConfig(path) {
		return "config"
	}
	if strings.EqualFold(filepath.Ext(path), ".dll") {
		return "plugin"
	}
	return "asset"
}

func isConfig(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cfg", ".json", ".yml", ".yaml", ".toml":
		return true
	default:
		return false
	}
}
