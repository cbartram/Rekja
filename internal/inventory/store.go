package inventory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/cbartram/rekja/internal/manifest"
)

// Store persists Rekja's installed manifest.
type Store struct {
	path string
}

// NewStore creates a manifest store at path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load returns the installed manifest, creating an empty in-memory manifest if
// it does not exist yet.
func (s *Store) Load(pluginsDir string) (manifest.Manifest, error) {
	file, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return manifest.New(pluginsDir), nil
		}
		return manifest.Manifest{}, err
	}
	defer file.Close()

	var result manifest.Manifest
	if err := json.NewDecoder(file).Decode(&result); err != nil {
		return manifest.Manifest{}, err
	}
	if result.SchemaVersion == 0 {
		result.SchemaVersion = manifest.SchemaVersion
	}
	if result.Target.PluginsDir == "" {
		result.Target.PluginsDir = pluginsDir
	}
	return result, nil
}

// Save writes the manifest through a temporary file and rename.
func (s *Store) Save(value manifest.Manifest) error {
	value.SchemaVersion = manifest.SchemaVersion
	value.UpdatedAt = time.Now().UTC()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	temp := s.path + ".tmp"
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, s.path)
}

// Path returns the on-disk manifest path.
func (s *Store) Path() string {
	return s.path
}
