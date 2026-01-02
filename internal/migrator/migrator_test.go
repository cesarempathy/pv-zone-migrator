package migrator

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePVCName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		input         string
		wantNamespace string
		wantPVCName   string
	}{
		{
			name:          "with_namespace",
			input:         "my-namespace/my-pvc",
			wantNamespace: "my-namespace",
			wantPVCName:   "my-pvc",
		},
		{
			name:          "without_namespace",
			input:         "my-pvc",
			wantNamespace: "default",
			wantPVCName:   "my-pvc",
		},
		{
			name:          "multiple_slashes",
			input:         "ns/pvc/extra",
			wantNamespace: "ns",
			wantPVCName:   "pvc/extra",
		},
		{
			name:          "empty_string",
			input:         "",
			wantNamespace: "default",
			wantPVCName:   "",
		},
		{
			name:          "only_slash",
			input:         "/pvc",
			wantNamespace: "",
			wantPVCName:   "pvc",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ns, pvcName := ParsePVCName(tc.input)

			assert.Equal(t, tc.wantNamespace, ns)
			assert.Equal(t, tc.wantPVCName, pvcName)
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		config    *Config
		wantCount int
		wantPVCs  []string
	}{
		{
			name: "single_pvc",
			config: &Config{
				Namespaces:     []string{"default"},
				TargetZone:     "us-west-2a",
				StorageClass:   "gp3",
				MaxConcurrency: 5,
				PVCList:        []string{"default/my-pvc"},
			},
			wantCount: 1,
			wantPVCs:  []string{"default/my-pvc"},
		},
		{
			name: "multiple_pvcs",
			config: &Config{
				Namespaces:     []string{"ns1", "ns2"},
				TargetZone:     "us-west-2a",
				StorageClass:   "gp3",
				MaxConcurrency: 3,
				PVCList:        []string{"ns1/pvc-a", "ns2/pvc-b", "ns1/pvc-c"},
			},
			wantCount: 3,
			wantPVCs:  []string{"ns1/pvc-a", "ns2/pvc-b", "ns1/pvc-c"},
		},
		{
			name: "empty_pvc_list",
			config: &Config{
				Namespaces:     []string{"default"},
				TargetZone:     "us-west-2a",
				StorageClass:   "gp3",
				MaxConcurrency: 5,
				PVCList:        []string{},
			},
			wantCount: 0,
			wantPVCs:  []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := New(tc.config, nil, nil)

			require.NotNil(t, m)
			assert.Equal(t, tc.config, m.GetConfig())
			assert.Equal(t, tc.wantCount, len(m.GetStatuses()))

			for _, pvc := range tc.wantPVCs {
				status, exists := m.GetStatuses()[pvc]
				assert.True(t, exists, "expected status for %s", pvc)
				assert.Equal(t, StepPending, status.Step)
			}
		})
	}
}

func TestGetStatuses(t *testing.T) {
	t.Parallel()

	config := &Config{
		PVCList: []string{"ns/pvc-1", "ns/pvc-2"},
	}
	m := New(config, nil, nil)

	statuses := m.GetStatuses()

	// Verify it's a copy (modifying returned map shouldn't affect original)
	delete(statuses, "ns/pvc-1")
	assert.Len(t, m.GetStatuses(), 2)
}

func TestIsDone(t *testing.T) {
	t.Parallel()

	config := &Config{
		PVCList: []string{"ns/pvc-1"},
	}
	m := New(config, nil, nil)

	assert.False(t, m.IsDone())

	// Manually set done
	m.mu.Lock()
	m.done = true
	m.mu.Unlock()

	assert.True(t, m.IsDone())
}

func TestStep_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		step Step
		want string
	}{
		{StepPending, "Pending"},
		{StepGetInfo, "Getting Info"},
		{StepSkipped, "Skipped"},
		{StepSnapshot, "Creating Snapshot"},
		{StepWaitSnapshot, "Snapshot Progress"},
		{StepCreateVolume, "Creating Volume"},
		{StepWaitVolume, "Volume Creating"},
		{StepCleanup, "Cleaning Up"},
		{StepCreatePV, "Creating PV"},
		{StepCreatePVC, "Creating PVC"},
		{StepDone, "Completed"},
		{StepFailed, "Failed"},
		{Step(100), "Unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.step.String())
		})
	}
}

func TestPlanAction_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		action PlanAction
		want   string
	}{
		{PlanActionMigrate, "Migrate"},
		{PlanActionSkip, "Skip"},
		{PlanActionError, "Error"},
		{PlanAction(100), "Unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.action.String())
		})
	}
}

func TestMigrationPlan_Fields(t *testing.T) {
	t.Parallel()

	plan := &MigrationPlan{
		Items: []PVCPlanItem{
			{Name: "ns/pvc-1", Action: PlanActionMigrate},
		},
		TargetZone:   "us-west-2a",
		StorageClass: "gp3",
		DryRun:       true,
		Namespaces:   []string{"ns"},
		Concurrency:  5,
	}

	assert.Len(t, plan.Items, 1)
	assert.Equal(t, "us-west-2a", plan.TargetZone)
	assert.Equal(t, "gp3", plan.StorageClass)
	assert.True(t, plan.DryRun)
	assert.Equal(t, []string{"ns"}, plan.Namespaces)
	assert.Equal(t, 5, plan.Concurrency)
}

func TestPVCPlanItem_Fields(t *testing.T) {
	t.Parallel()

	item := PVCPlanItem{
		Name:        "namespace/my-pvc",
		Namespace:   "namespace",
		PVCName:     "my-pvc",
		PVName:      "my-pv",
		VolumeID:    "vol-123",
		Capacity:    "50Gi",
		CurrentZone: "us-west-2b",
		TargetZone:  "us-west-2a",
		Action:      PlanActionMigrate,
		Reason:      "",
	}

	assert.Equal(t, "namespace/my-pvc", item.Name)
	assert.Equal(t, "namespace", item.Namespace)
	assert.Equal(t, "my-pvc", item.PVCName)
	assert.Equal(t, "my-pv", item.PVName)
	assert.Equal(t, "vol-123", item.VolumeID)
	assert.Equal(t, "50Gi", item.Capacity)
	assert.Equal(t, "us-west-2b", item.CurrentZone)
	assert.Equal(t, "us-west-2a", item.TargetZone)
	assert.Equal(t, PlanActionMigrate, item.Action)
}

func TestPVCStatus_Fields(t *testing.T) {
	t.Parallel()

	now := time.Now()
	status := &PVCStatus{
		Name:        "ns/pvc-test",
		Namespace:   "ns",
		PVCName:     "pvc-test",
		Step:        StepDone,
		Progress:    100,
		Error:       nil,
		StartTime:   now,
		EndTime:     now.Add(5 * time.Minute),
		SnapshotID:  "snap-123",
		NewVolumeID: "vol-new",
		OldVolumeID: "vol-old",
		PVName:      "pv-test",
		Capacity:    "20Gi",
		CurrentZone: "us-west-2b",
	}

	assert.Equal(t, "ns/pvc-test", status.Name)
	assert.Equal(t, "ns", status.Namespace)
	assert.Equal(t, "pvc-test", status.PVCName)
	assert.Equal(t, StepDone, status.Step)
	assert.Equal(t, 100, status.Progress)
	assert.Nil(t, status.Error)
	assert.Equal(t, now, status.StartTime)
	assert.Equal(t, "snap-123", status.SnapshotID)
	assert.Equal(t, "vol-new", status.NewVolumeID)
	assert.Equal(t, "vol-old", status.OldVolumeID)
}

func TestConfig_Fields(t *testing.T) {
	t.Parallel()

	config := &Config{
		Namespaces:     []string{"ns1", "ns2"},
		TargetZone:     "eu-west-1a",
		StorageClass:   "gp2",
		MaxConcurrency: 10,
		PVCList:        []string{"ns1/pvc-1", "ns2/pvc-2"},
		DryRun:         true,
	}

	assert.Equal(t, []string{"ns1", "ns2"}, config.Namespaces)
	assert.Equal(t, "eu-west-1a", config.TargetZone)
	assert.Equal(t, "gp2", config.StorageClass)
	assert.Equal(t, 10, config.MaxConcurrency)
	assert.Equal(t, []string{"ns1/pvc-1", "ns2/pvc-2"}, config.PVCList)
	assert.True(t, config.DryRun)
}

func TestMigrator_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	config := &Config{
		PVCList: []string{"ns/pvc-1", "ns/pvc-2", "ns/pvc-3"},
	}
	m := New(config, nil, nil)

	// Concurrent reads and writes
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = m.GetStatuses()
		}()
		go func() {
			defer wg.Done()
			_ = m.IsDone()
		}()
	}
	wg.Wait()
}
