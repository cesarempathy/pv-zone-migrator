package migrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatPlan(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		plan         *MigrationPlan
		wantContains []string
	}{
		{
			name: "plan_with_migrate_items",
			plan: &MigrationPlan{
				Items: []PVCPlanItem{
					{
						Name:        "ns/pvc-1",
						Action:      PlanActionMigrate,
						CurrentZone: "us-west-2b",
						TargetZone:  "us-west-2a",
						Capacity:    "100Gi",
					},
				},
				TargetZone:   "us-west-2a",
				StorageClass: "gp3",
				DryRun:       false,
				Concurrency:  5,
			},
			wantContains: []string{"ns/pvc-1", "Will migrate", "us-west-2a"},
		},
		{
			name: "plan_with_skip_items",
			plan: &MigrationPlan{
				Items: []PVCPlanItem{
					{
						Name:        "ns/pvc-skip",
						Action:      PlanActionSkip,
						CurrentZone: "us-west-2a",
						TargetZone:  "us-west-2a",
						Reason:      "Already in target zone",
					},
				},
				TargetZone:   "us-west-2a",
				StorageClass: "gp3",
			},
			wantContains: []string{"Skip", "same AZ"},
		},
		{
			name: "plan_with_error_items",
			plan: &MigrationPlan{
				Items: []PVCPlanItem{
					{
						Name:   "ns/pvc-error",
						Action: PlanActionError,
						Reason: "PVC not found",
					},
				},
				TargetZone: "us-west-2a",
			},
			wantContains: []string{"Error", "PVC not found"},
		},
		{
			name: "dry_run_plan",
			plan: &MigrationPlan{
				Items: []PVCPlanItem{
					{Name: "ns/pvc-1", Action: PlanActionMigrate},
				},
				TargetZone: "us-west-2a",
				DryRun:     true,
			},
			wantContains: []string{"DRY RUN"},
		},
		{
			name: "empty_plan",
			plan: &MigrationPlan{
				Items:      []PVCPlanItem{},
				TargetZone: "us-west-2a",
			},
			wantContains: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := FormatPlan(tc.plan)

			for _, want := range tc.wantContains {
				assert.Contains(t, result, want)
			}
		})
	}
}

func TestFormatPlan_MultipleItems(t *testing.T) {
	t.Parallel()

	plan := &MigrationPlan{
		Items: []PVCPlanItem{
			{Name: "ns1/pvc-1", Action: PlanActionMigrate, CurrentZone: "us-west-2b", TargetZone: "us-west-2a"},
			{Name: "ns1/pvc-2", Action: PlanActionSkip, Reason: "Same zone"},
			{Name: "ns2/pvc-3", Action: PlanActionError, Reason: "Not found"},
		},
		TargetZone:   "us-west-2a",
		StorageClass: "gp3",
		Concurrency:  3,
	}

	result := FormatPlan(plan)

	// Should contain all items
	assert.Contains(t, result, "ns1/pvc-1")
	assert.Contains(t, result, "ns1/pvc-2")
	assert.Contains(t, result, "ns2/pvc-3")

	// Should contain all actions
	assert.Contains(t, result, "Will migrate")
	assert.Contains(t, result, "Skip")
	assert.Contains(t, result, "✗")
}

func TestPadRight(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		input  string
		length int
		want   string
	}{
		{
			name:   "shorter_than_length",
			input:  "test",
			length: 10,
			want:   "test      ",
		},
		{
			name:   "equal_to_length",
			input:  "test",
			length: 4,
			want:   "test",
		},
		{
			name:   "longer_than_length",
			input:  "testing",
			length: 4,
			want:   "test", // padRight truncates when len(s) >= width
		},
		{
			name:   "empty_string",
			input:  "",
			length: 5,
			want:   "     ",
		},
		{
			name:   "zero_length",
			input:  "test",
			length: 0,
			want:   "", // padRight returns s[:0] when width is 0
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Call the actual package function
			result := padRight(tc.input, tc.length)

			assert.Equal(t, tc.want, result)
		})
	}
}

func TestTruncatePlan(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "shorter_than_max",
			input:  "short",
			maxLen: 10,
			want:   "short",
		},
		{
			name:   "equal_to_max",
			input:  "exact",
			maxLen: 5,
			want:   "exact",
		},
		{
			name:   "longer_than_max",
			input:  "this is a long string",
			maxLen: 10,
			want:   "this is...",
		},
		{
			name:   "empty_string",
			input:  "",
			maxLen: 5,
			want:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Call the actual package function
			result := truncatePlan(tc.input, tc.maxLen)

			assert.Equal(t, tc.want, result)
		})
	}
}

func TestRenderPlanTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		plan         *MigrationPlan
		wantContains []string
	}{
		{
			name: "render_migrate_item",
			plan: &MigrationPlan{
				Items: []PVCPlanItem{
					{
						Name:        "default/my-pvc",
						PVName:      "my-pv",
						VolumeID:    "vol-123",
						Capacity:    "100Gi",
						CurrentZone: "us-west-2b",
						TargetZone:  "us-west-2a",
						Action:      PlanActionMigrate,
					},
				},
				TargetZone:   "us-west-2a",
				StorageClass: "gp3",
			},
			wantContains: []string{"default/my-pvc", "vol-123", "100Gi", "Will migrate"},
		},
		{
			name: "render_skip_item",
			plan: &MigrationPlan{
				Items: []PVCPlanItem{
					{
						Name:   "ns/skip-pvc",
						Action: PlanActionSkip,
						Reason: "Already in target zone",
					},
				},
				TargetZone: "us-west-2a",
			},
			wantContains: []string{"ns/skip-pvc", "Skip (same AZ)"},
		},
		{
			name: "empty_items",
			plan: &MigrationPlan{
				Items:      []PVCPlanItem{},
				TargetZone: "us-west-2a",
			},
			wantContains: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Call the actual package function
			result := renderPlanTable(tc.plan)

			for _, want := range tc.wantContains {
				assert.Contains(t, result, want)
			}
		})
	}
}
