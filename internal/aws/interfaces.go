// Package aws provides AWS EC2 client functionality for EBS volume operations.
package aws

import (
	"context"
)

// EC2API defines the interface for EC2 operations used by the migrator.
// This interface enables mocking for unit tests.
type EC2API interface {
	// CreateSnapshot creates an EBS snapshot and returns the snapshot ID.
	CreateSnapshot(ctx context.Context, volumeID, pvcName, targetZone string) (string, error)

	// WaitForSnapshot waits for a snapshot to complete.
	WaitForSnapshot(ctx context.Context, snapshotID string) error

	// GetSnapshotProgress returns the progress (0-100) and state of a snapshot.
	GetSnapshotProgress(ctx context.Context, snapshotID string) (int, string, error)

	// CreateVolume creates a new EBS volume from a snapshot.
	CreateVolume(ctx context.Context, snapshotID, targetZone, pvcName, namespace string, sizeGiB int32) (string, error)

	// WaitForVolume waits for a volume to be available.
	WaitForVolume(ctx context.Context, volumeID string) error

	// GetVolumeState returns the state of a volume.
	GetVolumeState(ctx context.Context, volumeID string) (string, error)

	// GetVolumeInfo returns detailed information about a volume.
	GetVolumeInfo(ctx context.Context, volumeID string) (*VolumeInfo, error)
}

// Ensure Client implements EC2API
var _ EC2API = (*Client)(nil)
