package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// storageMigrationImage 数据迁移 Pod 使用的轻量镜像
	storageMigrationImage = "iregistry.baidu-int.com/magellan-public/busybox:1.0.0"
	// storageMigrationContainerName 迁移容器名
	storageMigrationContainerName = "migrate"
)

// CreateStorageMigrationPod 创建临时迁移 Pod，将 sourcePVC 数据拷贝到 targetPVC。
// sourceNode 为空时不使用 nodeSelector（用于 CFS → CFS-N 迁移），否则固定到指定节点（用于 local-path → CFS 迁移）。
func (cluster *Cluster) CreateStorageMigrationPod(ctx context.Context, podName, sourceNode, sourcePVCName, targetPVCName string) error {
	// 迁移脚本：cp -a 拷贝 + find 文件数校验
	migrateScript := `echo "Starting data copy..."
cp -a /source/. /target/
SRC=$(find /source -type f | wc -l)
DST=$(find /target -type f | wc -l)
echo "Source files: $SRC, Target files: $DST"
[ "$SRC" = "$DST" ] && echo "VERIFIED" || (echo "MISMATCH" && exit 1)`

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: cluster.namespace,
			Labels: map[string]string{
				"workload-type": "storage-migration",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    storageMigrationContainerName,
					Image:   storageMigrationImage,
					Command: []string{"sh", "-c"},
					Args:    []string{migrateScript},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "source",
							MountPath: "/source",
							ReadOnly:  true,
						},
						{
							Name:      "target",
							MountPath: "/target",
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2000m"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "source",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: sourcePVCName,
						},
					},
				},
				{
					Name: "target",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: targetPVCName,
						},
					},
				},
			},
		},
	}

	// local-path/hp1 → CFS：需要固定到源节点
	if sourceNode != "" {
		pod.Spec.NodeSelector = map[string]string{
			"kubernetes.io/hostname": sourceNode,
		}
	}

	_, err := cluster.clientSet.CoreV1().Pods(cluster.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create storage migration pod %s: %w", podName, err)
	}
	return nil
}

// WaitForPodCompletion 等待指定 Pod 进入 Succeeded 或 Failed 状态，超时返回错误。
func (cluster *Cluster) WaitForPodCompletion(ctx context.Context, podName string, timeout time.Duration) (corev1.PodPhase, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("timeout waiting for pod %s to complete", podName)
		case <-ticker.C:
			pod, err := cluster.clientSet.CoreV1().Pods(cluster.namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return "", fmt.Errorf("pod %s not found", podName)
				}
				continue
			}
			switch pod.Status.Phase {
			case corev1.PodSucceeded, corev1.PodFailed:
				return pod.Status.Phase, nil
			}
		}
	}
}

// GetPodLogs 获取指定 Pod 容器的日志，用于失败诊断。
func (cluster *Cluster) GetPodLogs(ctx context.Context, podName, containerName string) (string, error) {
	req := cluster.clientSet.CoreV1().Pods(cluster.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
	})
	result := req.Do(ctx)
	if result.Error() != nil {
		return "", result.Error()
	}
	raw, err := result.Raw()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// DeletePodByName 按名称删除 Pod，已不存在时不报错。
func (cluster *Cluster) DeletePodByName(ctx context.Context, podName string) error {
	err := cluster.clientSet.CoreV1().Pods(cluster.namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete pod %s: %w", podName, err)
	}
	return nil
}
