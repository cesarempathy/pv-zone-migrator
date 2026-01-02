// Package migrator implements the core PVC migration logic.
// It orchestrates the migration process including snapshots, volumes, and Kubernetes resources.
package migrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cesarempathy/pv-zone-migrator/internal/aws"
	"github.com/cesarempathy/pv-zone-migrator/internal/k8s"
)

// Config holds the migration configuration
type Config struct {
	Namespaces     []string
	TargetZone     string
	StorageClass   string
	MaxConcurrency int
	PVCList        []string // Format: "namespace/pvcname"
	DryRun         bool
}

// Step represents a migration step
type Step int

// Migration step constants representing the state of a PVC migration.
const (
	StepPending Step = iota
	StepGetInfo
	StepSkipped // PVC already in target zone
	StepSnapshot
	StepWaitSnapshot
	StepCreateVolume
	StepWaitVolume
	StepCleanup
	StepCreatePV
	StepCreatePVC
	StepDone
	StepFailed
)

func (s Step) String() string {
	names := []string{
		"Pending",
		"Getting Info",
		"Skipped",
		"Creating Snapshot",
		"Snapshot Progress",
		"Creating Volume",
		"Volume Creating",
		"Cleaning Up",
		"Creating PV",
		"Creating PVC",
		"Completed",
		"Failed",
	}
	if int(s) < len(names) {
		return names[s]
	}
	return "Unknown"
}

// PVCStatus represents the current status of a PVC migration
type PVCStatus struct {
	Name        string // Full name in format "namespace/pvcname"
	Namespace   string
	PVCName     string // Just the PVC name without namespace
	Step        Step
	Progress    int
	Error       error
	StartTime   time.Time
	EndTime     time.Time
	SnapshotID  string
	NewVolumeID string
	OldVolumeID string
	PVName      string
	Capacity    string
	CurrentZone string // Current availability zone of the volume
}

// ParsePVCName parses a "namespace/pvcname" string into its components
func ParsePVCName(fullName string) (namespace, pvcName string) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "default", fullName
}

// PlanAction represents what will happen to a PVC
type PlanAction int

// Plan action constants representing what will happen to a PVC.
const (
	PlanActionMigrate PlanAction = iota
	PlanActionSkip
	PlanActionError
)

func (a PlanAction) String() string {
	switch a {
	case PlanActionMigrate:
		return "Migrate"
	case PlanActionSkip:
		return "Skip"
	case PlanActionError:
		return "Error"
	default:
		return "Unknown"
	}
}

// PVCPlanItem represents a single PVC in the migration plan
type PVCPlanItem struct {
	Name        string // Full name "namespace/pvcname"
	Namespace   string
	PVCName     string
	PVName      string
	VolumeID    string
	Capacity    string
	CurrentZone string
	TargetZone  string
	Action      PlanAction
	Reason      string // Reason for skip or error
}

// MigrationPlan holds the complete migration plan
type MigrationPlan struct {
	Items        []PVCPlanItem
	TargetZone   string
	StorageClass string
	DryRun       bool
	Namespaces   []string
	Concurrency  int
}

// Migrator handles PVC migrations
type Migrator struct {
	config    *Config
	k8sClient *k8s.Client
	awsClient *aws.Client
	statuses  map[string]*PVCStatus
	mu        sync.RWMutex
	done      bool
}

// New creates a new Migrator
func New(config *Config, k8sClient *k8s.Client, awsClient *aws.Client) *Migrator {
	statuses := make(map[string]*PVCStatus)
	for _, pvc := range config.PVCList {
		ns, name := ParsePVCName(pvc)
		statuses[pvc] = &PVCStatus{
			Name:      pvc,
			Namespace: ns,
			PVCName:   name,
			Step:      StepPending,
		}
	}

	return &Migrator{
		config:    config,
		k8sClient: k8sClient,
		awsClient: awsClient,
		statuses:  statuses,
	}
}

// GetConfig returns the migration config
func (m *Migrator) GetConfig() *Config {
	return m.config
}

// GetStatuses returns a copy of all PVC statuses
func (m *Migrator) GetStatuses() map[string]*PVCStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*PVCStatus)
	for k, v := range m.statuses {
		copyStatus := *v
		result[k] = &copyStatus
	}
	return result
}

// IsDone returns true if all migrations are complete
func (m *Migrator) IsDone() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.done
}

func (m *Migrator) updateStatus(pvcName string, step Step, progress int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.statuses[pvcName]; ok {
		s.Step = step
		s.Progress = progress
		if err != nil {
			s.Error = err
			s.Step = StepFailed
			s.EndTime = time.Now()
		}
		if step == StepDone {
			s.EndTime = time.Now()
		}
	}
}

// Run starts the migration process
func (m *Migrator) Run(ctx context.Context) {
	semaphore := make(chan struct{}, m.config.MaxConcurrency)
	var wg sync.WaitGroup

	for _, pvcName := range m.config.PVCList {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			m.migratePVC(ctx, name)
		}(pvcName)
	}

	wg.Wait()

	m.mu.Lock()
	m.done = true
	m.mu.Unlock()
}

func (m *Migrator) migratePVC(ctx context.Context, pvcName string) {
	m.mu.Lock()
	status := m.statuses[pvcName]
	status.StartTime = time.Now()
	namespace := status.Namespace
	shortName := status.PVCName
	m.mu.Unlock()

	// Step 1: Get PVC Info
	m.updateStatus(pvcName, StepGetInfo, 0, nil)
	info, err := m.k8sClient.GetPVCInfo(ctx, namespace, shortName)
	if err != nil {
		m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("get info: %w", err))
		return
	}

	m.mu.Lock()
	m.statuses[pvcName].OldVolumeID = info.VolumeID
	m.statuses[pvcName].PVName = info.PVName
	m.statuses[pvcName].Capacity = info.Capacity
	m.mu.Unlock()

	// Check if the volume is already in the target zone
	volumeInfo, err := m.awsClient.GetVolumeInfo(ctx, info.VolumeID)
	if err != nil {
		m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("get volume info: %w", err))
		return
	}

	m.mu.Lock()
	m.statuses[pvcName].CurrentZone = volumeInfo.AvailabilityZone
	m.mu.Unlock()

	// Skip migration if already in target zone
	if volumeInfo.AvailabilityZone == m.config.TargetZone {
		m.updateStatus(pvcName, StepSkipped, 100, nil)
		m.mu.Lock()
		m.statuses[pvcName].EndTime = time.Now()
		m.mu.Unlock()
		return
	}

	if m.config.DryRun {
		m.updateStatus(pvcName, StepDone, 100, nil)
		return
	}

	// Step 2: Create Snapshot
	m.updateStatus(pvcName, StepSnapshot, 0, nil)
	snapshotID, err := m.awsClient.CreateSnapshot(ctx, info.VolumeID, shortName, m.config.TargetZone)
	if err != nil {
		m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("create snapshot: %w", err))
		return
	}

	m.mu.Lock()
	m.statuses[pvcName].SnapshotID = snapshotID
	m.mu.Unlock()

	// Step 3: Wait for Snapshot with progress
	m.updateStatus(pvcName, StepWaitSnapshot, 0, nil)
	for {
		progress, state, err := m.awsClient.GetSnapshotProgress(ctx, snapshotID)
		if err != nil {
			m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("get snapshot progress: %w", err))
			return
		}

		m.updateStatus(pvcName, StepWaitSnapshot, progress, nil)

		if state == "completed" {
			break
		}
		if state == "error" {
			m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("snapshot failed"))
			return
		}

		select {
		case <-ctx.Done():
			m.updateStatus(pvcName, StepFailed, 0, ctx.Err())
			return
		case <-time.After(5 * time.Second):
		}
	}

	// Step 4: Create Volume
	m.updateStatus(pvcName, StepCreateVolume, 0, nil)
	newVolumeID, err := m.awsClient.CreateVolume(ctx, snapshotID, m.config.TargetZone, shortName, namespace, info.CapacityGi)
	if err != nil {
		m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("create volume: %w", err))
		return
	}

	m.mu.Lock()
	m.statuses[pvcName].NewVolumeID = newVolumeID
	m.mu.Unlock()

	// Step 5: Wait for Volume
	m.updateStatus(pvcName, StepWaitVolume, 0, nil)
	for {
		state, err := m.awsClient.GetVolumeState(ctx, newVolumeID)
		if err != nil {
			m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("get volume state: %w", err))
			return
		}

		if state == "available" {
			m.updateStatus(pvcName, StepWaitVolume, 100, nil)
			break
		}
		if state == "error" {
			m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("volume creation failed"))
			return
		}

		progress := 50
		if state == "creating" {
			progress = 25
		}
		m.updateStatus(pvcName, StepWaitVolume, progress, nil)

		select {
		case <-ctx.Done():
			m.updateStatus(pvcName, StepFailed, 0, ctx.Err())
			return
		case <-time.After(3 * time.Second):
		}
	}

	// Step 6: Create PV
	m.updateStatus(pvcName, StepCreatePV, 0, nil)
	newPVName := shortName + "-static"
	if err := m.k8sClient.CreateStaticPV(ctx, newPVName, newVolumeID, info.Capacity, m.config.StorageClass, m.config.TargetZone); err != nil {
		m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("create PV: %w", err))
		return
	}

	// Step 7: Cleanup
	// We do cleanup AFTER creating the new PV to minimize the risk of data loss/orphaned volumes
	// if the process crashes.
	m.updateStatus(pvcName, StepCleanup, 0, nil)
	if err := m.k8sClient.CleanupResources(ctx, namespace, shortName, info.PVName); err != nil {
		// If cleanup fails, we still have the new PV created, but the old one might still exist.
		// This is a partial failure but better than data loss.
		m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("cleanup: %w", err))
		return
	}

	// Step 8: Create PVC
	m.updateStatus(pvcName, StepCreatePVC, 0, nil)
	if err := m.k8sClient.CreateBoundPVC(ctx, namespace, shortName, newPVName, info.Capacity, m.config.StorageClass); err != nil {
		m.updateStatus(pvcName, StepFailed, 0, fmt.Errorf("create PVC: %w", err))
		return
	}

	m.updateStatus(pvcName, StepDone, 100, nil)
}

// GeneratePlan creates a migration plan by fetching volume info for all PVCs
func (m *Migrator) GeneratePlan(ctx context.Context) (*MigrationPlan, error) {
	plan := &MigrationPlan{
		Items:        make([]PVCPlanItem, 0, len(m.config.PVCList)),
		TargetZone:   m.config.TargetZone,
		StorageClass: m.config.StorageClass,
		DryRun:       m.config.DryRun,
		Namespaces:   m.config.Namespaces,
		Concurrency:  m.config.MaxConcurrency,
	}

	for _, pvcName := range m.config.PVCList {
		ns, shortName := ParsePVCName(pvcName)
		item := PVCPlanItem{
			Name:       pvcName,
			Namespace:  ns,
			PVCName:    shortName,
			TargetZone: m.config.TargetZone,
		}

		// Get PVC info from Kubernetes
		info, err := m.k8sClient.GetPVCInfo(ctx, ns, shortName)
		if err != nil {
			item.Action = PlanActionError
			item.Reason = fmt.Sprintf("Failed to get PVC info: %v", err)
			plan.Items = append(plan.Items, item)
			continue
		}

		item.PVName = info.PVName
		item.VolumeID = info.VolumeID
		item.Capacity = info.Capacity

		// Get volume info from AWS
		volumeInfo, err := m.awsClient.GetVolumeInfo(ctx, info.VolumeID)
		if err != nil {
			item.Action = PlanActionError
			item.Reason = fmt.Sprintf("Failed to get volume info: %v", err)
			plan.Items = append(plan.Items, item)
			continue
		}

		item.CurrentZone = volumeInfo.AvailabilityZone

		// Determine action
		if volumeInfo.AvailabilityZone == m.config.TargetZone {
			item.Action = PlanActionSkip
			item.Reason = "Already in target zone"
		} else {
			item.Action = PlanActionMigrate
		}

		plan.Items = append(plan.Items, item)
	}

	return plan, nil
}
