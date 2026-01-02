package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"pvc-migrator/internal/aws"
	"pvc-migrator/internal/k8s"
	"pvc-migrator/internal/migrator"
	"pvc-migrator/internal/ui"
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

func runMigrate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Print header info
	if configFile != "" {
		fmt.Printf("%s %s\n", cliDimStyle.Render("📄 Config:"), configFile)
	}
	if kubeContext != "" {
		fmt.Printf("%s %s\n", cliDimStyle.Render("☸  Context:"), kubeContext)
	}

	// Initialize Kubernetes client with optional context
	k8sClient, err := k8s.NewClient(kubeContext)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Build PVC list with namespace info
	type pvcWithNamespace struct {
		Namespace string
		Name      string
	}
	var allPVCs []pvcWithNamespace
	pvcsByNamespace := make(map[string][]string)

	// Process each namespace from config (silently collect data)
	for _, nsCfg := range cfg.Namespaces {
		if len(nsCfg.PVCs) > 0 {
			// Use specified PVCs for this namespace
			for _, pvc := range nsCfg.PVCs {
				allPVCs = append(allPVCs, pvcWithNamespace{Namespace: nsCfg.Name, Name: pvc})
			}
			pvcsByNamespace[nsCfg.Name] = nsCfg.PVCs
		} else {
			// Discover all PVCs in this namespace
			discovered, err := k8sClient.ListPVCs(ctx, nsCfg.Name)
			if err != nil {
				return fmt.Errorf("failed to list PVCs in namespace '%s': %w", nsCfg.Name, err)
			}
			pvcsByNamespace[nsCfg.Name] = discovered
			if len(discovered) == 0 {
				continue
			}
			for _, pvc := range discovered {
				allPVCs = append(allPVCs, pvcWithNamespace{Namespace: nsCfg.Name, Name: pvc})
			}
		}
	}

	if len(allPVCs) == 0 {
		return fmt.Errorf("no PVCs found in any of the specified namespaces")
	}

	// Print PVC Discovery box
	fmt.Println(buildDiscoveryBox(pvcsByNamespace, len(allPVCs)))

	// Check for ArgoCD applications managing these namespaces
	var argoCDApps []k8s.ArgoCDAppInfo
	var argoCDAppNames []string
	if !skipArgoCD {
		for _, ns := range namespaces {
			apps, err := k8sClient.FindArgoCDAppsForNamespace(ctx, ns, argoCDNamespaces)
			if err != nil {
				continue
			}
			argoCDApps = append(argoCDApps, apps...)
		}
		for _, app := range argoCDApps {
			argoCDAppNames = append(argoCDAppNames, fmt.Sprintf("%s/%s", app.Namespace, app.Name))
		}

		// Print ArgoCD box
		fmt.Println(buildArgoCDBox(argoCDAppNames, argoCDNamespaces, dryRun))

		if len(argoCDApps) > 0 && !dryRun {
			if err := k8sClient.DisableArgoCDAutoSync(ctx, argoCDApps); err != nil {
				return fmt.Errorf("failed to disable ArgoCD auto-sync: %w", err)
			}
		}
	}

	// Check for running workloads and scale them down in all namespaces
	type scaledWorkloadsPerNS struct {
		Namespace string
		Workloads []k8s.WorkloadInfo
	}
	var allScaledWorkloads []scaledWorkloadsPerNS

	// Collect workload info for all namespaces first
	workloadsByNS := make(map[string][]string)
	workloadInfoByNS := make(map[string][]k8s.WorkloadInfo)

	for _, ns := range namespaces {
		runningWorkloads, err := k8sClient.GetWorkloadStatus(ctx, ns)
		if err != nil {
			// Restore ArgoCD if we disabled it
			if len(argoCDApps) > 0 && !dryRun {
				_ = k8sClient.EnableArgoCDAutoSync(ctx, argoCDApps)
			}
			return fmt.Errorf("failed to check workload status in namespace '%s': %w", ns, err)
		}
		workloadInfoByNS[ns] = runningWorkloads
		for _, w := range runningWorkloads {
			workloadsByNS[ns] = append(workloadsByNS[ns], fmt.Sprintf("%s/%s (replicas: %d)", w.Kind, w.Name, w.Replicas))
		}
	}

	// Print workloads box
	fmt.Println(buildWorkloadsBox(workloadsByNS, dryRun))

	// Scale down workloads if not dry-run
	for _, ns := range namespaces {
		runningWorkloads := workloadInfoByNS[ns]
		if len(runningWorkloads) > 0 && !dryRun {
			scaledWorkloads, err := k8sClient.ScaleDownWorkloads(ctx, ns)
			if err != nil {
				// Restore ArgoCD if we disabled it
				if len(argoCDApps) > 0 {
					_ = k8sClient.EnableArgoCDAutoSync(ctx, argoCDApps)
				}
				// Restore already scaled workloads
				for _, sw := range allScaledWorkloads {
					_ = k8sClient.ScaleUpWorkloads(ctx, sw.Namespace, sw.Workloads)
				}
				return fmt.Errorf("failed to scale down workloads in namespace '%s': %w", ns, err)
			}
			allScaledWorkloads = append(allScaledWorkloads, scaledWorkloadsPerNS{Namespace: ns, Workloads: scaledWorkloads})

			if err := k8sClient.WaitForWorkloadsScaledDown(ctx, ns, 5*time.Minute); err != nil {
				// Try to restore workloads and ArgoCD
				for _, sw := range allScaledWorkloads {
					_ = k8sClient.ScaleUpWorkloads(ctx, sw.Namespace, sw.Workloads)
				}
				if len(argoCDApps) > 0 {
					_ = k8sClient.EnableArgoCDAutoSync(ctx, argoCDApps)
				}
				return fmt.Errorf("failed waiting for pods to terminate in namespace '%s': %w", ns, err)
			}
		}
	}

	// Initialize AWS EC2 client
	ec2Client, err := aws.NewEC2Client(ctx)
	if err != nil {
		// Restore workloads before returning error
		for _, sw := range allScaledWorkloads {
			fmt.Printf("⚠️  Restoring workloads in namespace '%s' due to error...\n", sw.Namespace)
			_ = k8sClient.ScaleUpWorkloads(ctx, sw.Namespace, sw.Workloads)
		}
		// Restore ArgoCD auto-sync
		if len(argoCDApps) > 0 && !dryRun {
			_ = k8sClient.EnableArgoCDAutoSync(ctx, argoCDApps)
		}
		return fmt.Errorf("failed to create AWS EC2 client: %w", err)
	}

	// Build PVC list with namespace prefix for the migrator
	var pvcListWithNS []string
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

	// Create the migrator
	m := migrator.New(config, k8sClient, ec2Client)

	// Handle --plan flag: generate and display plan, then exit
	if planOnly {
		fmt.Println("\n🔍 Generating migration plan...")

		// Show spinner while generating plan
		s := spinner.New()
		s.Spinner = spinner.Dot
		s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

		plan, err := m.GeneratePlan(ctx)
		if err != nil {
			return fmt.Errorf("failed to generate plan: %w", err)
		}

		// Print the formatted plan
		fmt.Print(migrator.FormatPlan(plan))

		// Print confirmation hint
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(
			"Run without --plan flag to execute the migration."))
		fmt.Println()

		return nil
	}

	// Create and run the Bubble Tea UI
	model := ui.NewModel(m, config)
	p := tea.NewProgram(model, tea.WithAltScreen())

	finalModel, err := p.Run()
	if err != nil {
		// Restore workloads before returning error
		for _, sw := range allScaledWorkloads {
			fmt.Printf("⚠️  Restoring workloads in namespace '%s' due to error...\n", sw.Namespace)
			_ = k8sClient.ScaleUpWorkloads(ctx, sw.Namespace, sw.Workloads)
		}
		// Restore ArgoCD auto-sync
		if len(argoCDApps) > 0 && !dryRun {
			_ = k8sClient.EnableArgoCDAutoSync(ctx, argoCDApps)
		}
		return fmt.Errorf("UI error: %w", err)
	}

	// Print final summary
	if fm, ok := finalModel.(ui.Model); ok {
		fm.PrintSummary()
	}

	// Scale workloads back up if we scaled them down
	if len(allScaledWorkloads) > 0 && !dryRun {
		fmt.Println("\n🚀 Restoring workloads to original replica counts...")
		for _, sw := range allScaledWorkloads {
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

	// Re-enable ArgoCD auto-sync
	if len(argoCDApps) > 0 && !dryRun {
		fmt.Println("\n🔓 Re-enabling ArgoCD auto-sync...")
		for _, app := range argoCDApps {
			fmt.Printf("   - %s/%s\n", app.Namespace, app.Name)
		}
		if err := k8sClient.EnableArgoCDAutoSync(ctx, argoCDApps); err != nil {
			fmt.Printf("⚠️  Warning: Failed to re-enable ArgoCD auto-sync: %v\n", err)
			fmt.Println("   Please manually re-enable auto-sync in ArgoCD")
		} else {
			fmt.Println("   ✅ Auto-sync re-enabled")
		}
	}

	// Check if we should exit with error
	if fm, ok := finalModel.(ui.Model); ok && fm.HasErrors() {
		os.Exit(1)
	}

	return nil
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
func buildWorkloadsBox(workloadsByNS map[string][]string, isDryRun bool) string {
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
		if isDryRun {
			content.WriteString(fmt.Sprintf("\n  %s",
				cliDimStyle.Render(fmt.Sprintf("[dry-run] Would scale down %d workload(s)", totalWorkloads))))
		} else {
			content.WriteString(fmt.Sprintf("\n  %s %s",
				cliInfoStyle.Render("→"),
				fmt.Sprintf("Scaling down %d workload(s)...", totalWorkloads)))
		}
	}

	return cliBoxStyle.Render(content.String())
}
