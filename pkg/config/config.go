package config

import (
	"errors"
	"fmt"
	"io/ioutil"
	"strconv"
	"time"

	"gopkg.in/yaml.v2"
)

var defaultProtectedNamespaces = []string{"default", "openshift-infra"}

type MyDuration time.Duration

// NamespaceCapacity defines the high and low watermarks for number of namespaces in the cluster.
type NamespaceCapacity struct {
	// HighWatermark is the number of namespaces that will trigger more aggressive archival.
	HighWatermark int `yaml:"highWatermark"`

	// LowWatermark is the number of namespaces we will attempt to get to when the HighWatermark
	// has been reached.
	LowWatermark int `yaml:"lowWatermark"`
}

// ClusterConfig represents the settings for a specific cluster this instance of the archivist
// will manage capacity for.
type ClusterConfig struct {
	// Name is a user specified name to identify a particular cluster being managed.
	Name string `yaml:"name"`

	// NamespaceCapacity represents the high and low watermarks for number of namespaces in the cluster.
	NamespaceCapacity NamespaceCapacity `yaml:"namespaceCapacity"`

	// MinInactiveDuration defines the limit of inactivity after which you *may* be archived, if we need to reclaim space. You will never be archived if active within this amount of time.
	MinInactiveDuration MyDuration `yaml:"minInactiveDuration"`

	// MaxInactiveDuration defines the limit of inactivity after which you *will* be archived.
	MaxInactiveDuration MyDuration `yaml:"maxInactiveDuration"`

	// ProtectedNamespaces which can *never* be archived.
	ProtectedNamespaces []string `yaml:"protectedNamespaces"`
}

// ArchivistConfig is the top level configuration object for all components used in archival.
type ArchivistConfig struct {
	// LogLevel is the desired log level for the pods. (debug, info, warn, error, fatal, panic)
	LogLevel string `yaml:"logLevel"`

	// Clusters contains specific configuration for each cluster the Archivist is managing.
	Clusters []ClusterConfig `yaml:"clusters"`

	// Dry-run flag to log but not actually archive projects
	DryRun bool `yaml:"dryRun"`

	// MonitorCheckInterval is the interval at which the archivist will run
	MonitorCheckInterval MyDuration `yaml:"monitorCheckInterval"`
}

// NewArchivistConfigFromString creates a new configuration from a yaml string. (Mainly used in testing.)
func NewArchivistConfigFromString(yamlConfig string) (ArchivistConfig, error) {
	return newArchivist([]byte(yamlConfig))
}

// NewArchivistConfigFromFile creates a new configuration from a yaml file at the given path.
func NewArchivistConfigFromFile(filepath string) (ArchivistConfig, error) {
	if data, readErr := ioutil.ReadFile(filepath); readErr != nil {
		return ArchivistConfig{}, readErr
	} else {
		cfg, err := newArchivist(data)
		return cfg, err
	}
}

// NewDefaultArchivistConfig creates a new config with default settings.
func NewDefaultArchivistConfig() ArchivistConfig {
	cfg := ArchivistConfig{
		Clusters: []ClusterConfig{
			{
				Name: "local cluster",
			},
		},
	}
	cfg.Complete()
	cfg.Validate()
	return cfg
}

// newArchivist initiates the unmarshal of the YAML input, completes any needed default values, and validates the input values
func newArchivist(data []byte) (ArchivistConfig, error) {
	cfg := ArchivistConfig{}
	err := yaml.Unmarshal(data, &cfg)
	if err != nil {
		return cfg, err
	}

	cfg.Complete()
	err = cfg.Validate()

	return cfg, err
}

// Complete applies non-zero config defaults for settings that are not defined.
func (cfg *ArchivistConfig) Complete() {
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	// If no protected clusters are defined we need to make sure we set some defaults
	// for a typical OpenShift cluster.
	for i := range cfg.Clusters {
		// if there are no protected namespaces
		if len(cfg.Clusters[i].ProtectedNamespaces) == 0 {
			cfg.Clusters[i].ProtectedNamespaces = make([]string, len(defaultProtectedNamespaces))
			copy(cfg.Clusters[i].ProtectedNamespaces, defaultProtectedNamespaces)
		}
		if cfg.Clusters[i].MaxInactiveDuration == 0 {
			value, _ := time.ParseDuration("1440h")
			cfg.Clusters[i].MaxInactiveDuration = MyDuration(value)
		}
		if cfg.Clusters[i].MinInactiveDuration == 0 {
			value, _ := time.ParseDuration("720h")
			cfg.Clusters[i].MinInactiveDuration = MyDuration(value)
		}
	}
	if cfg.MonitorCheckInterval == 0 {
		value, _ := time.ParseDuration("24h")
		cfg.MonitorCheckInterval = MyDuration(value)
	}
}

// Validate ensures this configuration is valid and returns an error if not.
func (cfg *ArchivistConfig) Validate() error {
	if len(cfg.Clusters) == 0 {
		return fmt.Errorf("no clusters in config")
	}
	for _, cc := range cfg.Clusters {
		if cc.Name == "" {
			return errors.New("cluster must have a name")
		}
		if cc.MaxInactiveDuration < cc.MinInactiveDuration {
			return fmt.Errorf("MaxInactiveTime (%d) must be greater than or equal to MinInactiveTime (%d)",
				cc.MaxInactiveDuration, cc.MinInactiveDuration)
		}
	}
	return nil
}

// UnmarshalYAML implements the yaml.Unmarshaler interface and assists in correctly parsing and casting YAML strings to type time.Duration
func (d *MyDuration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var value string
	err := unmarshal(&value)
	if err != nil {
		return err
	}

	var tempDuration time.Duration
	if value[len(value)-1:] == "d" {
		tempDuration, err = ParseDays(value)
		if err != nil {
			return err
		}

	} else {
		tempDuration, err = time.ParseDuration(value)
		if err != nil {
			return err
		}
	}
	duration := MyDuration(tempDuration)
	*d = duration
	return nil
}

// ParseDays converts a string from days to hours and parses with time.ParseDuration
func ParseDays(durationInDays string) (time.Duration, error) {

	// re := regexp.MustCompile("[0-9]+")
	// tempDays := re.FindString(durationInDays)
	// days, err := strconv.Atoi(tempDays)
	tempDays := durationInDays[:len(durationInDays)-1]
	days, err := strconv.Atoi(tempDays)
	if err != nil {
		return 0, err
	}

	tempHours := days * 24
	input, err := time.ParseDuration(fmt.Sprintf("%dh", tempHours))
	if err != nil {
		return 0, err
	}
	return input, nil
}
