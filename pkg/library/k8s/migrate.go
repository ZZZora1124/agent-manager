package k8s

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// GetPV retrieves a PersistentVolume by name.
func (cluster *Cluster) GetPV(ctx context.Context, pvName string) (*corev1.PersistentVolume, error) {
	return cluster.clientSet.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
}

// GetPVC retrieves a PersistentVolumeClaim by name within the cluster namespace.
func (cluster *Cluster) GetPVC(ctx context.Context, pvcName string) (*corev1.PersistentVolumeClaim, error) {
	return cluster.clientSet.CoreV1().PersistentVolumeClaims(cluster.namespace).Get(ctx, pvcName, metav1.GetOptions{})
}

// GetPVSourceNode extracts the node where the PV is bound, from nodeAffinity or PVC annotation.
func (cluster *Cluster) GetPVSourceNode(ctx context.Context, pvName, pvcName string) (string, error) {
	pv, err := cluster.GetPV(ctx, pvName)
	if err != nil {
		return "", fmt.Errorf("get PV %s: %w", pvName, err)
	}

	// Try nodeAffinity first (local-path provisioner uses this).
	if node := extractNodeFromAffinity(pv); node != "" {
		return node, nil
	}

	// Fallback: PVC annotation "volume.kubernetes.io/selected-node".
	pvc, err := cluster.GetPVC(ctx, pvcName)
	if err != nil {
		return "", fmt.Errorf("get PVC %s: %w", pvcName, err)
	}
	if node, ok := pvc.Annotations["volume.kubernetes.io/selected-node"]; ok && node != "" {
		return node, nil
	}

	return "", fmt.Errorf("cannot determine source node for PV %s / PVC %s", pvName, pvcName)
}

// extractNodeFromAffinity reads the node name from PV's nodeAffinity.required match expressions.
func extractNodeFromAffinity(pv *corev1.PersistentVolume) string {
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		return ""
	}
	for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			if expr.Key == "kubernetes.io/hostname" && expr.Operator == corev1.NodeSelectorOpIn && len(expr.Values) > 0 {
				return expr.Values[0]
			}
		}
	}
	return ""
}

// GetPVLocalPath returns the local path from a PV (spec.local.path or spec.hostPath.path).
func (cluster *Cluster) GetPVLocalPath(pv *corev1.PersistentVolume) string {
	if pv.Spec.Local != nil {
		return pv.Spec.Local.Path
	}
	if pv.Spec.HostPath != nil {
		return pv.Spec.HostPath.Path
	}
	return ""
}

// FindStatefulSetByLabel finds a single StatefulSet by label selector in the cluster namespace.
func (cluster *Cluster) FindStatefulSetByLabel(ctx context.Context, labelSelector string) (string, error) {
	stsList, err := cluster.clientSet.AppsV1().StatefulSets(cluster.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("list statefulsets: %w", err)
	}
	if len(stsList.Items) == 0 {
		return "", fmt.Errorf("no statefulset found with selector %q", labelSelector)
	}
	return stsList.Items[0].Name, nil
}

// ScaleStatefulSet sets replicas and waits for the scale to be applied.
func (cluster *Cluster) ScaleStatefulSet(ctx context.Context, stsName string, replicas int32) error {
	sts, err := cluster.clientSet.AppsV1().StatefulSets(cluster.namespace).Get(ctx, stsName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get statefulset %s: %w", stsName, err)
	}

	sts.Spec.Replicas = &replicas
	_, err = cluster.clientSet.AppsV1().StatefulSets(cluster.namespace).Update(ctx, sts, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update statefulset %s replicas to %d: %w", stsName, replicas, err)
	}
	return nil
}

// WaitForPodTermination waits until all pods matching the label selector are terminated.
func (cluster *Cluster) WaitForPodTermination(ctx context.Context, labelSelector string, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout waiting for pod termination (selector=%s)", labelSelector)
		case <-ticker.C:
			pods, err := cluster.clientSet.CoreV1().Pods(cluster.namespace).List(ctx, metav1.ListOptions{
				LabelSelector: labelSelector,
			})
			if err != nil {
				return fmt.Errorf("list pods: %w", err)
			}
			if len(pods.Items) == 0 {
				return nil
			}
		}
	}
}

// WaitForPodRunning waits until at least one pod matching the label selector is Running and all containers ready.
func (cluster *Cluster) WaitForPodRunning(ctx context.Context, labelSelector string, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout waiting for pod running (selector=%s)", labelSelector)
		case <-ticker.C:
			pods, err := cluster.clientSet.CoreV1().Pods(cluster.namespace).List(ctx, metav1.ListOptions{
				LabelSelector: labelSelector,
			})
			if err != nil {
				return fmt.Errorf("list pods: %w", err)
			}
			for i := range pods.Items {
				pod := &pods.Items[i]
				if pod.Status.Phase == corev1.PodRunning && allContainersReady(pod) {
					return nil
				}
			}
		}
	}
}

// RecreatePVForMigration atomically deletes the old PV and creates a new one pointing to the
// target node and path. On failure it attempts to restore the original PV.
// It also patches the PVC annotation "volume.kubernetes.io/selected-node".
func (cluster *Cluster) RecreatePVForMigration(ctx context.Context, originalPV *corev1.PersistentVolume,
	pvcName, targetNode, targetPath string) error {

	pvClient := cluster.clientSet.CoreV1().PersistentVolumes()

	// 0. Get PVC to obtain its UID (required for PV-PVC binding).
	pvc, err := cluster.GetPVC(ctx, pvcName)
	if err != nil {
		return fmt.Errorf("get PVC %s for UID: %w", pvcName, err)
	}

	// 1. Build new PV from original, with correct PVC UID.
	newPV := buildMigratedPV(originalPV, targetNode, targetPath, pvc.UID)

	// 2. Remove finalizers from original PV so deletion is not blocked.
	if len(originalPV.Finalizers) > 0 {
		orig, err := pvClient.Get(ctx, originalPV.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get PV for finalizer cleanup: %w", err)
		}
		orig.Finalizers = nil
		if _, err := pvClient.Update(ctx, orig, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("clear PV finalizers: %w", err)
		}
	}

	// 3. Delete old PV.
	if err := pvClient.Delete(ctx, originalPV.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete old PV %s: %w", originalPV.Name, err)
	}

	// Wait briefly for PV deletion to complete.
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		_, err := pvClient.Get(waitCtx, originalPV.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			break
		}
		if waitCtx.Err() != nil {
			return fmt.Errorf("timeout waiting for old PV %s deletion", originalPV.Name)
		}
		time.Sleep(1 * time.Second)
	}

	// 4. Create new PV.
	if _, err := pvClient.Create(ctx, newPV, metav1.CreateOptions{}); err != nil {
		// Attempt to restore original PV.
		restorePV := originalPV.DeepCopy()
		restorePV.ResourceVersion = ""
		restorePV.UID = ""
		if _, restoreErr := pvClient.Create(ctx, restorePV, metav1.CreateOptions{}); restoreErr != nil {
			return fmt.Errorf("create new PV failed: %w; restore original also failed: %v", err, restoreErr)
		}
		return fmt.Errorf("create new PV failed (original restored): %w", err)
	}

	// 5. Patch PVC annotation to point to the new node.
	// Re-fetch PVC to get latest resourceVersion.
	pvc, err = cluster.clientSet.CoreV1().PersistentVolumeClaims(cluster.namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get PVC %s for annotation patch: %w", pvcName, err)
	}
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}
	pvc.Annotations["volume.kubernetes.io/selected-node"] = targetNode
	if _, err := cluster.clientSet.CoreV1().PersistentVolumeClaims(cluster.namespace).Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("patch PVC annotation: %w", err)
	}

	return nil
}

// buildMigratedPV constructs a new PV object targeting a different node and path.
// pvcUID is the actual UID of the PVC, required for PV-PVC binding to work.
func buildMigratedPV(orig *corev1.PersistentVolume, targetNode, targetPath string, pvcUID types.UID) *corev1.PersistentVolume {
	// Determine capacity from original, fallback to 10Gi.
	capacity := orig.Spec.Capacity
	if capacity == nil {
		capacity = corev1.ResourceList{
			corev1.ResourceStorage: resource.MustParse("10Gi"),
		}
	}

	// Determine storage class.
	sc := ""
	if orig.Spec.StorageClassName != "" {
		sc = orig.Spec.StorageClassName
	}

	// 深拷贝注解并更新 local.path.provisioner/selected-node 为目标节点
	annotations := make(map[string]string, len(orig.Annotations))
	for k, v := range orig.Annotations {
		annotations[k] = v
	}
	annotations["local.path.provisioner/selected-node"] = targetNode

	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        orig.Name,
			Labels:      orig.Labels,
			Annotations: annotations,
		},
		Spec: corev1.PersistentVolumeSpec{
			Capacity:                      capacity,
			AccessModes:                   orig.Spec.AccessModes,
			PersistentVolumeReclaimPolicy: orig.Spec.PersistentVolumeReclaimPolicy,
			StorageClassName:              sc,
			ClaimRef:                      orig.Spec.ClaimRef,
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				Local: &corev1.LocalVolumeSource{
					Path: targetPath,
				},
			},
			NodeAffinity: &corev1.VolumeNodeAffinity{
				Required: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{targetNode},
								},
							},
						},
					},
				},
			},
		},
	}

	// Set claimRef UID to PVC's actual UID for proper binding.
	// Clear resourceVersion so K8s accepts the new PV.
	if pv.Spec.ClaimRef != nil {
		pv.Spec.ClaimRef.UID = pvcUID
		pv.Spec.ClaimRef.ResourceVersion = ""
	}

	return pv
}

// GetStatefulSet retrieves a StatefulSet by name.
func (cluster *Cluster) GetStatefulSet(ctx context.Context, name string) (*appsv1.StatefulSet, error) {
	return cluster.clientSet.AppsV1().StatefulSets(cluster.namespace).Get(ctx, name, metav1.GetOptions{})
}
