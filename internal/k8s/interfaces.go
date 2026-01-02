// Package k8s provides Kubernetes client functionality for PVC/PV operations.
package k8s

import (
	"context"
	"time"
)

// API defines the interface for Kubernetes operations used by the migrator.
// This interface enables mocking for unit tests.
type API interface {
	// ListPVCs returns all PVC names in the given namespace.
	ListPVCs(ctx context.Context, namespace string) ([]string, error)

	// GetPVCInfo retrieves information about a PVC and its backing PV.
	GetPVCInfo(ctx context.Context, namespace, pvcName string) (*PVCInfo, error)

	// CleanupResources removes old PVC and PV.
	CleanupResources(ctx context.Context, namespace, pvcName, pvName string) error

	// CreateStaticPV creates a new PersistentVolume bound to an AWS EBS volume.
	CreateStaticPV(ctx context.Context, pvName, volumeID, capacity, storageClass, targetZone string) error

	// CreateBoundPVC creates a new PVC bound to a specific PV.
	CreateBoundPVC(ctx context.Context, namespace, pvcName, pvName, capacity, storageClass string) error

	// ScaleDownWorkloads scales all Deployments and StatefulSets in the namespace to 0.
	ScaleDownWorkloads(ctx context.Context, namespace string) ([]WorkloadInfo, error)

	// WaitForWorkloadsScaledDown waits until all pods in the namespace are terminated.
	WaitForWorkloadsScaledDown(ctx context.Context, namespace string, timeout time.Duration) error

	// ScaleUpWorkloads restores workloads to their original replica counts.
	ScaleUpWorkloads(ctx context.Context, namespace string, workloads []WorkloadInfo) error

	// GetWorkloadStatus returns a summary of running workloads in the namespace.
	GetWorkloadStatus(ctx context.Context, namespace string) ([]WorkloadInfo, error)

	// FindArgoCDAppsForNamespace finds ArgoCD applications targeting the given namespace.
	FindArgoCDAppsForNamespace(ctx context.Context, targetNamespace string, argoCDNamespaces []string) ([]ArgoCDAppInfo, error)

	// DisableArgoCDAutoSync disables auto-sync for the given ArgoCD applications.
	DisableArgoCDAutoSync(ctx context.Context, apps []ArgoCDAppInfo) error

	// EnableArgoCDAutoSync re-enables auto-sync for the given ArgoCD applications.
	EnableArgoCDAutoSync(ctx context.Context, apps []ArgoCDAppInfo) error
}

// Ensure Client implements API
var _ API = (*Client)(nil)
