// Package config handles YAML configuration loading and validation
// for the pvc-migrator tool.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// NamespaceConfig represents a namespace with optional PVC list
type NamespaceConfig struct {
	Name string   `yaml:"name"`
	PVCs []string `yaml:"pvcs,omitempty"`
}

// Config represents the YAML configuration file structure
type Config struct {
	KubeContext      string            `yaml:"kubeContext,omitempty"`
	Namespaces       []NamespaceConfig `yaml:"namespaces"`
	TargetZone       string            `yaml:"targetZone"`
	StorageClass     string            `yaml:"storageClass"`
	MaxConcurrency   int               `yaml:"maxConcurrency"`
	DryRun           bool              `yaml:"dryRun"`
	SkipArgoCD       bool              `yaml:"skipArgoCD"`
	ArgoCDNamespaces []string          `yaml:"argoCDNamespaces"`
}

// DefaultConfig returns a config with default values
func DefaultConfig() *Config {
	return &Config{
		KubeContext:      "", // Use current context if empty
		Namespaces:       []NamespaceConfig{{Name: "default"}},
		TargetZone:       "eu-west-1a",
		StorageClass:     "gp3",
		MaxConcurrency:   5,
		DryRun:           false,
		SkipArgoCD:       false,
		ArgoCDNamespaces: []string{"argocd", "argo-cd", "gitops"},
	}
}

// LoadFromFile loads configuration from a YAML file
func LoadFromFile(path string) (*Config, error) {
	// filepath.Clean is used implicitly by os.ReadFile
	data, err := os.ReadFile(path) //nolint:gosec // Path comes from CLI flag, user-controlled input is expected
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if len(c.Namespaces) == 0 {
		return fmt.Errorf("at least one namespace is required")
	}
	for _, ns := range c.Namespaces {
		if ns.Name == "" {
			return fmt.Errorf("namespace name cannot be empty")
		}
	}
	if c.TargetZone == "" {
		return fmt.Errorf("targetZone is required")
	}
	if c.StorageClass == "" {
		return fmt.Errorf("storageClass is required")
	}
	if c.MaxConcurrency < 1 {
		return fmt.Errorf("maxConcurrency must be at least 1")
	}
	return nil
}

// GetNamespaceNames returns just the namespace names
func (c *Config) GetNamespaceNames() []string {
	names := make([]string, len(c.Namespaces))
	for i, ns := range c.Namespaces {
		names[i] = ns.Name
	}
	return names
}

// WriteExampleConfig writes an example configuration file
func WriteExampleConfig(path string) error {
	example := &Config{
		KubeContext: "", // Optional: specify kubectl context (e.g., "my-cluster-context")
		Namespaces: []NamespaceConfig{
			{Name: "namespace-1", PVCs: []string{"pvc-1", "pvc-2"}},
			{Name: "namespace-2"}, // Will discover all PVCs
		},
		TargetZone:       "eu-west-1a",
		StorageClass:     "gp3",
		MaxConcurrency:   5,
		DryRun:           false,
		SkipArgoCD:       false,
		ArgoCDNamespaces: []string{"argocd", "argo-cd", "gitops"},
	}

	data, err := yaml.Marshal(example)
	if err != nil {
		return fmt.Errorf("failed to marshal example config: %w", err)
	}

	header := `# PVC Migrator Configuration
# 
# This file contains configuration for migrating PVCs between AWS Availability Zones.
#
# Each namespace can optionally specify which PVCs to migrate.
# If no PVCs are specified for a namespace, all PVCs in that namespace will be migrated.
#
# CLI flags can override some values (--zone, --storage-class, etc.)

# kubeContext: my-cluster-context  # Optional: kubectl context to use (defaults to current)

`
	if err := os.WriteFile(path, []byte(header+string(data)), 0600); err != nil {
		return fmt.Errorf("failed to write example config: %w", err)
	}

	return nil
}
