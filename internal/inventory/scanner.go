package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cbartram/rekja/internal/manifest"
)

// Scanner lists plugin files and reconciles them with the Rekja manifest.
type Scanner struct {
	pluginsDir string
	store      *Store
}

// Snapshot is the current local plugin state.
type Snapshot struct {
	Manifest       manifest.Manifest
	TrackedDrift   []manifest.FileRecord
	UntrackedFiles []manifest.FileRecord
}

// NewScanner creates a scanner for the plugin directory.
func NewScanner(pluginsDir string, store *Store) *Scanner {
	return &Scanner{pluginsDir: pluginsDir, store: store}
}

// Scan loads the manifest, hashes files on disk, and classifies drift and
// untracked files.
func (s *Scanner) Scan() (Snapshot, error) {
	current, err := s.store.Load(s.pluginsDir)
	if err != nil {
		return Snapshot{}, err
	}

	managed := make(map[string]manifest.FileRecord)
	for _, tracked := range current.Tracked {
		for _, file := range tracked.Files {
			managed[filepath.ToSlash(file.Path)] = file
		}
	}

	var drift []manifest.FileRecord
	var untracked []manifest.FileRecord
	err = filepath.WalkDir(s.pluginsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".rekja" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(s.pluginsDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		record := manifest.FileRecord{
			Path:   rel,
			SHA256: hash,
			Kind:   classifyFile(rel),
		}
		if expected, ok := managed[rel]; ok {
			if expected.SHA256 != "" && expected.SHA256 != hash {
				record.PreserveOnUpdate = expected.PreserveOnUpdate
				drift = append(drift, record)
			}
			return nil
		}
		untracked = append(untracked, record)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{Manifest: current}, nil
		}
		return Snapshot{}, err
	}

	current.Untracked = untracked
	return Snapshot{
		Manifest:       current,
		TrackedDrift:   drift,
		UntrackedFiles: untracked,
	}, nil
}

func hashFile(path string) (string, error) {
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

func classifyFile(path string) string {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".dll":
		return "plugin"
	case ".cfg", ".json", ".yml", ".yaml", ".toml":
		return "config"
	default:
		return "asset"
	}
}
