package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigParsing(t *testing.T) {
	tests := []struct {
		name                string
		configStr           string
		expectedConfig      ArchivistConfig
		expectedErrContains string
	}{
		{
			name: "full defined config",
			configStr: `---
clusters:
- name: test cluster
  namespaceCapacity:
    highWatermark: 500
    lowWatermark: 400
  minInactiveDays: 30
  maxInactiveDays: 60
  protectedNamespaces:
  - default
  - very-important
  - special
logLevel: debug
`,
			expectedConfig: ArchivistConfig{
				Clusters: []ClusterConfig{
					{
						Name: "test cluster",
						NamespaceCapacity: NamespaceCapacity{
							HighWatermark: 500,
							LowWatermark:  400,
						},
						MinInactiveDays:     30,
						MaxInactiveDays:     60,
						ProtectedNamespaces: []string{"default", "very-important", "special"},
					},
				},

				LogLevel: "debug",
			},
		},
		{
			name: "default config",
			configStr: `---
clusters:
- name: test cluster
`,
			expectedConfig: ArchivistConfig{
				Clusters: []ClusterConfig{
					{
						Name: "test cluster",
						NamespaceCapacity: NamespaceCapacity{
							HighWatermark: 0,
							LowWatermark:  0,
						},
						MaxInactiveDays:     0,
						MinInactiveDays:     0,
						ProtectedNamespaces: []string{"default", "openshift-infra"},
					},
				},
				LogLevel: "info",
			},
		},
		{
			name: "invalid max and min inactive days",
			// TODO: test dangerous high/low watermarks and throw errors
			configStr: `---
clusters:
- name: "test cluster"
  minInactiveDays: 30
  maxInactiveDays: 20
`,
			expectedErrContains: "maxInactiveDays",
		},
		{
			name: "no clusters defined",
			configStr: `---
logLevel: debug
`,
			expectedErrContains: "no clusters in config",
		},
		{
			name: "cluster must have a name",
			configStr: `---
clusters:
- minInactiveDays: 30
  maxInactiveDays: 60
`,
			expectedErrContains: "cluster must have a name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewArchivistConfigFromString(tc.configStr)
			if tc.expectedErrContains != "" {
				if assert.NotNil(t, err) {
					assert.True(t, strings.Contains(err.Error(), tc.expectedErrContains))
				}
			} else {
				if assert.Nil(t, err) {
					assert.Equal(t, tc.expectedConfig, cfg)
				}
			}
		})
	}
}
