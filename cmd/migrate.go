// Package cmd implements the CLI commands for the pvc-migrator tool.
// It provides commands for migrating PVCs between AWS Availability Zones.
package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/cesarempathy/pv-zone-migrator/internal/aws"
	"github.com/cesarempathy/pv-zone-migrator/internal/k8s"
	"github.com/cesarempathy/pv-zone-migrator/internal/migrator"
	"github.com/cesarempathy/pv-zone-migrator/internal/ui"
)

// Scale mode constants
const (
	scaleModeAuto   = "auto"
	scaleModeManual = "manual"
)

// Console output styles
var (
	cliHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("99"))

	cliSuccessStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42"))

	cliWarningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))

	cliInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75"))

	cliDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	cliValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255"))

	cliBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("99")).
			Padding(0, 1).
			MarginTop(1)

	cliLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75")).
			Width(16)
)

// scaledWorkloadsPerNS stores scaled workloads for a namespace
type scaledWorkloadsPerNS struct {
	Namespace string
	Workloads []k8s.WorkloadInfo
}

// migrationContext holds shared state for the migration process
type migrationContext struct {
	ctx              context.Context
	k8sClient        *k8s.Client
	argoCDApps       []k8s.ArgoCDAppInfo
	scaledWorkloads  []scaledWorkloadsPerNS
	workloadInfoByNS map[string][]k8s.WorkloadInfo
}

// restoreOnError restores workloads and ArgoCD state on error
func (mc *migrationContext) restoreOnError() {
	for _, sw := range mc.scaledWorkloads {
		fmt.Printf("⚠️  Restoring workloads in namespace '%s' due to error...\n", sw.Namespace)
		_ = mc.k8sClient.ScaleUpWorkloads(mc.ctx, sw.Namespace, sw.Workloads)
	}
	if len(mc.argoCDApps) > 0 {
		_ = mc.k8sClient.EnableArgoCDAutoSync(mc.ctx, mc.argoCDApps)
	}
}

// handleManualScaling handles manual workload scaling mode
func (mc *migrationContext) handleManualScaling() error {
	fmt.Println()
	fmt.Println(cliWarningStyle.Render("⚠️  Please scale down the workloads manually before proceeding:"))
	fmt.Println()

	for ns, workloads := range mc.workloadInfoByNS {
		if len(workloads) == 0 {
			continue
		}
		for _, w := range workloads {
			var cmdStr string
			switch w.Kind {
			case "Deployment":
				cmdStr = fmt.Sprintf("kubectl scale deployment %s --replicas=0 -n %s", w.Name, ns)
			case "StatefulSet":
				cmdStr = fmt.Sprintf("kubectl scale statefulset %s --replicas=0 -n %s", w.Name, ns)
			}
			if kubeContext != "" {
				cmdStr += fmt.Sprintf(" --context=%s", kubeContext)
			}
			fmt.Printf("  %s\n", cliDimStyle.Render(cmdStr))
		}
	}

	fmt.Println()
	fmt.Println(cliInfoStyle.Render("Waiting for you to run the commands above..."))
	fmt.Println(cliDimStyle.Render("Press Enter when workloads are scaled down, or 'q' to quit:"))

	var input string
	_, _ = fmt.Scanln(&input)
	if strings.ToLower(strings.TrimSpace(input)) == "q" {
		if len(mc.argoCDApps) > 0 {
			_ = mc.k8sClient.EnableArgoCDAutoSync(mc.ctx, mc.argoCDApps)
		}
		return fmt.Errorf("migration cancelled by user")
	}

	// Record workloads for restoration
	for ns, workloads := range mc.workloadInfoByNS {
		if len(workloads) > 0 {
			mc.scaledWorkloads = append(mc.scaledWorkloads, scaledWorkloadsPerNS{Namespace: ns, Workloads: workloads})
		}
	}

	// Wait for pods to terminate
	fmt.Println(cliInfoStyle.Render("⏳ Verifying workloads are scaled down..."))
	for _, ns := range namespaces {
		if len(mc.workloadInfoByNS[ns]) > 0 {
			if err := mc.k8sClient.WaitForWorkloadsScaledDown(mc.ctx, ns, 5*time.Minute); err != nil {
				if len(mc.argoCDApps) > 0 {
					_ = mc.k8sClient.EnableArgoCDAutoSync(mc.ctx, mc.argoCDApps)
				}
				return fmt.Errorf("workloads not scaled down in namespace '%s': %w", ns, err)
			}
		}
	}
	fmt.Println(cliSuccessStyle.Render("✓ All workloads scaled down"))
	return nil
}

// handleAutoScaling handles automatic workload scaling mode
func (mc *migrationContext) handleAutoScaling() error {
	for _, ns := range namespaces {
		runningWorkloads := mc.workloadInfoByNS[ns]
		if len(runningWorkloads) == 0 {
			continue
		}

		scaledWorkloads, err := mc.k8sClient.ScaleDownWorkloads(mc.ctx, ns)
		if err != nil {
			mc.restoreOnError()
			return fmt.Errorf("failed to scale down workloads in namespace '%s': %w", ns, err)
		}
		mc.scaledWorkloads = append(mc.scaledWorkloads, scaledWorkloadsPerNS{Namespace: ns, Workloads: scaledWorkloads})

		if err := mc.k8sClient.WaitForWorkloadsScaledDown(mc.ctx, ns, 5*time.Minute); err != nil {
			mc.restoreOnError()
			return fmt.Errorf("failed waiting for pods to terminate in namespace '%s': %w", ns, err)
		}
	}
	return nil
}

// pvcWithNamespace represents a PVC with its namespace
type pvcWithNamespace struct {
	Namespace string
	Name      string
}

// discoverPVCs discovers all PVCs from configured namespaces
func discoverPVCs(ctx context.Context, k8sClient *k8s.Client) ([]pvcWithNamespace, map[string][]string, error) {
	var allPVCs []pvcWithNamespace
	pvcsByNamespace := make(map[string][]string)

	for _, nsCfg := range cfg.Namespaces {
		if len(nsCfg.PVCs) > 0 {
			for _, pvc := range nsCfg.PVCs {
				allPVCs = append(allPVCs, pvcWithNamespace{Namespace: nsCfg.Name, Name: pvc})
			}
			pvcsByNamespace[nsCfg.Name] = nsCfg.PVCs
		} else {
			discovered, err := k8sClient.ListPVCs(ctx, nsCfg.Name)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to list PVCs in namespace '%s': %w", nsCfg.Name, err)
			}
			pvcsByNamespace[nsCfg.Name] = discovered
			for _, pvc := range discovered {
				allPVCs = append(allPVCs, pvcWithNamespace{Namespace: nsCfg.Name, Name: pvc})
			}
		}
	}
	return allPVCs, pvcsByNamespace, nil
}

// handleArgoCDApps finds and disables ArgoCD auto-sync for affected applications
func handleArgoCDApps(ctx context.Context, k8sClient *k8s.Client) ([]k8s.ArgoCDAppInfo, error) {
	if skipArgoCD {
		return nil, nil
	}

	var argoCDApps []k8s.ArgoCDAppInfo
	for _, ns := range namespaces {
		apps, err := k8sClient.FindArgoCDAppsForNamespace(ctx, ns, argoCDNamespaces)
		if err != nil {
			continue
		}
		argoCDApps = append(argoCDApps, apps...)
	}

	argoCDAppNames := make([]string, 0, len(argoCDApps))
	for _, app := range argoCDApps {
		argoCDAppNames = append(argoCDAppNames, fmt.Sprintf("%s/%s", app.Namespace, app.Name))
	}

	fmt.Println(buildArgoCDBox(argoCDAppNames, argoCDNamespaces, dryRun))

	if len(argoCDApps) > 0 && !dryRun {
		if err := k8sClient.DisableArgoCDAutoSync(ctx, argoCDApps); err != nil {
			return nil, fmt.Errorf("failed to disable ArgoCD auto-sync: %w", err)
		}
	}
	return argoCDApps, nil
}

// collectWorkloadInfo gathers information about running workloads in all namespaces
func collectWorkloadInfo(ctx context.Context, k8sClient *k8s.Client, argoCDApps []k8s.ArgoCDAppInfo) (map[string][]string, map[string][]k8s.WorkloadInfo, error) {
	workloadsByNS := make(map[string][]string)
	workloadInfoByNS := make(map[string][]k8s.WorkloadInfo)

	for _, ns := range namespaces {
		runningWorkloads, err := k8sClient.GetWorkloadStatus(ctx, ns)
		if err != nil {
			if len(argoCDApps) > 0 && !dryRun {
				_ = k8sClient.EnableArgoCDAutoSync(ctx, argoCDApps)
			}
			return nil, nil, fmt.Errorf("failed to check workload status in namespace '%s': %w", ns, err)
		}
		workloadInfoByNS[ns] = runningWorkloads
		for _, w := range runningWorkloads {
			workloadsByNS[ns] = append(workloadsByNS[ns], fmt.Sprintf("%s/%s (replicas: %d)", w.Kind, w.Name, w.Replicas))
		}
	}
	return workloadsByNS, workloadInfoByNS, nil
}

func runMigrate(_ *cobra.Command, _ []string) error {
	ctx := context.Background()

	// Validate scaleMode
	if scaleMode != scaleModeAuto && scaleMode != scaleModeManual {
		return fmt.Errorf("invalid scale mode '%s': must be either '%s' or '%s'", scaleMode, scaleModeAuto, scaleModeManual)
	}

	// Print header info
	printHeaderInfo()

	// Initialize Kubernetes client with optional context
	k8sClient, err := k8s.NewClient(kubeContext)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Discover PVCs and collect initial information
	allPVCs, _, argoCDApps, _, workloadInfoByNS, err := initializeMigration(ctx, k8sClient)
	if err != nil {
		return err
	}

	// Create migration context
	mc := &migrationContext{
		ctx:              ctx,
		k8sClient:        k8sClient,
		argoCDApps:       argoCDApps,
		workloadInfoByNS: workloadInfoByNS,
	}

	// Handle workload scaling
	totalWorkloads := calculateTotalWorkloads(workloadInfoByNS)
	if totalWorkloads > 0 && !dryRun {
		if err := handleWorkloadScaling(mc); err != nil {
			return err
		}
	}

	// Initialize AWS client and create migrator
	ec2Client, err := aws.NewEC2Client(ctx)
	if err != nil {
		mc.restoreOnError()
		return fmt.Errorf("failed to create AWS EC2 client: %w", err)
	}

	m, config := createMigrator(k8sClient, ec2Client, allPVCs)

	// Handle plan-only mode
	if planOnly {
		return handlePlanMode(ctx, m)
	}

	// Run migration UI
	finalModel, err := runMigrationUI(mc, m, config)
	if err != nil {
		mc.restoreOnError()
		return err
	}

	// Print summary and cleanup
	if fm, ok := finalModel.(ui.Model); ok {
		fm.PrintSummary()
		if fm.HasErrors() {
			os.Exit(1)
		}
	}

	// Restore workloads and ArgoCD
	restoreWorkloads(ctx, k8sClient, mc)
	restoreArgoCDAutoSync(ctx, k8sClient, mc)

	return nil
}

// printHeaderInfo prints the migration header information
func printHeaderInfo() {
	if configFile != "" {
		fmt.Printf("%s %s\n", cliDimStyle.Render("📄 Config:"), configFile)
	}
	if kubeContext != "" {
		fmt.Printf("%s %s\n", cliDimStyle.Render("☸  Context:"), kubeContext)
	}
}

// initializeMigration discovers PVCs, ArgoCD apps, and workloads
func initializeMigration(ctx context.Context, k8sClient *k8s.Client) (
	[]pvcWithNamespace,
	map[string][]string,
	[]k8s.ArgoCDAppInfo,
	map[string][]string,
	map[string][]k8s.WorkloadInfo,
	error,
) {
	// Discover PVCs
	allPVCs, pvcsByNamespace, err := discoverPVCs(ctx, k8sClient)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if len(allPVCs) == 0 {
		return nil, nil, nil, nil, nil, fmt.Errorf("no PVCs found in any of the specified namespaces")
	}
	fmt.Println(buildDiscoveryBox(pvcsByNamespace, len(allPVCs)))

	// Handle ArgoCD applications
	argoCDApps, err := handleArgoCDApps(ctx, k8sClient)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// Collect workload information
	workloadsByNS, workloadInfoByNS, err := collectWorkloadInfo(ctx, k8sClient, argoCDApps)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	fmt.Println(buildWorkloadsBox(workloadsByNS, dryRun, scaleMode))

	return allPVCs, pvcsByNamespace, argoCDApps, workloadsByNS, workloadInfoByNS, nil
}

// calculateTotalWorkloads counts total workloads across all namespaces
func calculateTotalWorkloads(workloadInfoByNS map[string][]k8s.WorkloadInfo) int {
	total := 0
	for _, workloads := range workloadInfoByNS {
		total += len(workloads)
	}
	return total
}

// handleWorkloadScaling handles the scaling of workloads based on scale mode
func handleWorkloadScaling(mc *migrationContext) error {
	switch scaleMode {
	case scaleModeManual:
		return mc.handleManualScaling()
	default:
		return mc.handleAutoScaling()
	}
}

// createMigrator creates the migrator instance with necessary clients
func createMigrator(k8sClient *k8s.Client, ec2Client *aws.Client, allPVCs []pvcWithNamespace) (
	*migrator.Migrator,
	*migrator.Config,
) {
	// Build PVC list with namespace prefix
	pvcListWithNS := make([]string, 0, len(allPVCs))
	for _, pvc := range allPVCs {
		pvcListWithNS = append(pvcListWithNS, fmt.Sprintf("%s/%s", pvc.Namespace, pvc.Name))
	}

	// Create migration config
	config := &migrator.Config{
		Namespaces:     namespaces,
		TargetZone:     targetZone,
		StorageClass:   storageClass,
		MaxConcurrency: maxConcurrency,
		PVCList:        pvcListWithNS,
		DryRun:         dryRun,
	}

	m := migrator.New(config, k8sClient, ec2Client)
	return m, config
}

// handlePlanMode generates and displays the migration plan
func handlePlanMode(ctx context.Context, m *migrator.Migrator) error {
	fmt.Println("\n🔍 Generating migration plan...")

	plan, err := m.GeneratePlan(ctx)
	if err != nil {
		return fmt.Errorf("failed to generate plan: %w", err)
	}

	fmt.Print(migrator.FormatPlan(plan))
	fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(
		"Run without --plan flag to execute the migration."))
	fmt.Println()

	return nil
}

// runMigrationUI creates and runs the Bubble Tea UI
func runMigrationUI(_ *migrationContext, m *migrator.Migrator, config *migrator.Config) (tea.Model, error) {
	model := ui.NewModel(m, config)
	p := tea.NewProgram(model, tea.WithAltScreen())

	finalModel, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("UI error: %w", err)
	}

	return finalModel, nil
}

// restoreWorkloads scales workloads back to their original replica counts
func restoreWorkloads(ctx context.Context, k8sClient *k8s.Client, mc *migrationContext) {
	if len(mc.scaledWorkloads) == 0 || dryRun {
		return
	}

	fmt.Println("\n🚀 Restoring workloads to original replica counts...")
	for _, sw := range mc.scaledWorkloads {
		fmt.Printf("   Namespace '%s':\n", sw.Namespace)
		for _, w := range sw.Workloads {
			fmt.Printf("     - %s/%s → %d replicas\n", w.Kind, w.Name, w.Replicas)
		}
		if err := k8sClient.ScaleUpWorkloads(ctx, sw.Namespace, sw.Workloads); err != nil {
			fmt.Printf("   ⚠️  Warning: Failed to restore some workloads in '%s': %v\n", sw.Namespace, err)
			fmt.Println("      Please manually restore workloads using kubectl")
		} else {
			fmt.Printf("   ✅ Workloads restored in namespace '%s'\n", sw.Namespace)
		}
	}
}

// restoreArgoCDAutoSync re-enables auto-sync for ArgoCD applications
func restoreArgoCDAutoSync(ctx context.Context, k8sClient *k8s.Client, mc *migrationContext) {
	if len(mc.argoCDApps) == 0 || dryRun {
		return
	}

	fmt.Println("\n🔓 Re-enabling ArgoCD auto-sync...")
	for _, app := range mc.argoCDApps {
		fmt.Printf("   - %s/%s\n", app.Namespace, app.Name)
	}
	if err := k8sClient.EnableArgoCDAutoSync(ctx, mc.argoCDApps); err != nil {
		fmt.Printf("⚠️  Warning: Failed to re-enable ArgoCD auto-sync: %v\n", err)
		fmt.Println("   Please manually re-enable auto-sync in ArgoCD")
	} else {
		fmt.Println("   ✅ Auto-sync re-enabled")
	}
}

// buildDiscoveryBox creates a styled box for PVC discovery results
func buildDiscoveryBox(pvcsByNamespace map[string][]string, totalPVCs int) string {
	var content strings.Builder

	content.WriteString(cliHeaderStyle.Render("PVC Discovery"))
	content.WriteString("\n\n")

	for ns, pvcs := range pvcsByNamespace {
		if len(pvcs) == 0 {
			content.WriteString(fmt.Sprintf("  %s %s\n",
				cliWarningStyle.Render("⚠"),
				cliDimStyle.Render(fmt.Sprintf("%s: no PVCs found", ns))))
			continue
		}

		content.WriteString(fmt.Sprintf("  %s %s %s\n",
			cliInfoStyle.Render("◆"),
			cliValueStyle.Render(ns),
			cliDimStyle.Render(fmt.Sprintf("(%d PVCs)", len(pvcs)))))

		// Show PVCs in a compact grid
		content.WriteString(formatPVCGrid(pvcs))
	}

	content.WriteString(fmt.Sprintf("\n  %s %s",
		cliLabelStyle.Render("Total:"),
		cliHeaderStyle.Render(fmt.Sprintf("%d PVCs", totalPVCs))))

	return cliBoxStyle.Render(content.String())
}

// formatPVCGrid formats PVC names in a compact grid
func formatPVCGrid(pvcs []string) string {
	var b strings.Builder
	maxPerLine := 3
	maxLen := 26

	for i, pvc := range pvcs {
		if i%maxPerLine == 0 {
			b.WriteString("    ")
		}

		name := pvc
		if len(name) > maxLen {
			name = name[:maxLen-2] + ".."
		}

		b.WriteString(cliDimStyle.Render(fmt.Sprintf("%-28s", name)))

		if (i+1)%maxPerLine == 0 && i < len(pvcs)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// buildArgoCDBox creates a styled box for ArgoCD detection results
func buildArgoCDBox(apps []string, searchNamespaces []string, isDryRun bool) string {
	var content strings.Builder

	content.WriteString(cliHeaderStyle.Render("ArgoCD Auto-Sync"))
	content.WriteString("\n\n")

	content.WriteString(fmt.Sprintf("  %s %s\n",
		cliLabelStyle.Render("Searched in:"),
		cliDimStyle.Render(strings.Join(searchNamespaces, ", "))))

	if len(apps) == 0 {
		content.WriteString(fmt.Sprintf("\n  %s %s",
			cliSuccessStyle.Render("✓"),
			cliDimStyle.Render("No applications with auto-sync found")))
	} else {
		content.WriteString(fmt.Sprintf("\n  %s %s\n",
			cliWarningStyle.Render("⚠"),
			fmt.Sprintf("Found %d app(s) with auto-sync:", len(apps))))

		for _, app := range apps {
			content.WriteString(fmt.Sprintf("    %s %s\n",
				cliDimStyle.Render("•"),
				cliValueStyle.Render(app)))
		}

		if isDryRun {
			content.WriteString(fmt.Sprintf("\n  %s",
				cliDimStyle.Render("[dry-run] Would disable auto-sync")))
		} else {
			content.WriteString(fmt.Sprintf("\n  %s %s",
				cliInfoStyle.Render("→"),
				"Auto-sync will be disabled during migration"))
		}
	}

	return cliBoxStyle.Render(content.String())
}

// buildWorkloadsBox creates a styled box for running workloads
func buildWorkloadsBox(workloadsByNS map[string][]string, isDryRun bool, mode string) string {
	var content strings.Builder

	content.WriteString(cliHeaderStyle.Render("Running Workloads"))
	content.WriteString("\n")

	totalWorkloads := 0
	for ns, workloads := range workloadsByNS {
		if len(workloads) == 0 {
			continue
		}
		totalWorkloads += len(workloads)

		content.WriteString(fmt.Sprintf("\n  %s %s\n",
			cliInfoStyle.Render("◆"),
			cliValueStyle.Render(ns)))

		for _, w := range workloads {
			content.WriteString(fmt.Sprintf("    %s %s\n",
				cliWarningStyle.Render("•"),
				cliValueStyle.Render(w)))
		}
	}

	if totalWorkloads == 0 {
		content.WriteString(fmt.Sprintf("\n  %s %s",
			cliSuccessStyle.Render("✓"),
			cliDimStyle.Render("No running workloads found")))
	} else {
		switch {
		case isDryRun:
			content.WriteString(fmt.Sprintf("\n  %s",
				cliDimStyle.Render(fmt.Sprintf("[dry-run] Would scale down %d workload(s)", totalWorkloads))))
		case mode == scaleModeManual:
			content.WriteString(fmt.Sprintf("\n  %s %s",
				cliWarningStyle.Render("⚠"),
				fmt.Sprintf("%d workload(s) need to be scaled down (manual mode)", totalWorkloads)))
		default:
			content.WriteString(fmt.Sprintf("\n  %s %s",
				cliInfoStyle.Render("→"),
				fmt.Sprintf("Scaling down %d workload(s)...", totalWorkloads)))
		}
	}

	return cliBoxStyle.Render(content.String())
}
