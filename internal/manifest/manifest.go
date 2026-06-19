package manifest

import "time"

const SchemaVersion = 1

// Manifest is Rekja's source of truth for Thunderstore package ownership of
// files written under the ValheimPlus plugin directory.
type Manifest struct {
	SchemaVersion int          `json:"schema_version"`
	Target        Target       `json:"target"`
	Tracked       []TrackedMod `json:"tracked"`
	Untracked     []FileRecord `json:"untracked_files,omitempty"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// Target records the ValheimPlus paths this manifest describes.
type Target struct {
	PluginsDir        string `json:"plugins_dir"`
	ValheimPlusConfig string `json:"valheimplus_config"`
}

// TrackedMod describes one Thunderstore package intentionally managed by Rekja.
type TrackedMod struct {
	Namespace        string       `json:"namespace"`
	Name             string       `json:"name"`
	FullName         string       `json:"full_name"`
	DesiredVersion   string       `json:"desired_version"`
	InstalledVersion string       `json:"installed_version"`
	InstalledAt      time.Time    `json:"installed_at"`
	Source           string       `json:"source"`
	DependencyMode   string       `json:"dependency_mode"`
	Files            []FileRecord `json:"files"`
	Dependencies     []Dependency `json:"dependencies,omitempty"`
}

// FileRecord maps a managed or detected file back to a package install.
type FileRecord struct {
	Path             string `json:"path"`
	SHA256           string `json:"sha256"`
	Kind             string `json:"kind"`
	PreserveOnUpdate bool   `json:"preserve_on_update,omitempty"`
	DetectedVersion  string `json:"detected_version,omitempty"`
}

// Dependency captures the resolved dependency that caused a package to be
// installed or retained.
type Dependency struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	ResolvedFrom string `json:"resolved_from"`
}

// Key returns the stable Thunderstore package key.
func (m TrackedMod) Key() string {
	if m.FullName != "" {
		return m.FullName
	}
	if m.Namespace == "" || m.Name == "" {
		return ""
	}
	return m.Namespace + "-" + m.Name
}

// New creates an empty manifest for the supplied plugin directory.
func New(pluginsDir string) Manifest {
	return Manifest{
		SchemaVersion: SchemaVersion,
		Target: Target{
			PluginsDir:        pluginsDir,
			ValheimPlusConfig: "/config/valheimplus/valheim_plus.cfg",
		},
		UpdatedAt: time.Now().UTC(),
	}
}
