package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEC2API implements the ec2ClientAPI interface for testing
type mockEC2API struct {
	createSnapshotFunc    func(ctx context.Context, params *ec2.CreateSnapshotInput, optFns ...func(*ec2.Options)) (*ec2.CreateSnapshotOutput, error)
	describeSnapshotsFunc func(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
	createVolumeFunc      func(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error)
	describeVolumesFunc   func(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
}

func (m *mockEC2API) CreateSnapshot(ctx context.Context, params *ec2.CreateSnapshotInput, optFns ...func(*ec2.Options)) (*ec2.CreateSnapshotOutput, error) {
	if m.createSnapshotFunc != nil {
		return m.createSnapshotFunc(ctx, params, optFns...)
	}
	return nil, errors.New("CreateSnapshot not implemented")
}

func (m *mockEC2API) DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
	if m.describeSnapshotsFunc != nil {
		return m.describeSnapshotsFunc(ctx, params, optFns...)
	}
	return nil, errors.New("DescribeSnapshots not implemented")
}

func (m *mockEC2API) CreateVolume(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
	if m.createVolumeFunc != nil {
		return m.createVolumeFunc(ctx, params, optFns...)
	}
	return nil, errors.New("CreateVolume not implemented")
}

func (m *mockEC2API) DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	if m.describeVolumesFunc != nil {
		return m.describeVolumesFunc(ctx, params, optFns...)
	}
	return nil, errors.New("DescribeVolumes not implemented")
}

func TestClient_CreateSnapshot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		volumeID   string
		pvcName    string
		targetZone string
		mockSetup  func(m *mockEC2API)
		wantID     string
		wantErr    bool
	}{
		{
			name:       "success",
			volumeID:   "vol-123",
			pvcName:    "test-pvc",
			targetZone: "us-west-2a",
			mockSetup: func(m *mockEC2API) {
				m.createSnapshotFunc = func(_ context.Context, params *ec2.CreateSnapshotInput, _ ...func(*ec2.Options)) (*ec2.CreateSnapshotOutput, error) {
					// Verify inputs
					assert.Equal(t, "vol-123", *params.VolumeId)
					assert.Contains(t, *params.Description, "test-pvc")
					return &ec2.CreateSnapshotOutput{
						SnapshotId: aws.String("snap-abc123"),
					}, nil
				}
			},
			wantID:  "snap-abc123",
			wantErr: false,
		},
		{
			name:       "api_error",
			volumeID:   "vol-error",
			pvcName:    "error-pvc",
			targetZone: "us-west-2a",
			mockSetup: func(m *mockEC2API) {
				m.createSnapshotFunc = func(_ context.Context, _ *ec2.CreateSnapshotInput, _ ...func(*ec2.Options)) (*ec2.CreateSnapshotOutput, error) {
					return nil, errors.New("AWS API error")
				}
			},
			wantID:  "",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock := &mockEC2API{}
			tc.mockSetup(mock)
			client := NewEC2ClientWithInterface(mock)
			ctx := context.Background()

			snapshotID, err := client.CreateSnapshot(ctx, tc.volumeID, tc.pvcName, tc.targetZone)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantID, snapshotID)
		})
	}
}

func TestClient_GetSnapshotProgress(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		snapshotID   string
		mockSetup    func(m *mockEC2API)
		wantProgress int
		wantState    string
		wantErr      bool
	}{
		{
			name:       "completed_snapshot",
			snapshotID: "snap-completed",
			mockSetup: func(m *mockEC2API) {
				m.describeSnapshotsFunc = func(_ context.Context, _ *ec2.DescribeSnapshotsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
					return &ec2.DescribeSnapshotsOutput{
						Snapshots: []ec2types.Snapshot{
							{
								SnapshotId: aws.String("snap-completed"),
								Progress:   aws.String("100%"),
								State:      ec2types.SnapshotStateCompleted,
							},
						},
					}, nil
				}
			},
			wantProgress: 100,
			wantState:    "completed",
			wantErr:      false,
		},
		{
			name:       "in_progress_snapshot",
			snapshotID: "snap-progress",
			mockSetup: func(m *mockEC2API) {
				m.describeSnapshotsFunc = func(_ context.Context, _ *ec2.DescribeSnapshotsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
					return &ec2.DescribeSnapshotsOutput{
						Snapshots: []ec2types.Snapshot{
							{
								SnapshotId: aws.String("snap-progress"),
								Progress:   aws.String("50%"),
								State:      ec2types.SnapshotStatePending,
							},
						},
					}, nil
				}
			},
			wantProgress: 50,
			wantState:    "pending",
			wantErr:      false,
		},
		{
			name:       "snapshot_not_found",
			snapshotID: "snap-notfound",
			mockSetup: func(m *mockEC2API) {
				m.describeSnapshotsFunc = func(_ context.Context, _ *ec2.DescribeSnapshotsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
					return &ec2.DescribeSnapshotsOutput{
						Snapshots: []ec2types.Snapshot{},
					}, nil
				}
			},
			wantProgress: 0,
			wantState:    "",
			wantErr:      true,
		},
		{
			name:       "api_error",
			snapshotID: "snap-error",
			mockSetup: func(m *mockEC2API) {
				m.describeSnapshotsFunc = func(_ context.Context, _ *ec2.DescribeSnapshotsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
					return nil, errors.New("AWS API error")
				}
			},
			wantProgress: 0,
			wantState:    "",
			wantErr:      true,
		},
		{
			name:       "nil_progress",
			snapshotID: "snap-nil",
			mockSetup: func(m *mockEC2API) {
				m.describeSnapshotsFunc = func(_ context.Context, _ *ec2.DescribeSnapshotsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
					return &ec2.DescribeSnapshotsOutput{
						Snapshots: []ec2types.Snapshot{
							{
								SnapshotId: aws.String("snap-nil"),
								Progress:   nil,
								State:      ec2types.SnapshotStatePending,
							},
						},
					}, nil
				}
			},
			wantProgress: 0,
			wantState:    "pending",
			wantErr:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock := &mockEC2API{}
			tc.mockSetup(mock)
			client := NewEC2ClientWithInterface(mock)
			ctx := context.Background()

			progress, state, err := client.GetSnapshotProgress(ctx, tc.snapshotID)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantProgress, progress)
			assert.Equal(t, tc.wantState, state)
		})
	}
}

func TestClient_CreateVolume(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		snapshotID string
		targetZone string
		pvcName    string
		namespace  string
		sizeGiB    int32
		mockSetup  func(m *mockEC2API)
		wantID     string
		wantErr    bool
	}{
		{
			name:       "success",
			snapshotID: "snap-123",
			targetZone: "us-west-2a",
			pvcName:    "my-pvc",
			namespace:  "default",
			sizeGiB:    100,
			mockSetup: func(m *mockEC2API) {
				m.createVolumeFunc = func(_ context.Context, params *ec2.CreateVolumeInput, _ ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
					assert.Equal(t, "snap-123", *params.SnapshotId)
					assert.Equal(t, "us-west-2a", *params.AvailabilityZone)
					assert.Equal(t, int32(100), *params.Size)
					return &ec2.CreateVolumeOutput{
						VolumeId: aws.String("vol-newvol"),
					}, nil
				}
			},
			wantID:  "vol-newvol",
			wantErr: false,
		},
		{
			name:       "api_error",
			snapshotID: "snap-error",
			targetZone: "us-west-2a",
			pvcName:    "my-pvc",
			namespace:  "default",
			sizeGiB:    50,
			mockSetup: func(m *mockEC2API) {
				m.createVolumeFunc = func(_ context.Context, _ *ec2.CreateVolumeInput, _ ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
					return nil, errors.New("AWS API error")
				}
			},
			wantID:  "",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock := &mockEC2API{}
			tc.mockSetup(mock)
			client := NewEC2ClientWithInterface(mock)
			ctx := context.Background()

			volumeID, err := client.CreateVolume(ctx, tc.snapshotID, tc.targetZone, tc.pvcName, tc.namespace, tc.sizeGiB)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantID, volumeID)
		})
	}
}

func TestClient_GetVolumeState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		volumeID  string
		mockSetup func(m *mockEC2API)
		wantState string
		wantErr   bool
	}{
		{
			name:     "available_volume",
			volumeID: "vol-available",
			mockSetup: func(m *mockEC2API) {
				m.describeVolumesFunc = func(_ context.Context, _ *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
					return &ec2.DescribeVolumesOutput{
						Volumes: []ec2types.Volume{
							{
								VolumeId: aws.String("vol-available"),
								State:    ec2types.VolumeStateAvailable,
							},
						},
					}, nil
				}
			},
			wantState: "available",
			wantErr:   false,
		},
		{
			name:     "creating_volume",
			volumeID: "vol-creating",
			mockSetup: func(m *mockEC2API) {
				m.describeVolumesFunc = func(_ context.Context, _ *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
					return &ec2.DescribeVolumesOutput{
						Volumes: []ec2types.Volume{
							{
								VolumeId: aws.String("vol-creating"),
								State:    ec2types.VolumeStateCreating,
							},
						},
					}, nil
				}
			},
			wantState: "creating",
			wantErr:   false,
		},
		{
			name:     "volume_not_found",
			volumeID: "vol-notfound",
			mockSetup: func(m *mockEC2API) {
				m.describeVolumesFunc = func(_ context.Context, _ *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
					return &ec2.DescribeVolumesOutput{
						Volumes: []ec2types.Volume{},
					}, nil
				}
			},
			wantState: "",
			wantErr:   true,
		},
		{
			name:     "api_error",
			volumeID: "vol-error",
			mockSetup: func(m *mockEC2API) {
				m.describeVolumesFunc = func(_ context.Context, _ *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
					return nil, errors.New("AWS API error")
				}
			},
			wantState: "",
			wantErr:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock := &mockEC2API{}
			tc.mockSetup(mock)
			client := NewEC2ClientWithInterface(mock)
			ctx := context.Background()

			state, err := client.GetVolumeState(ctx, tc.volumeID)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantState, state)
		})
	}
}

func TestClient_GetVolumeInfo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		volumeID  string
		mockSetup func(m *mockEC2API)
		wantInfo  *VolumeInfo
		wantErr   bool
	}{
		{
			name:     "success",
			volumeID: "vol-123",
			mockSetup: func(m *mockEC2API) {
				m.describeVolumesFunc = func(_ context.Context, _ *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
					return &ec2.DescribeVolumesOutput{
						Volumes: []ec2types.Volume{
							{
								VolumeId:         aws.String("vol-123"),
								AvailabilityZone: aws.String("us-west-2a"),
								State:            ec2types.VolumeStateAvailable,
							},
						},
					}, nil
				}
			},
			wantInfo: &VolumeInfo{
				VolumeID:         "vol-123",
				AvailabilityZone: "us-west-2a",
				State:            "available",
			},
			wantErr: false,
		},
		{
			name:     "volume_not_found",
			volumeID: "vol-notfound",
			mockSetup: func(m *mockEC2API) {
				m.describeVolumesFunc = func(_ context.Context, _ *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
					return &ec2.DescribeVolumesOutput{
						Volumes: []ec2types.Volume{},
					}, nil
				}
			},
			wantInfo: nil,
			wantErr:  true,
		},
		{
			name:     "api_error",
			volumeID: "vol-error",
			mockSetup: func(m *mockEC2API) {
				m.describeVolumesFunc = func(_ context.Context, _ *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
					return nil, errors.New("AWS API error")
				}
			},
			wantInfo: nil,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock := &mockEC2API{}
			tc.mockSetup(mock)
			client := NewEC2ClientWithInterface(mock)
			ctx := context.Background()

			info, err := client.GetVolumeInfo(ctx, tc.volumeID)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantInfo, info)
		})
	}
}

func TestVolumeInfo_Struct(t *testing.T) {
	t.Parallel()

	info := &VolumeInfo{
		VolumeID:         "vol-test",
		AvailabilityZone: "us-west-2a",
		State:            "available",
	}

	assert.Equal(t, "vol-test", info.VolumeID)
	assert.Equal(t, "us-west-2a", info.AvailabilityZone)
	assert.Equal(t, "available", info.State)
}
