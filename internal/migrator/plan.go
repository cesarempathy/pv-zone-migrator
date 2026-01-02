package migrator

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Plan formatting styles
var (
	planTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("99")).
			MarginBottom(1)

	planHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("75"))

	planBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("99")).
			Padding(0, 1)

	planMigrateStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("42"))

	planSkipStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))

	planErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	planDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	planInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75"))

	planWarningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214"))

	planTableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("99")).
				PaddingRight(2)
)

// FormatPlan renders the migration plan as a colored string
func FormatPlan(plan *MigrationPlan) string {
	var b strings.Builder

	// Title
	b.WriteString("\n")
	b.WriteString(planTitleStyle.Render("═══════════════════════════════════════════════════════════════════════════"))
	b.WriteString("\n")
	b.WriteString(planTitleStyle.Render("                              MIGRATION PLAN"))
	b.WriteString("\n")
	b.WriteString(planTitleStyle.Render("═══════════════════════════════════════════════════════════════════════════"))
	b.WriteString("\n\n")

	// Configuration section
	b.WriteString(planHeaderStyle.Render("Configuration:"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s %s\n", planInfoStyle.Render("Target Zone:"), plan.TargetZone))
	b.WriteString(fmt.Sprintf("  %s %s\n", planInfoStyle.Render("Storage Class:"), plan.StorageClass))
	b.WriteString(fmt.Sprintf("  %s %s\n", planInfoStyle.Render("Namespaces:"), strings.Join(plan.Namespaces, ", ")))
	b.WriteString(fmt.Sprintf("  %s %d\n", planInfoStyle.Render("Concurrency:"), plan.Concurrency))
	if plan.DryRun {
		b.WriteString(fmt.Sprintf("  %s\n", planWarningStyle.Render("⚠️  DRY RUN MODE - No changes will be made")))
	}
	b.WriteString("\n")

	// Count actions
	migrateCount := 0
	skipCount := 0
	errorCount := 0
	for _, item := range plan.Items {
		switch item.Action {
		case PlanActionMigrate:
			migrateCount++
		case PlanActionSkip:
			skipCount++
		case PlanActionError:
			errorCount++
		}
	}

	// Summary
	b.WriteString(planHeaderStyle.Render(fmt.Sprintf("PVCs to Process (%d):", len(plan.Items))))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s  %s  %s\n",
		planMigrateStyle.Render(fmt.Sprintf("✓ Migrate: %d", migrateCount)),
		planSkipStyle.Render(fmt.Sprintf("○ Skip: %d", skipCount)),
		planErrorStyle.Render(fmt.Sprintf("✗ Error: %d", errorCount)),
	))
	b.WriteString("\n")

	// Table header
	tableContent := renderPlanTable(plan)
	b.WriteString(planBoxStyle.Render(tableContent))
	b.WriteString("\n\n")

	// Actions summary
	if migrateCount > 0 {
		b.WriteString(planHeaderStyle.Render("Actions to be performed:"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  %s Create EBS snapshots for %d volume(s)\n", planDimStyle.Render("1."), migrateCount))
		b.WriteString(fmt.Sprintf("  %s Create new volumes in %s\n", planDimStyle.Render("2."), plan.TargetZone))
		b.WriteString(fmt.Sprintf("  %s Delete old PVCs and PVs\n", planDimStyle.Render("3.")))
		b.WriteString(fmt.Sprintf("  %s Create new static PVs and bound PVCs\n", planDimStyle.Render("4.")))
		b.WriteString("\n")
	}

	return b.String()
}

func renderPlanTable(plan *MigrationPlan) string {
	var b strings.Builder

	// Calculate column widths
	pvcColWidth := 40
	zoneColWidth := 14
	actionColWidth := 25

	// Header
	b.WriteString(planTableHeaderStyle.Render(padRight("PVC", pvcColWidth)))
	b.WriteString(planTableHeaderStyle.Render(padRight("Current Zone", zoneColWidth)))
	b.WriteString(planTableHeaderStyle.Render(padRight("Action", actionColWidth)))
	b.WriteString("\n")

	// Separator
	b.WriteString(planDimStyle.Render(strings.Repeat("─", pvcColWidth+zoneColWidth+actionColWidth)))
	b.WriteString("\n")

	// Rows
	for _, item := range plan.Items {
		// PVC name
		pvcName := truncatePlan(item.Name, pvcColWidth-2)
		b.WriteString(padRight(pvcName, pvcColWidth))

		// Current zone
		zoneStr := item.CurrentZone
		if zoneStr == "" {
			zoneStr = "N/A"
		}
		b.WriteString(padRight(zoneStr, zoneColWidth))

		// Action with icon
		switch item.Action {
		case PlanActionMigrate:
			actionStr := fmt.Sprintf("✓ Will migrate → %s", item.TargetZone)
			b.WriteString(planMigrateStyle.Render(actionStr))
		case PlanActionSkip:
			b.WriteString(planSkipStyle.Render("○ Skip (same AZ)"))
		case PlanActionError:
			errStr := truncatePlan(item.Reason, actionColWidth-4)
			b.WriteString(planErrorStyle.Render(fmt.Sprintf("✗ %s", errStr)))
		}

		b.WriteString("\n")

		// Show capacity and volume ID on second line for migrate items
		if item.Action == PlanActionMigrate && item.VolumeID != "" {
			b.WriteString(planDimStyle.Render(fmt.Sprintf("  └─ %s, Volume: %s", item.Capacity, truncatePlan(item.VolumeID, 25))))
			b.WriteString("\n")
		}
	}

	return b.String()
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

func truncatePlan(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
