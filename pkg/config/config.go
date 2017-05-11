package config

import (
	"fmt"
	"io/ioutil"

	"gopkg.in/yaml.v2"
)

var defaultProtectedNamespaces = []string{"default", "openshift-infra"}

type NamespaceCapacity struct {

	// HighWatermark is the number of clusters that will trigger more aggressive archival.
	HighWatermark int `yaml:"highWatermark"`

	// LowWatermark is the number of clusters we will attempt to get to when the HighWatermark
	// has been reached.
	LowWatermark int `yaml:"lowWatermark"`
}

// ClusterConfig represents the settings for a specific cluster this instance of the archivist
// will manage capacity for.
type ClusterConfig struct {
	// Name is a user specified name to identify a particular cluster being managed.
	Name              string            `yaml:"name"`
	NamespaceCapacity NamespaceCapacity `yaml:"namespaceCapacity"`
	// You *may* be archived if inactive beyond than this number of days, if we need to reclaim space:
	MinInactiveDays int `yaml:"minInactiveDays"`
	// You *will* be archived if inactive beyond this number of days:
	MaxInactiveDays int `yaml:"maxInactiveDays"`
	// Namespaces which can *never* be archived:
	ProtectedNamespaces []string `yaml:"protectedNamespaces"`
}

type ArchivistConfig struct {
	LogLevel string          `yaml:"logLevel"`
	Clusters []ClusterConfig `yaml:"clusters"`
}

func NewArchivistConfigFromString(yamlConfig string) (ArchivistConfig, error) {
	cfg, err := newArchivist([]byte(yamlConfig))
	return cfg, err
}

func NewArchivistConfigFromFile(filepath string) (ArchivistConfig, error) {
	if data, readErr := ioutil.ReadFile(filepath); readErr != nil {
		return ArchivistConfig{}, readErr
	} else {
		cfg, err := newArchivist(data)
		return cfg, err
	}
}

func NewDefaultArchivistConfig() ArchivistConfig {
	cfg := ArchivistConfig{
		Clusters: []ClusterConfig{
			{
				Name: "local cluster",
			},
		},
	}
	ApplyConfigDefaults(&cfg)
	return cfg
}

func newArchivist(data []byte) (ArchivistConfig, error) {
	cfg := ArchivistConfig{}
	err := yaml.Unmarshal(data, &cfg)
	if err != nil {
		return cfg, err
	}
	ApplyConfigDefaults(&cfg)
	fmt.Println("defaulting 3")
	if len(cfg.Clusters) > 0 {
		fmt.Println(cfg.Clusters[0].ProtectedNamespaces)
	}
	err = ValidateConfig(&cfg)
	return cfg, err
}

func ApplyConfigDefaults(cfg *ArchivistConfig) {
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	for i := range cfg.Clusters {
		if len(cfg.Clusters[i].ProtectedNamespaces) == 0 {
			// TODO: is this re-use of a package var array safe?
			cfg.Clusters[i].ProtectedNamespaces = make([]string, len(defaultProtectedNamespaces))
			copy(cfg.Clusters[i].ProtectedNamespaces, defaultProtectedNamespaces)
			fmt.Println("defaulting 1")
			fmt.Println(cfg.Clusters[i].ProtectedNamespaces)
		}
	}
	fmt.Println("defaulting 2")
	if len(cfg.Clusters) > 0 {
		fmt.Println(cfg.Clusters[0].ProtectedNamespaces)
	}
}

func ValidateConfig(cfg *ArchivistConfig) error {
	if len(cfg.Clusters) == 0 {
		return fmt.Errorf("no clusters in config")
	}
	for _, cc := range cfg.Clusters {
		if cc.Name == "" {
			return fmt.Errorf("cluster must have a name")
		}
		if cc.MaxInactiveDays < cc.MinInactiveDays {
			return fmt.Errorf("maxInactiveDays must be greater than minInactiveDays")
		}
	}
	if cfg.LogLevel == "" {
		return fmt.Errorf("invalid log level: %s", cfg.LogLevel)
	}
	return nil
}
