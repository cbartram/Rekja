package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cbartram/rekja/internal/manifest"
)

const (
	defaultPluginsDir          = "/config/valheimplus/plugins"
	defaultThunderstoreBaseURL = "https://valheim.thunderstore.io"
	defaultContainerName       = "valheim-server"
)

// Config is the runtime configuration for a single Rekja TUI session.
type Config struct {
	PluginsDir           string                `json:"plugins_dir"`
	ThunderstoreBaseURL  string                `json:"thunderstore_base_url"`
	PreserveConfigFiles  bool                  `json:"preserve_config_files"`
	Tracked              []manifest.TrackedMod `json:"tracked"`
	Kubernetes           KubernetesConfig      `json:"kubernetes"`
	RestartAfterSync     bool                  `json:"restart_after_sync"`
	RequireRestartPrompt bool                  `json:"require_restart_prompt"`
}

// KubernetesConfig identifies the Valheim Pod and restart target.
type KubernetesConfig struct {
	Namespace      string            `json:"namespace"`
	PodName        string            `json:"pod_name"`
	LabelSelector  string            `json:"label_selector"`
	ContainerName  string            `json:"container_name"`
	KubeconfigPath string            `json:"kubeconfig_path"`
	RestartCommand []string          `json:"restart_command"`
	PodLabels      map[string]string `json:"pod_labels"`
}

// Load reads config from path and applies conservative defaults. An empty path
// means only environment variables and defaults are used.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, err
		}
		defer file.Close()

		if err := json.NewDecoder(file).Decode(&cfg); err != nil {
			return Config{}, err
		}
	}

	applyEnv(&cfg)
	return cfg.withDefaults().Validate()
}

// Default returns the default single-server sidecar configuration.
func Default() Config {
	return Config{
		PluginsDir:           defaultPluginsDir,
		ThunderstoreBaseURL:  defaultThunderstoreBaseURL,
		PreserveConfigFiles:  true,
		RequireRestartPrompt: true,
		Kubernetes: KubernetesConfig{
			ContainerName:  defaultContainerName,
			RestartCommand: []string{"supervisorctl", "restart", "valheim-server"},
		},
	}
}

func applyEnv(cfg *Config) {
	if value := os.Getenv("REKJA_PLUGINS_DIR"); value != "" {
		cfg.PluginsDir = value
	}
	if value := os.Getenv("REKJA_THUNDERSTORE_BASE_URL"); value != "" {
		cfg.ThunderstoreBaseURL = value
	}
	if value := os.Getenv("REKJA_NAMESPACE"); value != "" {
		cfg.Kubernetes.Namespace = value
	}
	if value := os.Getenv("REKJA_POD_NAME"); value != "" {
		cfg.Kubernetes.PodName = value
	}
	if value := os.Getenv("REKJA_LABEL_SELECTOR"); value != "" {
		cfg.Kubernetes.LabelSelector = value
	}
	if value := os.Getenv("REKJA_CONTAINER"); value != "" {
		cfg.Kubernetes.ContainerName = value
	}
	if value := os.Getenv("KUBECONFIG"); value != "" && cfg.Kubernetes.KubeconfigPath == "" {
		cfg.Kubernetes.KubeconfigPath = value
	}
}

func (c Config) withDefaults() Config {
	if c.PluginsDir == "" {
		c.PluginsDir = defaultPluginsDir
	}
	if c.ThunderstoreBaseURL == "" {
		c.ThunderstoreBaseURL = defaultThunderstoreBaseURL
	}
	if c.Kubernetes.ContainerName == "" {
		c.Kubernetes.ContainerName = defaultContainerName
	}
	if len(c.Kubernetes.RestartCommand) == 0 {
		c.Kubernetes.RestartCommand = []string{"supervisorctl", "restart", "valheim-server"}
	}
	return c
}

// Validate checks only configuration invariants that would make Rekja unsafe or
// impossible to run.
func (c Config) Validate() (Config, error) {
	if c.PluginsDir == "" {
		return Config{}, errors.New("plugins_dir is required")
	}
	if !filepath.IsAbs(c.PluginsDir) {
		return Config{}, fmt.Errorf("plugins_dir must be absolute: %s", c.PluginsDir)
	}
	if c.ThunderstoreBaseURL == "" {
		return Config{}, errors.New("thunderstore_base_url is required")
	}
	return c, nil
}

// ManifestPath returns the Rekja-owned installed-state file on the PVC.
func (c Config) ManifestPath() string {
	return filepath.Join(c.WorkDir(), "installed.json")
}

// WorkDir returns the Rekja-owned working directory on the PVC.
func (c Config) WorkDir() string {
	return filepath.Join(filepath.Dir(c.PluginsDir), ".rekja")
}
