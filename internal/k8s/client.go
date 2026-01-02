package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps the Kubernetes clientset
type Client struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
}

// PVCInfo contains information about a PVC and its backing volume
type PVCInfo struct {
	PVName     string
	VolumeID   string
	Capacity   string
	CapacityGi int32
}

// WorkloadInfo stores information about a scaled workload
type WorkloadInfo struct {
	Kind     string // "Deployment" or "StatefulSet"
	Name     string
	Replicas int32
}

// ArgoCDAppInfo stores information about an ArgoCD application
type ArgoCDAppInfo struct {
	Name           string
	Namespace      string
	AutoSyncPolicy json.RawMessage // Store the original automated policy for restoration
}

// NewClient creates a new Kubernetes client
// kubeContext is optional - if empty, uses the current context from kubeconfig
func NewClient(kubeContext string) (*Client, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}

	// Build config with optional context override
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	configOverrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		configOverrides.CurrentContext = kubeContext
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
	}, nil
}

// ListPVCs returns all PVC names in the given namespace
func (c *Client) ListPVCs(ctx context.Context, namespace string) ([]string, error) {
	pvcList, err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list PVCs in namespace %s: %w", namespace, err)
	}

	names := make([]string, 0, len(pvcList.Items))
	for _, pvc := range pvcList.Items {
		names = append(names, pvc.Name)
	}

	return names, nil
}

// GetPVCInfo retrieves information about a PVC and its backing PV
func (c *Client) GetPVCInfo(ctx context.Context, namespace, pvcName string) (*PVCInfo, error) {
	pvc, err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get PVC %s: %w", pvcName, err)
	}

	pvName := pvc.Spec.VolumeName
	if pvName == "" {
		return nil, fmt.Errorf("PVC %s is not bound to any PV", pvcName)
	}

	pv, err := c.clientset.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get PV %s: %w", pvName, err)
	}

	volumeID := ""
	if pv.Spec.CSI != nil && pv.Spec.CSI.VolumeHandle != "" {
		volumeID = pv.Spec.CSI.VolumeHandle
	} else if pv.Spec.AWSElasticBlockStore != nil && pv.Spec.AWSElasticBlockStore.VolumeID != "" {
		volumeID = pv.Spec.AWSElasticBlockStore.VolumeID
		if strings.Contains(volumeID, "/") {
			parts := strings.Split(volumeID, "/")
			volumeID = parts[len(parts)-1]
		}
	}

	if volumeID == "" {
		return nil, fmt.Errorf("could not find AWS Volume ID for PV %s", pvName)
	}

	capacity := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	capacityStr := capacity.String()
	capacityGi := int32(capacity.Value() / (1024 * 1024 * 1024))
	if capacityGi < 1 {
		capacityGi = 1
	}

	return &PVCInfo{
		PVName:     pvName,
		VolumeID:   volumeID,
		Capacity:   capacityStr,
		CapacityGi: capacityGi,
	}, nil
}

// CleanupResources removes old PVC and PV
func (c *Client) CleanupResources(ctx context.Context, namespace, pvcName, pvName string) error {
	pvc, err := c.clientset.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err == nil {
		if len(pvc.Finalizers) > 0 {
			pvc.Finalizers = nil
			_, _ = c.clientset.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, pvc, metav1.UpdateOptions{})
		}

		deletePolicy := metav1.DeletePropagationForeground
		gracePeriod := int64(0)
		_ = c.clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvcName, metav1.DeleteOptions{
			GracePeriodSeconds: &gracePeriod,
			PropagationPolicy:  &deletePolicy,
		})
	}

	pv, err := c.clientset.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
	if err == nil {
		if len(pv.Finalizers) > 0 {
			pv.Finalizers = nil
			_, _ = c.clientset.CoreV1().PersistentVolumes().Update(ctx, pv, metav1.UpdateOptions{})
		}

		gracePeriod := int64(0)
		_ = c.clientset.CoreV1().PersistentVolumes().Delete(ctx, pvName, metav1.DeleteOptions{
			GracePeriodSeconds: &gracePeriod,
		})
	}

	time.Sleep(2 * time.Second)
	return nil
}

// CreateStaticPV creates a new PersistentVolume bound to an AWS EBS volume
func (c *Client) CreateStaticPV(ctx context.Context, pvName, volumeID, capacity, storageClass, targetZone string) error {
	capacityQuantity, err := resource.ParseQuantity(capacity)
	if err != nil {
		return fmt.Errorf("failed to parse capacity %s: %w", capacity, err)
	}

	filesystemMode := corev1.PersistentVolumeFilesystem

	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
			Labels: map[string]string{
				"migrated": "true",
			},
		},
		Spec: corev1.PersistentVolumeSpec{
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: capacityQuantity,
			},
			VolumeMode:                    &filesystemMode,
			AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			StorageClassName:              storageClass,
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "ebs.csi.aws.com",
					FSType:       "ext4",
					VolumeHandle: volumeID,
				},
			},
			NodeAffinity: &corev1.VolumeNodeAffinity{
				Required: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "topology.kubernetes.io/zone",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{targetZone},
								},
							},
						},
					},
				},
			},
		},
	}

	_, err = c.clientset.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	return err
}

// CreateBoundPVC creates a new PVC bound to a specific PV
func (c *Client) CreateBoundPVC(ctx context.Context, namespace, pvcName, pvName, capacity, storageClass string) error {
	capacityQuantity, err := resource.ParseQuantity(capacity)
	if err != nil {
		return fmt.Errorf("failed to parse capacity %s: %w", capacity, err)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
			Labels: map[string]string{
				"migrated": "true",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: capacityQuantity,
				},
			},
			VolumeName: pvName,
		},
	}

	_, err = c.clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	return err
}

// ScaleDownWorkloads scales all Deployments and StatefulSets in the namespace to 0
// and returns their original replica counts for later restoration
func (c *Client) ScaleDownWorkloads(ctx context.Context, namespace string) ([]WorkloadInfo, error) {
	var workloads []WorkloadInfo

	// Scale down Deployments
	deployments, err := c.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}

	for _, deploy := range deployments.Items {
		if deploy.Spec.Replicas != nil && *deploy.Spec.Replicas > 0 {
			workloads = append(workloads, WorkloadInfo{
				Kind:     "Deployment",
				Name:     deploy.Name,
				Replicas: *deploy.Spec.Replicas,
			})

			// Scale to 0
			zero := int32(0)
			deploy.Spec.Replicas = &zero
			_, err := c.clientset.AppsV1().Deployments(namespace).Update(ctx, &deploy, metav1.UpdateOptions{})
			if err != nil {
				return workloads, fmt.Errorf("failed to scale deployment %s to 0: %w", deploy.Name, err)
			}
		}
	}

	// Scale down StatefulSets
	statefulsets, err := c.clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return workloads, fmt.Errorf("failed to list statefulsets: %w", err)
	}

	for _, sts := range statefulsets.Items {
		if sts.Spec.Replicas != nil && *sts.Spec.Replicas > 0 {
			workloads = append(workloads, WorkloadInfo{
				Kind:     "StatefulSet",
				Name:     sts.Name,
				Replicas: *sts.Spec.Replicas,
			})

			// Scale to 0
			zero := int32(0)
			sts.Spec.Replicas = &zero
			_, err := c.clientset.AppsV1().StatefulSets(namespace).Update(ctx, &sts, metav1.UpdateOptions{})
			if err != nil {
				return workloads, fmt.Errorf("failed to scale statefulset %s to 0: %w", sts.Name, err)
			}
		}
	}

	return workloads, nil
}

// WaitForWorkloadsScaledDown waits until all pods in the namespace are terminated
func (c *Client) WaitForWorkloadsScaledDown(ctx context.Context, namespace string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}

		runningPods := 0
		for _, pod := range pods.Items {
			if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending {
				runningPods++
			}
		}

		if runningPods == 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return fmt.Errorf("timeout waiting for pods to terminate")
}

// ScaleUpWorkloads restores workloads to their original replica counts
func (c *Client) ScaleUpWorkloads(ctx context.Context, namespace string, workloads []WorkloadInfo) error {
	for _, w := range workloads {
		switch w.Kind {
		case "Deployment":
			deploy, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, w.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get deployment %s: %w", w.Name, err)
			}
			deploy.Spec.Replicas = &w.Replicas
			_, err = c.clientset.AppsV1().Deployments(namespace).Update(ctx, deploy, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to scale deployment %s to %d: %w", w.Name, w.Replicas, err)
			}

		case "StatefulSet":
			sts, err := c.clientset.AppsV1().StatefulSets(namespace).Get(ctx, w.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get statefulset %s: %w", w.Name, err)
			}
			sts.Spec.Replicas = &w.Replicas
			_, err = c.clientset.AppsV1().StatefulSets(namespace).Update(ctx, sts, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to scale statefulset %s to %d: %w", w.Name, w.Replicas, err)
			}
		}
	}

	return nil
}

// GetWorkloadStatus returns a summary of running workloads in the namespace
func (c *Client) GetWorkloadStatus(ctx context.Context, namespace string) ([]WorkloadInfo, error) {
	var workloads []WorkloadInfo

	deployments, err := c.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}

	for _, deploy := range deployments.Items {
		if deploy.Spec.Replicas != nil && *deploy.Spec.Replicas > 0 {
			workloads = append(workloads, WorkloadInfo{
				Kind:     "Deployment",
				Name:     deploy.Name,
				Replicas: *deploy.Spec.Replicas,
			})
		}
	}

	statefulsets, err := c.clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list statefulsets: %w", err)
	}

	for _, sts := range statefulsets.Items {
		if sts.Spec.Replicas != nil && *sts.Spec.Replicas > 0 {
			workloads = append(workloads, WorkloadInfo{
				Kind:     "StatefulSet",
				Name:     sts.Name,
				Replicas: *sts.Spec.Replicas,
			})
		}
	}

	return workloads, nil
}

// argoCDAppGVR returns the GroupVersionResource for ArgoCD Applications
func argoCDAppGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "applications",
	}
}

// FindArgoCDAppsForNamespace finds ArgoCD applications targeting the given namespace
func (c *Client) FindArgoCDAppsForNamespace(ctx context.Context, targetNamespace string, argoCDNamespaces []string) ([]ArgoCDAppInfo, error) {
	var apps []ArgoCDAppInfo

	// Use provided namespaces or default
	if len(argoCDNamespaces) == 0 {
		argoCDNamespaces = []string{"argocd", "argo-cd", "gitops"}
	}

	for _, ns := range argoCDNamespaces {
		appList, err := c.dynamicClient.Resource(argoCDAppGVR()).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			// Namespace might not exist, skip
			continue
		}

		for _, app := range appList.Items {
			// Check if app targets our namespace
			destNS, found, err := unstructured.NestedString(app.Object, "spec", "destination", "namespace")
			if err != nil || !found {
				continue
			}

			if destNS == targetNamespace {
				// Check if auto-sync is enabled
				automated, found, _ := unstructured.NestedMap(app.Object, "spec", "syncPolicy", "automated")
				if found && automated != nil {
					// Store the automated policy for restoration
					automatedJSON, _ := json.Marshal(automated)
					apps = append(apps, ArgoCDAppInfo{
						Name:           app.GetName(),
						Namespace:      ns,
						AutoSyncPolicy: automatedJSON,
					})
				}
			}
		}
	}

	return apps, nil
}

// DisableArgoCDAutoSync disables auto-sync for the given ArgoCD applications
func (c *Client) DisableArgoCDAutoSync(ctx context.Context, apps []ArgoCDAppInfo) error {
	for _, appInfo := range apps {
		app, err := c.dynamicClient.Resource(argoCDAppGVR()).Namespace(appInfo.Namespace).Get(ctx, appInfo.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ArgoCD app %s/%s: %w", appInfo.Namespace, appInfo.Name, err)
		}

		// Remove the automated field from syncPolicy
		syncPolicy, found, _ := unstructured.NestedMap(app.Object, "spec", "syncPolicy")
		if found && syncPolicy != nil {
			delete(syncPolicy, "automated")
			if err := unstructured.SetNestedMap(app.Object, syncPolicy, "spec", "syncPolicy"); err != nil {
				return fmt.Errorf("failed to update syncPolicy for %s: %w", appInfo.Name, err)
			}
		}

		_, err = c.dynamicClient.Resource(argoCDAppGVR()).Namespace(appInfo.Namespace).Update(ctx, app, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to disable auto-sync for ArgoCD app %s/%s: %w", appInfo.Namespace, appInfo.Name, err)
		}
	}

	return nil
}

// EnableArgoCDAutoSync re-enables auto-sync for the given ArgoCD applications
func (c *Client) EnableArgoCDAutoSync(ctx context.Context, apps []ArgoCDAppInfo) error {
	for _, appInfo := range apps {
		app, err := c.dynamicClient.Resource(argoCDAppGVR()).Namespace(appInfo.Namespace).Get(ctx, appInfo.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get ArgoCD app %s/%s: %w", appInfo.Namespace, appInfo.Name, err)
		}

		// Restore the automated policy
		var automated map[string]interface{}
		if err := json.Unmarshal(appInfo.AutoSyncPolicy, &automated); err != nil {
			return fmt.Errorf("failed to unmarshal auto-sync policy for %s: %w", appInfo.Name, err)
		}

		// Get or create syncPolicy
		syncPolicy, _, _ := unstructured.NestedMap(app.Object, "spec", "syncPolicy")
		if syncPolicy == nil {
			syncPolicy = make(map[string]interface{})
		}
		syncPolicy["automated"] = automated

		if err := unstructured.SetNestedMap(app.Object, syncPolicy, "spec", "syncPolicy"); err != nil {
			return fmt.Errorf("failed to update syncPolicy for %s: %w", appInfo.Name, err)
		}

		_, err = c.dynamicClient.Resource(argoCDAppGVR()).Namespace(appInfo.Namespace).Update(ctx, app, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to enable auto-sync for ArgoCD app %s/%s: %w", appInfo.Namespace, appInfo.Name, err)
		}
	}

	return nil
}
