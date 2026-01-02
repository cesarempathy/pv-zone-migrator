package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// Workload kind constants
const (
	kindDeployment  = "Deployment"
	kindStatefulSet = "StatefulSet"
)

// newTestClient creates a new test client with a fake clientset
func newTestClient(objects ...runtime.Object) *Client {
	fakeClientset := fake.NewSimpleClientset(objects...) //nolint:staticcheck // NewClientset requires apply configurations
	return NewClientWithInterface(fakeClientset, nil)
}

// helper to create a PVC
func newPVC(namespace, name, pvName, capacity string) *corev1.PersistentVolumeClaim {
	qty := resource.MustParse(capacity)
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeName: pvName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: qty,
				},
			},
		},
	}
}

// helper to create a PV with CSI volume source
func newCSIPV(name, volumeHandle string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "ebs.csi.aws.com",
					VolumeHandle: volumeHandle,
				},
			},
		},
	}
}

// helper to create a PV with legacy AWS EBS volume source
func newLegacyEBSPV(name, volumeID string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				AWSElasticBlockStore: &corev1.AWSElasticBlockStoreVolumeSource{
					VolumeID: volumeID,
				},
			},
		},
	}
}

// helper to create a deployment
func newDeployment(namespace, name string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "main", Image: "nginx"},
					},
				},
			},
		},
	}
}

// helper to create a statefulset
func newStatefulSet(namespace, name string, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "main", Image: "nginx"},
					},
				},
			},
		},
	}
}

func TestClient_ListPVCs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		namespace string
		pvcs      []*corev1.PersistentVolumeClaim
		wantNames []string
		wantErr   bool
	}{
		{
			name:      "list_single_pvc",
			namespace: "default",
			pvcs: []*corev1.PersistentVolumeClaim{
				newPVC("default", "pvc-1", "pv-1", "10Gi"),
			},
			wantNames: []string{"pvc-1"},
			wantErr:   false,
		},
		{
			name:      "list_multiple_pvcs",
			namespace: "test-ns",
			pvcs: []*corev1.PersistentVolumeClaim{
				newPVC("test-ns", "pvc-a", "pv-a", "10Gi"),
				newPVC("test-ns", "pvc-b", "pv-b", "20Gi"),
				newPVC("test-ns", "pvc-c", "pv-c", "30Gi"),
			},
			wantNames: []string{"pvc-a", "pvc-b", "pvc-c"},
			wantErr:   false,
		},
		{
			name:      "empty_namespace",
			namespace: "empty-ns",
			pvcs:      []*corev1.PersistentVolumeClaim{},
			wantNames: []string{},
			wantErr:   false,
		},
		{
			name:      "filter_by_namespace",
			namespace: "target",
			pvcs: []*corev1.PersistentVolumeClaim{
				newPVC("target", "target-pvc", "pv-1", "10Gi"),
				newPVC("other", "other-pvc", "pv-2", "10Gi"),
			},
			wantNames: []string{"target-pvc"},
			wantErr:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var objects []runtime.Object
			for _, pvc := range tc.pvcs {
				objects = append(objects, pvc)
			}
			client := newTestClient(objects...)
			ctx := context.Background()

			names, err := client.ListPVCs(ctx, tc.namespace)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tc.wantNames, names)
		})
	}
}

func TestClient_GetPVCInfo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		namespace   string
		pvcName     string
		pvc         *corev1.PersistentVolumeClaim
		pv          *corev1.PersistentVolume
		wantInfo    *PVCInfo
		wantErr     bool
		errContains string
	}{
		{
			name:      "csi_volume",
			namespace: "default",
			pvcName:   "my-pvc",
			pvc:       newPVC("default", "my-pvc", "my-pv", "50Gi"),
			pv:        newCSIPV("my-pv", "vol-12345678"),
			wantInfo: &PVCInfo{
				PVName:     "my-pv",
				VolumeID:   "vol-12345678",
				Capacity:   "50Gi",
				CapacityGi: 50,
			},
			wantErr: false,
		},
		{
			name:      "legacy_ebs_volume",
			namespace: "default",
			pvcName:   "legacy-pvc",
			pvc:       newPVC("default", "legacy-pvc", "legacy-pv", "100Gi"),
			pv:        newLegacyEBSPV("legacy-pv", "aws://us-west-2a/vol-abcdef"),
			wantInfo: &PVCInfo{
				PVName:     "legacy-pv",
				VolumeID:   "vol-abcdef",
				Capacity:   "100Gi",
				CapacityGi: 100,
			},
			wantErr: false,
		},
		{
			name:        "pvc_not_found",
			namespace:   "default",
			pvcName:     "nonexistent",
			pvc:         nil,
			pv:          nil,
			wantInfo:    nil,
			wantErr:     true,
			errContains: "failed to get PVC",
		},
		{
			name:      "unbound_pvc",
			namespace: "default",
			pvcName:   "unbound-pvc",
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unbound-pvc",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "", // Unbound
				},
			},
			pv:          nil,
			wantInfo:    nil,
			wantErr:     true,
			errContains: "not bound",
		},
		{
			name:        "pv_not_found",
			namespace:   "default",
			pvcName:     "orphan-pvc",
			pvc:         newPVC("default", "orphan-pvc", "missing-pv", "10Gi"),
			pv:          nil,
			wantInfo:    nil,
			wantErr:     true,
			errContains: "failed to get PV",
		},
		{
			name:      "small_capacity",
			namespace: "default",
			pvcName:   "small-pvc",
			pvc:       newPVC("default", "small-pvc", "small-pv", "500Mi"),
			pv:        newCSIPV("small-pv", "vol-small"),
			wantInfo: &PVCInfo{
				PVName:     "small-pv",
				VolumeID:   "vol-small",
				Capacity:   "500Mi",
				CapacityGi: 1, // Minimum 1 GiB
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var objects []runtime.Object
			if tc.pvc != nil {
				objects = append(objects, tc.pvc)
			}
			if tc.pv != nil {
				objects = append(objects, tc.pv)
			}
			client := newTestClient(objects...)
			ctx := context.Background()

			info, err := client.GetPVCInfo(ctx, tc.namespace, tc.pvcName)

			if tc.wantErr {
				require.Error(t, err)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, info)
			assert.Equal(t, tc.wantInfo.PVName, info.PVName)
			assert.Equal(t, tc.wantInfo.VolumeID, info.VolumeID)
			assert.Equal(t, tc.wantInfo.Capacity, info.Capacity)
			assert.Equal(t, tc.wantInfo.CapacityGi, info.CapacityGi)
		})
	}
}

func TestClient_ScaleDownWorkloads(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		namespace     string
		deployments   []*appsv1.Deployment
		statefulsets  []*appsv1.StatefulSet
		wantWorkloads []WorkloadInfo
		wantErr       bool
	}{
		{
			name:      "scale_down_deployments",
			namespace: "test-ns",
			deployments: []*appsv1.Deployment{
				newDeployment("test-ns", "deploy-1", 3),
				newDeployment("test-ns", "deploy-2", 2),
			},
			statefulsets: nil,
			wantWorkloads: []WorkloadInfo{
				{Kind: "Deployment", Name: "deploy-1", Replicas: 3},
				{Kind: "Deployment", Name: "deploy-2", Replicas: 2},
			},
			wantErr: false,
		},
		{
			name:        "scale_down_statefulsets",
			namespace:   "db-ns",
			deployments: nil,
			statefulsets: []*appsv1.StatefulSet{
				newStatefulSet("db-ns", "mysql", 1),
				newStatefulSet("db-ns", "redis", 3),
			},
			wantWorkloads: []WorkloadInfo{
				{Kind: "StatefulSet", Name: "mysql", Replicas: 1},
				{Kind: "StatefulSet", Name: "redis", Replicas: 3},
			},
			wantErr: false,
		},
		{
			name:      "scale_down_mixed",
			namespace: "mixed-ns",
			deployments: []*appsv1.Deployment{
				newDeployment("mixed-ns", "web", 5),
			},
			statefulsets: []*appsv1.StatefulSet{
				newStatefulSet("mixed-ns", "db", 2),
			},
			wantWorkloads: []WorkloadInfo{
				{Kind: "Deployment", Name: "web", Replicas: 5},
				{Kind: "StatefulSet", Name: "db", Replicas: 2},
			},
			wantErr: false,
		},
		{
			name:          "empty_namespace",
			namespace:     "empty-ns",
			deployments:   nil,
			statefulsets:  nil,
			wantWorkloads: nil,
			wantErr:       false,
		},
		{
			name:      "skip_zero_replicas",
			namespace: "skip-ns",
			deployments: []*appsv1.Deployment{
				newDeployment("skip-ns", "running", 2),
				newDeployment("skip-ns", "stopped", 0),
			},
			statefulsets: nil,
			wantWorkloads: []WorkloadInfo{
				{Kind: "Deployment", Name: "running", Replicas: 2},
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var objects []runtime.Object
			for _, d := range tc.deployments {
				objects = append(objects, d)
			}
			for _, s := range tc.statefulsets {
				objects = append(objects, s)
			}
			client := newTestClient(objects...)
			ctx := context.Background()

			workloads, err := client.ScaleDownWorkloads(ctx, tc.namespace)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tc.wantWorkloads, workloads)

			// Verify that workloads were actually scaled to 0
			for _, w := range workloads {
				switch w.Kind {
				case kindDeployment:
					d, err := client.clientset.AppsV1().Deployments(tc.namespace).Get(ctx, w.Name, metav1.GetOptions{})
					require.NoError(t, err)
					assert.Equal(t, int32(0), *d.Spec.Replicas, "deployment %s should be scaled to 0", w.Name)
				case kindStatefulSet:
					s, err := client.clientset.AppsV1().StatefulSets(tc.namespace).Get(ctx, w.Name, metav1.GetOptions{})
					require.NoError(t, err)
					assert.Equal(t, int32(0), *s.Spec.Replicas, "statefulset %s should be scaled to 0", w.Name)
				}
			}
		})
	}
}

func TestClient_ScaleUpWorkloads(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		namespace string
		workloads []WorkloadInfo
		setup     func(client *Client)
		wantErr   bool
	}{
		{
			name:      "scale_up_deployments",
			namespace: "test-ns",
			workloads: []WorkloadInfo{
				{Kind: kindDeployment, Name: "web", Replicas: 3},
			},
			setup: func(client *Client) {
				d := newDeployment("test-ns", "web", 0)
				_, _ = client.clientset.AppsV1().Deployments("test-ns").Create(context.Background(), d, metav1.CreateOptions{})
			},
			wantErr: false,
		},
		{
			name:      "scale_up_statefulsets",
			namespace: "db-ns",
			workloads: []WorkloadInfo{
				{Kind: kindStatefulSet, Name: "mysql", Replicas: 2},
			},
			setup: func(client *Client) {
				s := newStatefulSet("db-ns", "mysql", 0)
				_, _ = client.clientset.AppsV1().StatefulSets("db-ns").Create(context.Background(), s, metav1.CreateOptions{})
			},
			wantErr: false,
		},
		{
			name:      "scale_up_mixed",
			namespace: "mixed-ns",
			workloads: []WorkloadInfo{
				{Kind: kindDeployment, Name: "app", Replicas: 5},
				{Kind: kindStatefulSet, Name: "cache", Replicas: 3},
			},
			setup: func(client *Client) {
				d := newDeployment("mixed-ns", "app", 0)
				s := newStatefulSet("mixed-ns", "cache", 0)
				_, _ = client.clientset.AppsV1().Deployments("mixed-ns").Create(context.Background(), d, metav1.CreateOptions{})
				_, _ = client.clientset.AppsV1().StatefulSets("mixed-ns").Create(context.Background(), s, metav1.CreateOptions{})
			},
			wantErr: false,
		},
		{
			name:      "deployment_not_found",
			namespace: "missing-ns",
			workloads: []WorkloadInfo{
				{Kind: kindDeployment, Name: "missing", Replicas: 1},
			},
			setup:   func(_ *Client) {},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := newTestClient()
			tc.setup(client)
			ctx := context.Background()

			err := client.ScaleUpWorkloads(ctx, tc.namespace, tc.workloads)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Verify replicas were restored
			for _, w := range tc.workloads {
				switch w.Kind {
				case kindDeployment:
					d, err := client.clientset.AppsV1().Deployments(tc.namespace).Get(ctx, w.Name, metav1.GetOptions{})
					require.NoError(t, err)
					assert.Equal(t, w.Replicas, *d.Spec.Replicas)
				case kindStatefulSet:
					s, err := client.clientset.AppsV1().StatefulSets(tc.namespace).Get(ctx, w.Name, metav1.GetOptions{})
					require.NoError(t, err)
					assert.Equal(t, w.Replicas, *s.Spec.Replicas)
				}
			}
		})
	}
}

func TestClient_GetWorkloadStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		namespace    string
		deployments  []*appsv1.Deployment
		statefulsets []*appsv1.StatefulSet
		wantCount    int
	}{
		{
			name:      "running_workloads",
			namespace: "test-ns",
			deployments: []*appsv1.Deployment{
				newDeployment("test-ns", "web", 3),
			},
			statefulsets: []*appsv1.StatefulSet{
				newStatefulSet("test-ns", "db", 1),
			},
			wantCount: 2,
		},
		{
			name:      "mixed_running_stopped",
			namespace: "mixed-ns",
			deployments: []*appsv1.Deployment{
				newDeployment("mixed-ns", "running", 2),
				newDeployment("mixed-ns", "stopped", 0),
			},
			statefulsets: nil,
			wantCount:    1, // Only running workloads
		},
		{
			name:         "empty_namespace",
			namespace:    "empty-ns",
			deployments:  nil,
			statefulsets: nil,
			wantCount:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var objects []runtime.Object
			for _, d := range tc.deployments {
				objects = append(objects, d)
			}
			for _, s := range tc.statefulsets {
				objects = append(objects, s)
			}
			client := newTestClient(objects...)
			ctx := context.Background()

			workloads, err := client.GetWorkloadStatus(ctx, tc.namespace)

			require.NoError(t, err)
			assert.Len(t, workloads, tc.wantCount)
		})
	}
}

func TestClient_CreateStaticPV(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		pvName       string
		volumeID     string
		capacity     string
		storageClass string
		targetZone   string
		wantErr      bool
	}{
		{
			name:         "create_pv_success",
			pvName:       "my-pv-static",
			volumeID:     "vol-12345",
			capacity:     "100Gi",
			storageClass: "gp3",
			targetZone:   "us-west-2a",
			wantErr:      false,
		},
		{
			name:         "create_pv_small_capacity",
			pvName:       "small-pv",
			volumeID:     "vol-small",
			capacity:     "1Gi",
			storageClass: "gp2",
			targetZone:   "eu-west-1b",
			wantErr:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := newTestClient()
			ctx := context.Background()

			err := client.CreateStaticPV(ctx, tc.pvName, tc.volumeID, tc.capacity, tc.storageClass, tc.targetZone)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Verify PV was created correctly
			pv, err := client.clientset.CoreV1().PersistentVolumes().Get(ctx, tc.pvName, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, tc.pvName, pv.Name)
			assert.Equal(t, "true", pv.Labels["migrated"])
			assert.Equal(t, tc.storageClass, pv.Spec.StorageClassName)
			assert.Equal(t, corev1.PersistentVolumeReclaimRetain, pv.Spec.PersistentVolumeReclaimPolicy)

			// Verify CSI source
			require.NotNil(t, pv.Spec.CSI)
			assert.Equal(t, "ebs.csi.aws.com", pv.Spec.CSI.Driver)
			assert.Equal(t, tc.volumeID, pv.Spec.CSI.VolumeHandle)

			// Verify node affinity
			require.NotNil(t, pv.Spec.NodeAffinity)
			require.NotNil(t, pv.Spec.NodeAffinity.Required)
			require.Len(t, pv.Spec.NodeAffinity.Required.NodeSelectorTerms, 1)
			assert.Equal(t, tc.targetZone, pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values[0])
		})
	}
}

func TestClient_CreateBoundPVC(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		namespace    string
		pvcName      string
		pvName       string
		capacity     string
		storageClass string
		wantErr      bool
	}{
		{
			name:         "create_pvc_success",
			namespace:    "default",
			pvcName:      "my-pvc",
			pvName:       "my-pv-static",
			capacity:     "100Gi",
			storageClass: "gp3",
			wantErr:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := newTestClient()
			ctx := context.Background()

			err := client.CreateBoundPVC(ctx, tc.namespace, tc.pvcName, tc.pvName, tc.capacity, tc.storageClass)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Verify PVC was created correctly
			pvc, err := client.clientset.CoreV1().PersistentVolumeClaims(tc.namespace).Get(ctx, tc.pvcName, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, tc.pvcName, pvc.Name)
			assert.Equal(t, tc.namespace, pvc.Namespace)
			assert.Equal(t, "true", pvc.Labels["migrated"])
			assert.Equal(t, tc.pvName, pvc.Spec.VolumeName)
			assert.Equal(t, tc.storageClass, *pvc.Spec.StorageClassName)
		})
	}
}

func TestClient_CleanupResources(t *testing.T) {
	t.Parallel()

	t.Run("cleanup_existing_resources", func(t *testing.T) {
		t.Parallel()

		pvc := newPVC("default", "cleanup-pvc", "cleanup-pv", "10Gi")
		pv := newCSIPV("cleanup-pv", "vol-123")
		client := newTestClient(pvc, pv)
		ctx := context.Background()

		err := client.CleanupResources(ctx, "default", "cleanup-pvc", "cleanup-pv")

		require.NoError(t, err)

		// Verify PVC was deleted
		_, err = client.clientset.CoreV1().PersistentVolumeClaims("default").Get(ctx, "cleanup-pvc", metav1.GetOptions{})
		assert.True(t, err != nil, "PVC should be deleted")

		// Verify PV was deleted
		_, err = client.clientset.CoreV1().PersistentVolumes().Get(ctx, "cleanup-pv", metav1.GetOptions{})
		assert.True(t, err != nil, "PV should be deleted")
	})

	t.Run("cleanup_nonexistent_resources", func(t *testing.T) {
		t.Parallel()

		client := newTestClient()
		ctx := context.Background()

		// Should not error when resources don't exist
		err := client.CleanupResources(ctx, "default", "nonexistent-pvc", "nonexistent-pv")

		require.NoError(t, err)
	})
}

func TestClient_ListPVCs_APIError(t *testing.T) {
	t.Parallel()

	fakeClientset := fake.NewSimpleClientset() //nolint:staticcheck // deprecated but still functional
	// Add reactor to simulate API error
	fakeClientset.PrependReactor("list", "persistentvolumeclaims", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})
	client := NewClientWithInterface(fakeClientset, nil)
	ctx := context.Background()

	_, err := client.ListPVCs(ctx, "test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list PVCs")
}

func TestWorkloadInfo_Fields(t *testing.T) {
	t.Parallel()

	w := WorkloadInfo{
		Kind:     "Deployment",
		Name:     "test-app",
		Replicas: 5,
	}

	assert.Equal(t, "Deployment", w.Kind)
	assert.Equal(t, "test-app", w.Name)
	assert.Equal(t, int32(5), w.Replicas)
}

func TestPVCInfo_Fields(t *testing.T) {
	t.Parallel()

	info := PVCInfo{
		PVName:     "pv-test",
		VolumeID:   "vol-abc123",
		Capacity:   "50Gi",
		CapacityGi: 50,
	}

	assert.Equal(t, "pv-test", info.PVName)
	assert.Equal(t, "vol-abc123", info.VolumeID)
	assert.Equal(t, "50Gi", info.Capacity)
	assert.Equal(t, int32(50), info.CapacityGi)
}

func TestArgoCDAppInfo_Fields(t *testing.T) {
	t.Parallel()

	info := ArgoCDAppInfo{
		Name:      "myapp",
		Namespace: "argocd",
	}

	assert.Equal(t, "myapp", info.Name)
	assert.Equal(t, "argocd", info.Namespace)
}
