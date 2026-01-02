// Package aws provides AWS EC2 client functionality for EBS volume operations.
// It handles snapshot creation, volume creation, and state monitoring.
package aws

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ec2ClientAPI is the internal interface for EC2 SDK operations
type ec2ClientAPI interface {
	CreateSnapshot(ctx context.Context, params *ec2.CreateSnapshotInput, optFns ...func(*ec2.Options)) (*ec2.CreateSnapshotOutput, error)
	DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
	CreateVolume(ctx context.Context, params *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error)
	DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
}

// Client wraps the AWS EC2 client
type Client struct {
	ec2 ec2ClientAPI
}

// NewEC2Client creates a new AWS EC2 client
func NewEC2Client(ctx context.Context) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &Client{ec2: ec2.NewFromConfig(cfg)}, nil
}

// NewEC2ClientWithInterface creates a Client with a custom EC2 API implementation (for testing)
func NewEC2ClientWithInterface(api ec2ClientAPI) *Client {
	return &Client{ec2: api}
}

// SanitizeTag cleans input strings to be safe for AWS Tags.
// Allowed characters: Alphanumeric, spaces, and _ . : / = + - @
func SanitizeTag(input string) string {
	// Regex to match allowed characters
	re := regexp.MustCompile(`[^a-zA-Z0-9\s_.:/=+\-@]`)
	return re.ReplaceAllString(input, "_")
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
					{Key: aws.String("Name"), Value: aws.String(fmt.Sprintf("migrate-%s", SanitizeTag(pvcName)))},
					{Key: aws.String("MigratedPVC"), Value: aws.String(SanitizeTag(pvcName))},
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
		_, _ = fmt.Sscanf(*snapshot.Progress, "%d%%", &progress)
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
					{Key: aws.String("Name"), Value: aws.String(fmt.Sprintf("migrated-%s", SanitizeTag(pvcName)))},
					{Key: aws.String("MigratedPVC"), Value: aws.String(SanitizeTag(pvcName))},
					{Key: aws.String("kubernetes.io/created-for/pvc/name"), Value: aws.String(SanitizeTag(pvcName))},
					{Key: aws.String("kubernetes.io/created-for/pvc/namespace"), Value: aws.String(SanitizeTag(namespace))},
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
