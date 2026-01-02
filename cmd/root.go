package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"pvc-migrator/internal/config"
)

var (
	// Global config file path
	configFile string

	// Loaded configuration
	cfg *config.Config

	// CLI flag values (can override config file)
	kubeContext      string
	namespaces       []string
	targetZone       string
	storageClass     string
	maxConcurrency   int
	dryRun           bool
	skipArgoCD       bool
	argoCDNamespaces []string
	planOnly         bool
)

var rootCmd = &cobra.Command{
	Use:   "pvc-migrator",
	Short: "Migrate Kubernetes PVCs between AWS Availability Zones",
	Long: `A robust CLI tool to migrate Kubernetes PersistentVolumeClaims (PVCs) 
from one AWS Availability Zone to another using AWS EBS Snapshots.

This tool performs the following steps for each PVC:
1. Retrieves PVC/PV information and AWS Volume ID
2. Creates an EBS snapshot of the original volume
3. Creates a new volume from the snapshot in the target AZ
4. Cleans up old Kubernetes PVC/PV resources
5. Creates new static PV and bound PVC in the target zone

Example:
  pvc-migrator migrate -n budibase -z eu-west-1a -s gp3 \
    -p database-storage-0,database-storage-1,minio-data
  
  # Multiple namespaces:
  pvc-migrator migrate -n ns1,ns2,ns3 -z eu-west-1a -s gp3
    
  # Using a config file:
  pvc-migrator migrate -c config.yaml`,
	Version: "1.0.0",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return loadConfig(cmd)
	},
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Start the PVC migration process",
	Long:  `Migrate specified PVCs to the target AWS Availability Zone.`,
	RunE:  runMigrate,
}

var initConfigCmd = &cobra.Command{
	Use:   "init-config [filename]",
	Short: "Generate an example configuration file",
	Long:  `Generate an example YAML configuration file with default values.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filename := "pvc-migrator.yaml"
		if len(args) > 0 {
			filename = args[0]
		}
		if err := config.WriteExampleConfig(filename); err != nil {
			return err
		}
		fmt.Printf("✅ Example configuration written to: %s\n", filename)
		return nil
	},
}

func init() {
	// Global config flag available to all commands
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "Path to YAML configuration file")

	// Migration-specific flags
	migrateCmd.Flags().StringVar(&kubeContext, "context", "", "Kubernetes context to use (defaults to current context)")
	migrateCmd.Flags().StringSliceVarP(&namespaces, "namespace", "n", nil, "Kubernetes namespace(s) containing the PVCs (comma-separated, discovers all PVCs)")
	migrateCmd.Flags().StringVarP(&targetZone, "zone", "z", "", "Target AWS Availability Zone")
	migrateCmd.Flags().StringVarP(&storageClass, "storage-class", "s", "", "Storage class for the new PVs")
	migrateCmd.Flags().IntVar(&maxConcurrency, "concurrency", 0, "Maximum concurrent migrations")
	migrateCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be done without making changes")
	migrateCmd.Flags().BoolVar(&skipArgoCD, "skip-argocd", false, "Skip ArgoCD auto-sync detection and handling")
	migrateCmd.Flags().StringSliceVar(&argoCDNamespaces, "argocd-namespaces", nil, "Namespaces to search for ArgoCD applications")
	migrateCmd.Flags().BoolVar(&planOnly, "plan", false, "Show migration plan and exit without executing")

	rootCmd.AddCommand(migrateCmd)
	rootCmd.AddCommand(initConfigCmd)
}

// loadConfig loads configuration from file and merges with CLI flags
func loadConfig(cmd *cobra.Command) error {
	// Start with default config
	cfg = config.DefaultConfig()

	// Load from config file if specified
	if configFile != "" {
		fileCfg, err := config.LoadFromFile(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config file: %w", err)
		}
		cfg = fileCfg
		// Note: Config loaded message is now printed in migrate.go with styling
	}

	// CLI flags override config file values
	// Only override if the flag was explicitly set
	if cmd.Flags().Changed("context") {
		cfg.KubeContext = kubeContext
	}
	if cmd.Flags().Changed("namespace") {
		// Convert CLI namespaces to NamespaceConfig (no specific PVCs, discover all)
		cfg.Namespaces = make([]config.NamespaceConfig, len(namespaces))
		for i, ns := range namespaces {
			cfg.Namespaces[i] = config.NamespaceConfig{Name: ns}
		}
	}
	if cmd.Flags().Changed("zone") {
		cfg.TargetZone = targetZone
	}
	if cmd.Flags().Changed("storage-class") {
		cfg.StorageClass = storageClass
	}
	if cmd.Flags().Changed("concurrency") {
		cfg.MaxConcurrency = maxConcurrency
	}
	if cmd.Flags().Changed("dry-run") {
		cfg.DryRun = dryRun
	}
	if cmd.Flags().Changed("skip-argocd") {
		cfg.SkipArgoCD = skipArgoCD
	}
	if cmd.Flags().Changed("argocd-namespaces") {
		cfg.ArgoCDNamespaces = argoCDNamespaces
	}

	// Sync back to global vars for backward compatibility
	kubeContext = cfg.KubeContext
	namespaces = cfg.GetNamespaceNames()
	targetZone = cfg.TargetZone
	storageClass = cfg.StorageClass
	maxConcurrency = cfg.MaxConcurrency
	dryRun = cfg.DryRun
	skipArgoCD = cfg.SkipArgoCD
	argoCDNamespaces = cfg.ArgoCDNamespaces

	return nil
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
