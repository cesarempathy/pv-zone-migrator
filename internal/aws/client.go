package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Client wraps the AWS EC2 client
type Client struct {
	ec2 *ec2.Client
}

// NewEC2Client creates a new AWS EC2 client
func NewEC2Client(ctx context.Context) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &Client{ec2: ec2.NewFromConfig(cfg)}, nil
}

// CreateSnapshot creates an EBS snapshot
func (c *Client) CreateSnapshot(ctx context.Context, volumeID, pvcName, targetZone string) (string, error) {
	description := fmt.Sprintf("Migrate %s to %s", pvcName, targetZone)

	input := &ec2.CreateSnapshotInput{
		VolumeId:    aws.String(volumeID),
		Description: aws.String(description),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeSnapshot,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(fmt.Sprintf("migrate-%s", pvcName))},
					{Key: aws.String("MigratedPVC"), Value: aws.String(pvcName)},
				},
			},
		},
	}

	result, err := c.ec2.CreateSnapshot(ctx, input)
	if err != nil {
		return "", err
	}

	return *result.SnapshotId, nil
}

// WaitForSnapshot waits for a snapshot to complete
func (c *Client) WaitForSnapshot(ctx context.Context, snapshotID string) error {
	waiter := ec2.NewSnapshotCompletedWaiter(c.ec2)
	return waiter.Wait(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: []string{snapshotID},
	}, 30*time.Minute)
}

// GetSnapshotProgress returns the progress of a snapshot (0-100)
func (c *Client) GetSnapshotProgress(ctx context.Context, snapshotID string) (int, string, error) {
	result, err := c.ec2.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: []string{snapshotID},
	})
	if err != nil {
		return 0, "", err
	}

	if len(result.Snapshots) == 0 {
		return 0, "", fmt.Errorf("snapshot not found")
	}

	snapshot := result.Snapshots[0]
	progress := 0
	if snapshot.Progress != nil {
		fmt.Sscanf(*snapshot.Progress, "%d%%", &progress)
	}

	return progress, string(snapshot.State), nil
}

// CreateVolume creates a new EBS volume from a snapshot
func (c *Client) CreateVolume(ctx context.Context, snapshotID, targetZone, pvcName, namespace string, sizeGiB int32) (string, error) {
	input := &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String(targetZone),
		SnapshotId:       aws.String(snapshotID),
		VolumeType:       ec2types.VolumeTypeGp3,
		Size:             aws.Int32(sizeGiB),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeVolume,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(fmt.Sprintf("migrated-%s", pvcName))},
					{Key: aws.String("MigratedPVC"), Value: aws.String(pvcName)},
					{Key: aws.String("kubernetes.io/created-for/pvc/name"), Value: aws.String(pvcName)},
					{Key: aws.String("kubernetes.io/created-for/pvc/namespace"), Value: aws.String(namespace)},
				},
			},
		},
	}

	result, err := c.ec2.CreateVolume(ctx, input)
	if err != nil {
		return "", err
	}

	return *result.VolumeId, nil
}

// WaitForVolume waits for a volume to be available
func (c *Client) WaitForVolume(ctx context.Context, volumeID string) error {
	waiter := ec2.NewVolumeAvailableWaiter(c.ec2)
	return waiter.Wait(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volumeID},
	}, 10*time.Minute)
}

// GetVolumeState returns the state of a volume
func (c *Client) GetVolumeState(ctx context.Context, volumeID string) (string, error) {
	result, err := c.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volumeID},
	})
	if err != nil {
		return "", err
	}

	if len(result.Volumes) == 0 {
		return "", fmt.Errorf("volume not found")
	}

	return string(result.Volumes[0].State), nil
}

// VolumeInfo contains information about an EBS volume
type VolumeInfo struct {
	VolumeID         string
	AvailabilityZone string
	State            string
}

// GetVolumeInfo returns detailed information about a volume including its availability zone
func (c *Client) GetVolumeInfo(ctx context.Context, volumeID string) (*VolumeInfo, error) {
	result, err := c.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volumeID},
	})
	if err != nil {
		return nil, err
	}

	if len(result.Volumes) == 0 {
		return nil, fmt.Errorf("volume not found: %s", volumeID)
	}

	vol := result.Volumes[0]
	return &VolumeInfo{
		VolumeID:         aws.ToString(vol.VolumeId),
		AvailabilityZone: aws.ToString(vol.AvailabilityZone),
		State:            string(vol.State),
	}, nil
}
