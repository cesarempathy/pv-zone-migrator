package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cesarempathy/pv-zone-migrator/internal/migrator"
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			MarginBottom(1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("99"))

	pvcNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86")).
			Width(45)

	stepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Width(20)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("75"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("99")).
			Padding(1, 2)
)

type tickMsg time.Time
type startMsg struct{}
type doneMsg struct{}
type planReadyMsg struct {
	plan *migrator.MigrationPlan
	err  error
}

// Model is the Bubble Tea model
type Model struct {
	migrator       *migrator.Migrator
	config         *migrator.Config
	spinner        spinner.Model
	progressBars   map[string]progress.Model
	started        bool
	confirmed      bool
	quitting       bool
	ctx            context.Context
	cancel         context.CancelFunc
	generatingPlan bool
	plan           *migrator.MigrationPlan
	planError      error
}

// NewModel creates a new UI model
func NewModel(m *migrator.Migrator, config *migrator.Config) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	progressBars := make(map[string]progress.Model)
	for _, pvc := range config.PVCList {
		p := progress.New(
			progress.WithDefaultGradient(),
			progress.WithWidth(30),
			progress.WithoutPercentage(),
		)
		progressBars[pvc] = p
	}

	ctx, cancel := context.WithCancel(context.Background())

	return Model{
		migrator:       m,
		config:         config,
		spinner:        s,
		progressBars:   progressBars,
		ctx:            ctx,
		cancel:         cancel,
		generatingPlan: true, // Start by generating the plan
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.tickCmd(), m.generatePlanCmd())
}

func (m Model) generatePlanCmd() tea.Cmd {
	return func() tea.Msg {
		plan, err := m.migrator.GeneratePlan(m.ctx)
		return planReadyMsg{plan: plan, err: err}
	}
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			m.cancel()
			return m, tea.Quit
		case "enter", "y":
			if !m.confirmed && !m.generatingPlan && m.planError == nil {
				m.confirmed = true
				return m, m.startMigration()
			}
		case "n":
			if !m.confirmed {
				m.quitting = true
				return m, tea.Quit
			}
		}

	case tea.WindowSizeMsg:
		return m, nil

	case planReadyMsg:
		m.generatingPlan = false
		m.plan = msg.plan
		m.planError = msg.err
		return m, m.tickCmd()

	case startMsg:
		m.started = true
		return m, m.tickCmd()

	case doneMsg:
		return m, tea.Quit

	case tickMsg:
		if m.started && m.migrator.IsDone() {
			return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
				return doneMsg{}
			})
		}

		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, tea.Batch(cmd, m.tickCmd())

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) startMigration() tea.Cmd {
	return func() tea.Msg {
		go m.migrator.Run(m.ctx)
		return startMsg{}
	}
}

// View renders the UI
func (m Model) View() string {
	if m.quitting {
		return "\n  👋 Migration cancelled.\n\n"
	}

	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(titleStyle.Render("  🚀 PVC Migration Tool"))
	b.WriteString("\n\n")

	// Show loading state while generating plan
	if m.generatingPlan {
		b.WriteString("  ")
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(infoStyle.Render("Generating migration plan..."))
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render("  Fetching volume information from AWS..."))
		b.WriteString("\n\n")
		return b.String()
	}

	// Show error if plan generation failed
	if m.planError != nil {
		b.WriteString(errorStyle.Render("  ✗ Failed to generate plan: "))
		b.WriteString(errorStyle.Render(m.planError.Error()))
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render("  Press q to exit"))
		b.WriteString("\n\n")
		return b.String()
	}

	// Show plan before confirmation
	if !m.confirmed && m.plan != nil {
		b.WriteString(migrator.FormatPlan(m.plan))

		b.WriteString(warningStyle.Render("  ⚠️  WARNING: Ensure all deployments/statefulsets are SCALED TO 0"))
		b.WriteString("\n\n")
		b.WriteString("  Press ")
		b.WriteString(headerStyle.Render("Enter"))
		b.WriteString(" or ")
		b.WriteString(headerStyle.Render("y"))
		b.WriteString(" to start, ")
		b.WriteString(headerStyle.Render("n"))
		b.WriteString(" or ")
		b.WriteString(headerStyle.Render("q"))
		b.WriteString(" to cancel\n\n")
		return b.String()
	}

	// Config box (shown during migration)
	namespacesStr := strings.Join(m.config.Namespaces, ", ")
	configContent := fmt.Sprintf(
		"%s %s\n%s %s\n%s %s\n%s %d\n%s %d",
		infoStyle.Render("Namespaces:"),
		namespacesStr,
		infoStyle.Render("Target Zone:"),
		m.config.TargetZone,
		infoStyle.Render("Storage Class:"),
		m.config.StorageClass,
		infoStyle.Render("Concurrency:"),
		m.config.MaxConcurrency,
		infoStyle.Render("PVCs to migrate:"),
		len(m.config.PVCList),
	)

	if m.config.DryRun {
		configContent += "\n" + warningStyle.Render("⚠️  DRY RUN MODE - No changes will be made")
	}

	b.WriteString(boxStyle.Render(configContent))
	b.WriteString("\n\n")

	b.WriteString(headerStyle.Render("  Migration Progress:"))
	b.WriteString("\n\n")

	statuses := m.migrator.GetStatuses()

	pvcNames := make([]string, 0, len(statuses))
	for name := range statuses {
		pvcNames = append(pvcNames, name)
	}
	sort.Strings(pvcNames)

	for _, name := range pvcNames {
		status := statuses[name]
		b.WriteString(m.renderPVCStatus(status))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if !m.migrator.IsDone() {
		b.WriteString(dimStyle.Render("  Press q or Ctrl+C to cancel"))
	} else {
		b.WriteString(successStyle.Render("  ✅ Migration complete! Press q to exit"))
	}
	b.WriteString("\n\n")

	return b.String()
}

func (m Model) renderPVCStatus(status *migrator.PVCStatus) string {
	var b strings.Builder

	b.WriteString("  ")
	b.WriteString(pvcNameStyle.Render(truncate(status.Name, 43)))
	b.WriteString(" ")

	switch status.Step {
	case migrator.StepPending:
		b.WriteString(dimStyle.Render("○"))
		b.WriteString(" ")
		b.WriteString(stepStyle.Render("Pending"))

	case migrator.StepDone:
		b.WriteString(successStyle.Render("✓"))
		b.WriteString(" ")
		b.WriteString(successStyle.Render("Completed"))
		if !status.EndTime.IsZero() && !status.StartTime.IsZero() {
			duration := status.EndTime.Sub(status.StartTime).Round(time.Second)
			b.WriteString(dimStyle.Render(fmt.Sprintf(" (%s)", duration)))
		}

	case migrator.StepSkipped:
		b.WriteString(warningStyle.Render("○"))
		b.WriteString(" ")
		b.WriteString(warningStyle.Render("Skipped"))
		b.WriteString(dimStyle.Render(" (already in target zone)"))

	case migrator.StepFailed:
		b.WriteString(errorStyle.Render("✗"))
		b.WriteString(" ")
		b.WriteString(errorStyle.Render("Failed"))
		if status.Error != nil {
			b.WriteString(dimStyle.Render(fmt.Sprintf(" - %s", truncate(status.Error.Error(), 40))))
		}

	default:
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(stepStyle.Render(status.Step.String()))
		b.WriteString(" ")

		if status.Step == migrator.StepWaitSnapshot && status.Progress > 0 {
			if p, ok := m.progressBars[status.Name]; ok {
				b.WriteString(p.ViewAs(float64(status.Progress) / 100.0))
				b.WriteString(dimStyle.Render(fmt.Sprintf(" %d%%", status.Progress)))
			}
		} else if status.Step == migrator.StepWaitVolume && status.Progress > 0 {
			if p, ok := m.progressBars[status.Name]; ok {
				b.WriteString(p.ViewAs(float64(status.Progress) / 100.0))
			}
		}
	}

	return b.String()
}

// HasErrors returns true if any migration failed
func (m Model) HasErrors() bool {
	statuses := m.migrator.GetStatuses()
	for _, s := range statuses {
		if s.Step == migrator.StepFailed {
			return true
		}
	}
	return false
}

// PrintSummary prints a summary after the TUI exits
func (m Model) PrintSummary() {
	if m.quitting && !m.started {
		return
	}

	statuses := m.migrator.GetStatuses()

	fmt.Println()
	fmt.Println(headerStyle.Render("═══════════════════════════════════════════════════════════════"))
	fmt.Println(headerStyle.Render("                      MIGRATION SUMMARY"))
	fmt.Println(headerStyle.Render("═══════════════════════════════════════════════════════════════"))
	fmt.Println()

	successCount := 0
	failedCount := 0
	skippedCount := 0

	pvcNames := make([]string, 0, len(statuses))
	for name := range statuses {
		pvcNames = append(pvcNames, name)
	}
	sort.Strings(pvcNames)

	for _, name := range pvcNames {
		s := statuses[name]
		if s.Step == migrator.StepDone {
			successCount++
			duration := ""
			if !s.EndTime.IsZero() && !s.StartTime.IsZero() {
				duration = fmt.Sprintf(" (%s)", s.EndTime.Sub(s.StartTime).Round(time.Second))
			}
			fmt.Printf("  %s %s%s\n", successStyle.Render("✓"), s.Name, dimStyle.Render(duration))
			if s.NewVolumeID != "" {
				fmt.Printf("    %s %s\n", dimStyle.Render("New Volume:"), s.NewVolumeID)
			}
		} else if s.Step == migrator.StepSkipped {
			skippedCount++
			fmt.Printf("  %s %s %s\n", warningStyle.Render("○"), s.Name, dimStyle.Render("(already in target zone)"))
		} else if s.Step == migrator.StepFailed {
			failedCount++
			fmt.Printf("  %s %s\n", errorStyle.Render("✗"), s.Name)
			if s.Error != nil {
				fmt.Printf("    %s %s\n", errorStyle.Render("Error:"), s.Error.Error())
			}
		} else {
			fmt.Printf("  %s %s (Incomplete)\n", warningStyle.Render("○"), s.Name)
		}
	}

	fmt.Println()
	fmt.Println(headerStyle.Render("═══════════════════════════════════════════════════════════════"))
	fmt.Printf("  Total: %d | ", len(statuses))
	fmt.Printf("%s | ", successStyle.Render(fmt.Sprintf("Success: %d", successCount)))
	fmt.Printf("%s | ", warningStyle.Render(fmt.Sprintf("Skipped: %d", skippedCount)))
	fmt.Printf("%s\n", errorStyle.Render(fmt.Sprintf("Failed: %d", failedCount)))
	fmt.Println(headerStyle.Render("═══════════════════════════════════════════════════════════════"))

	if failedCount > 0 {
		fmt.Println()
		fmt.Println(warningStyle.Render("  ⚠️  Some migrations failed. Please check the errors above."))
	} else if successCount > 0 {
		fmt.Println()
		fmt.Println(successStyle.Render("  🎉 All migrations completed successfully!"))
		fmt.Printf("  %s\n", infoStyle.Render(fmt.Sprintf("Next step: Ensure your workloads can schedule pods in %s", m.config.TargetZone)))
	}
	fmt.Println()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
