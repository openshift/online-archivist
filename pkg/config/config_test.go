package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConfigParsing(t *testing.T) {

	value, _ := time.ParseDuration("30m")
	minInactiveDuration1 := MyDuration(value)
	value, _ = time.ParseDuration("60m")
	maxInactiveDuration1 := MyDuration(value)
	value, _ = time.ParseDuration("12h")
	monitorCheckInterval1 := MyDuration(value)

	// value, _ = time.ParseDuration("720h")
	value, _ = ParseDays("30d")
	minInactiveDuration2 := MyDuration(value)
	// value, _ = time.ParseDuration("1440h")
	value, _ = ParseDays("60d")
	maxInactiveDuration2 := MyDuration(value)
	value, _ = time.ParseDuration("24h")
	monitorCheckInterval2 := MyDuration(value)

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
						MinInactiveDuration: minInactiveDuration1,
						MaxInactiveDuration: maxInactiveDuration1,
						ProtectedNamespaces: []string{"default", "very-important", "special"},
					},
				},
				DryRun:               false,
				MonitorCheckInterval: monitorCheckInterval1,
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
						MaxInactiveDuration: maxInactiveDuration2,
						MinInactiveDuration: minInactiveDuration2,
						ProtectedNamespaces: []string{"default", "openshift-infra"},
					},
				},
				MonitorCheckInterval: monitorCheckInterval2,
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
