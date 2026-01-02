package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()

	assert.Equal(t, []NamespaceConfig{{Name: "default"}}, cfg.Namespaces)
	assert.Equal(t, "eu-west-1a", cfg.TargetZone)
	assert.Equal(t, "gp3", cfg.StorageClass)
	assert.Equal(t, 5, cfg.MaxConcurrency)
	assert.False(t, cfg.DryRun)
	assert.False(t, cfg.SkipArgoCD)
	assert.Equal(t, []string{"argocd", "argo-cd", "gitops"}, cfg.ArgoCDNamespaces)
}

func TestLoadFromFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		filePath    string
		wantErr     bool
		errContains string
		validate    func(t *testing.T, cfg *Config)
	}{
		{
			name:     "valid_config",
			filePath: "../../testdata/valid_config.yaml",
			wantErr:  false,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "test-context", cfg.KubeContext)
				assert.Equal(t, "us-west-2a", cfg.TargetZone)
				assert.Equal(t, "gp3", cfg.StorageClass)
				assert.Equal(t, 3, cfg.MaxConcurrency)
				require.Len(t, cfg.Namespaces, 2)
				assert.Equal(t, "test-ns", cfg.Namespaces[0].Name)
				assert.Equal(t, []string{"pvc-1", "pvc-2"}, cfg.Namespaces[0].PVCs)
				assert.Equal(t, "another-ns", cfg.Namespaces[1].Name)
				assert.Empty(t, cfg.Namespaces[1].PVCs)
				assert.Equal(t, []string{"argocd", "gitops"}, cfg.ArgoCDNamespaces)
			},
		},
		{
			name:        "invalid_yaml",
			filePath:    "../../testdata/invalid_config.yaml",
			wantErr:     true,
			errContains: "failed to parse config file",
		},
		{
			name:        "file_not_found",
			filePath:    "../../testdata/nonexistent.yaml",
			wantErr:     true,
			errContains: "failed to read config file",
		},
		{
			name:     "empty_config_uses_defaults",
			filePath: "../../testdata/empty_config.yaml",
			wantErr:  false,
			validate: func(t *testing.T, cfg *Config) {
				// Should have defaults
				assert.Equal(t, "eu-west-1a", cfg.TargetZone)
				assert.Equal(t, "gp3", cfg.StorageClass)
				assert.Equal(t, 5, cfg.MaxConcurrency)
			},
		},
		{
			name:     "minimal_config",
			filePath: "../../testdata/minimal_config.yaml",
			wantErr:  false,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "eu-west-1a", cfg.TargetZone)
				assert.Equal(t, "gp3", cfg.StorageClass)
				assert.Equal(t, 1, cfg.MaxConcurrency)
				require.Len(t, cfg.Namespaces, 1)
				assert.Equal(t, "default", cfg.Namespaces[0].Name)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := LoadFromFile(tc.filePath)

			if tc.wantErr {
				require.Error(t, err)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)

			if tc.validate != nil {
				tc.validate(t, cfg)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		config      *Config
		wantErr     bool
		errContains string
	}{
		{
			name:    "valid_config",
			config:  DefaultConfig(),
			wantErr: false,
		},
		{
			name: "valid_with_multiple_namespaces",
			config: &Config{
				Namespaces: []NamespaceConfig{
					{Name: "ns1"},
					{Name: "ns2", PVCs: []string{"pvc-a"}},
				},
				TargetZone:     "us-east-1a",
				StorageClass:   "gp2",
				MaxConcurrency: 10,
			},
			wantErr: false,
		},
		{
			name: "empty_namespaces",
			config: &Config{
				Namespaces:     []NamespaceConfig{},
				TargetZone:     "us-west-2a",
				StorageClass:   "gp3",
				MaxConcurrency: 1,
			},
			wantErr:     true,
			errContains: "at least one namespace is required",
		},
		{
			name: "empty_namespace_name",
			config: &Config{
				Namespaces:     []NamespaceConfig{{Name: ""}},
				TargetZone:     "us-west-2a",
				StorageClass:   "gp3",
				MaxConcurrency: 1,
			},
			wantErr:     true,
			errContains: "namespace name cannot be empty",
		},
		{
			name: "missing_target_zone",
			config: &Config{
				Namespaces:     []NamespaceConfig{{Name: "default"}},
				TargetZone:     "",
				StorageClass:   "gp3",
				MaxConcurrency: 1,
			},
			wantErr:     true,
			errContains: "targetZone is required",
		},
		{
			name: "missing_storage_class",
			config: &Config{
				Namespaces:     []NamespaceConfig{{Name: "default"}},
				TargetZone:     "us-west-2a",
				StorageClass:   "",
				MaxConcurrency: 1,
			},
			wantErr:     true,
			errContains: "storageClass is required",
		},
		{
			name: "invalid_concurrency_zero",
			config: &Config{
				Namespaces:     []NamespaceConfig{{Name: "default"}},
				TargetZone:     "us-west-2a",
				StorageClass:   "gp3",
				MaxConcurrency: 0,
			},
			wantErr:     true,
			errContains: "maxConcurrency must be at least 1",
		},
		{
			name: "invalid_concurrency_negative",
			config: &Config{
				Namespaces:     []NamespaceConfig{{Name: "default"}},
				TargetZone:     "us-west-2a",
				StorageClass:   "gp3",
				MaxConcurrency: -5,
			},
			wantErr:     true,
			errContains: "maxConcurrency must be at least 1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.config.Validate()

			if tc.wantErr {
				require.Error(t, err)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestConfig_GetNamespaceNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		namespaces []NamespaceConfig
		expected   []string
	}{
		{
			name:       "single_namespace",
			namespaces: []NamespaceConfig{{Name: "default"}},
			expected:   []string{"default"},
		},
		{
			name: "multiple_namespaces",
			namespaces: []NamespaceConfig{
				{Name: "ns1"},
				{Name: "ns2"},
				{Name: "ns3"},
			},
			expected: []string{"ns1", "ns2", "ns3"},
		},
		{
			name: "namespaces_with_pvcs",
			namespaces: []NamespaceConfig{
				{Name: "app", PVCs: []string{"pvc-1", "pvc-2"}},
				{Name: "db"},
			},
			expected: []string{"app", "db"},
		},
		{
			name:       "empty_namespaces",
			namespaces: []NamespaceConfig{},
			expected:   []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{Namespaces: tc.namespaces}
			result := cfg.GetNamespaceNames()

			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestWriteExampleConfig(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for test
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "example_config.yaml")

	// Write example config
	err := WriteExampleConfig(testPath)
	require.NoError(t, err)

	// Verify file exists
	_, err = os.Stat(testPath)
	require.NoError(t, err)

	// Verify file is readable and valid YAML
	cfg, err := LoadFromFile(testPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Validate the example config
	err = cfg.Validate()
	require.NoError(t, err)

	// Verify expected content
	assert.Equal(t, "eu-west-1a", cfg.TargetZone)
	assert.Equal(t, "gp3", cfg.StorageClass)
	assert.Equal(t, 5, cfg.MaxConcurrency)
	require.Len(t, cfg.Namespaces, 2)
	assert.Equal(t, "namespace-1", cfg.Namespaces[0].Name)
	assert.Equal(t, []string{"pvc-1", "pvc-2"}, cfg.Namespaces[0].PVCs)
}

func TestWriteExampleConfig_InvalidPath(t *testing.T) {
	t.Parallel()

	// Try to write to an invalid path
	err := WriteExampleConfig("/nonexistent/directory/config.yaml")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write example config")
}

func TestNamespaceConfig_Fields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		ns       NamespaceConfig
		expected struct {
			name string
			pvcs []string
		}
	}{
		{
			name: "with_pvcs",
			ns:   NamespaceConfig{Name: "test-ns", PVCs: []string{"pvc1", "pvc2"}},
			expected: struct {
				name string
				pvcs []string
			}{
				name: "test-ns",
				pvcs: []string{"pvc1", "pvc2"},
			},
		},
		{
			name: "without_pvcs",
			ns:   NamespaceConfig{Name: "empty-ns"},
			expected: struct {
				name string
				pvcs []string
			}{
				name: "empty-ns",
				pvcs: nil,
			},
		},
		{
			name: "empty_pvcs_slice",
			ns:   NamespaceConfig{Name: "ns", PVCs: []string{}},
			expected: struct {
				name string
				pvcs []string
			}{
				name: "ns",
				pvcs: []string{},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.expected.name, tc.ns.Name)
			assert.Equal(t, tc.expected.pvcs, tc.ns.PVCs)
		})
	}
}
