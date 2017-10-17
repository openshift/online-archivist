package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// unsafeDurationParse parses a duration string with days support, but panics
// on an error. It is intended to just let us do inline date parsing in tests.
func unsafeDurationParse(dur string) CustomDuration {
	duration, err := ParseDurationWithDays(dur)
	if err != nil {
		panic(fmt.Sprintf("bad duration in test data: %s", dur))
	}
	return duration
}

func TestConfigParsing(t *testing.T) {

	tests := []struct {
		name                string
		configStr           string
		expectedConfig      ArchivistConfig
		expectedErrContains string
	}{
		{
			name: "full defined config",
			configStr: `logLevel: info
clusters:
- name: test cluster
  namespaceCapacity:
    highWatermark: 500
    lowWatermark: 400
  minInactiveDuration: 30m
  maxInactiveDuration: 60m
  protectedNamespaces:
  - default
  - very-important
  - special
dryRun: false
deleteArchivedNamespaces: true
archiveTTL: 7d
monitorCheckInterval: 12h`,
			expectedConfig: ArchivistConfig{
				LogLevel: "info",
				Clusters: []ClusterConfig{
					{
						Name: "test cluster",
						NamespaceCapacity: NamespaceCapacity{
							HighWatermark: 500,
							LowWatermark:  400,
						},
						MinInactiveDuration: unsafeDurationParse("30m"),
						MaxInactiveDuration: unsafeDurationParse("60m"),
						ProtectedNamespaces: []string{"default", "very-important", "special"},
					},
				},
				DryRun:                   false,
				MonitorCheckInterval:     unsafeDurationParse("12h"),
				DeleteArchivedNamespaces: true,
				ArchiveTTL:               unsafeDurationParse("7d"),
			},
		},
		{
			name: "default config",
			configStr: `logLevel: info
clusters:
- name: test cluster`,
			expectedConfig: ArchivistConfig{
				LogLevel: "info",
				Clusters: []ClusterConfig{
					{
						Name: "test cluster",
						NamespaceCapacity: NamespaceCapacity{
							HighWatermark: 0,
							LowWatermark:  0,
						},
						MinInactiveDuration: unsafeDurationParse("720h"),  // 30 days
						MaxInactiveDuration: unsafeDurationParse("2160h"), // 90 days
						ProtectedNamespaces: []string{"default", "openshift-infra"},
					},
				},
				MonitorCheckInterval:     unsafeDurationParse("24h"),
				DeleteArchivedNamespaces: false,
				ArchiveTTL:               unsafeDurationParse(defaultArchiveTTL),
			},
		},
		{
			name: "invalid max and min inactive days",
			// TODO: test dangerous high/low watermarks and throw errors
			configStr: `clusters:
- name: "test cluster"
  minInactiveDuration: 30m
  maxInactiveDuration: 20m`,
			expectedErrContains: "MaxInactiveTime",
		},
		{
			name:                "no clusters defined",
			configStr:           ` logLevel: debug`,
			expectedErrContains: "no clusters in config",
		},
		{
			name: "cluster must have a name",
			configStr: `clusters:
- minInactiveDays: 30
maxInactiveDays: 60`,
			expectedErrContains: "cluster must have a name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewArchivistConfigFromString(tc.configStr)
			if tc.expectedErrContains != "" {
				if assert.NotNil(t, err) {
					assert.True(t, strings.Contains(err.Error(), tc.expectedErrContains), "the input config does NOT match the expected config")
				}
			} else {
				if assert.Nil(t, err) {
					assert.Equal(t, tc.expectedConfig, cfg, "the input config matches the expected config")
				}
			}
		})
	}
}
