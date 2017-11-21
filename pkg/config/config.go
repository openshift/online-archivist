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

// CustomDuration is a time.Duration with added support for parsing days. (i.e. 30d)
type CustomDuration time.Duration

const (
	defaultMinInactiveDuration = "30d"
	defaultMaxInactiveDuration = "90d"
	defaultArchiveTTL          = "60d"
)

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
	MinInactiveDuration CustomDuration `yaml:"minInactiveDuration"`

	// MaxInactiveDuration defines the limit of inactivity after which you *will* be archived.
	MaxInactiveDuration CustomDuration `yaml:"maxInactiveDuration"`

	// ProtectedNamespaces which can *never* be archived.
	ProtectedNamespaces []string `yaml:"protectedNamespaces"`
}

// ArchivistConfig is the top level configuration object for all components used in archival.
type ArchivistConfig struct {
	// LogLevel is the desired log level for the pods. (debug, info, warn, error, fatal, panic)
	LogLevel string `yaml:"logLevel"`

	// Clusters contains specific configuration for each cluster the Archivist is managing.
	Clusters []ClusterConfig `yaml:"clusters"`

	// Dry-run flag to log but not actually archive namespaces
	DryRun bool `yaml:"dryRun"`

	// DeletedArchivedNamespaces can be enabled to cleanup after successful
	// archival. Configurable and disabled by default as this is not currently
	// planned to be used in production, another application will be
	// responsible for deleting the projects. Functionality may also one day
	// move to an Ark hook.
	DeleteArchivedNamespaces bool `yaml:"deleteArchivedNamespaces"`

	// ArchiveTTL configures the length of time the archive will be preserved
	// before it is cleaned up.
	ArchiveTTL CustomDuration `yaml:"archiveTTL"`

	// MonitorCheckInterval is the interval at which the archivist will run
	MonitorCheckInterval CustomDuration `yaml:"monitorCheckInterval"`
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
			value, _ := ParseDurationWithDays(defaultMaxInactiveDuration)
			cfg.Clusters[i].MaxInactiveDuration = value
		}
		if cfg.Clusters[i].MinInactiveDuration == 0 {
			value, _ := ParseDurationWithDays(defaultMinInactiveDuration)
			cfg.Clusters[i].MinInactiveDuration = value
		}
	}
	if cfg.MonitorCheckInterval == 0 {
		value, _ := ParseDurationWithDays("24h")
		cfg.MonitorCheckInterval = value
	}
	if cfg.ArchiveTTL == 0 {
		value, _ := ParseDurationWithDays(defaultArchiveTTL)
		cfg.ArchiveTTL = value
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
func (d *CustomDuration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var value string
	err := unmarshal(&value)
	if err != nil {
		return err
	}

	dur, err := ParseDurationWithDays(value)
	if err != nil {
		return err
	}
	*d = dur
	return nil
}

// ParseDurationWithDays parses a duration from a string similar to standard go
// time.ParseDuration, but layers in support for "d" meaning days, which is
// used in this application. A day simply equates to 24h in this instance.
func ParseDurationWithDays(durationStr string) (CustomDuration, error) {

	var tempDuration time.Duration
	var err error
	// Handle "d" for days by converting to 24 hours and handing off to standard lib
	// to parse:
	if durationStr[len(durationStr)-1:] == "d" {
		// re := regexp.MustCompile("[0-9]+")
		// tempDays := re.FindString(durationStr)
		// days, err := strconv.Atoi(tempDays)
		tempDays := durationStr[:len(durationStr)-1]
		days, err := strconv.Atoi(tempDays)
		if err != nil {
			return 0, err
		}

		tempHours := days * 24
		tempDuration, err = time.ParseDuration(fmt.Sprintf("%dh", tempHours))
		if err != nil {
			return 0, err
		}

	} else {
		tempDuration, err = time.ParseDuration(durationStr)
		if err != nil {
			return 0, err
		}
	}
	return CustomDuration(tempDuration), nil
}
