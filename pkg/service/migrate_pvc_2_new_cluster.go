package service

import (
	"context"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/env"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/gdp/logit"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (

	// SbxTargetAppID 需要迁移的目标 AppID
	SbxTargetAppID = 7
	// SbxTargetClusterName 迁移目标集群名称
	SbxTargetClusterName = "agent-sandbox2"

	// TargetAppID 需要迁移的目标 AppID
	TargetAppID = 2071
	// TargetClusterName 迁移目标集群名称
	TargetClusterName = "agent-sandbox2-prod"
)

// MigrateSbxPVCToNewCluster 将沙箱的 PVC 和 PV 从老集群迁移到新集群
// 当满足以下条件时执行迁移：
// 1. 沙箱开启 PVC (usePvc = true)
// 2. app_id = 2071
// 3. cluster != "agent-sandbox2-prod"
// 4. storage_class = "sharedcfs"
//
// 迁移流程：
// 1. 从老集群获取 PVC 和 PV
// 2. 在新集群创建相同的 PVC 和 PV
// 3. 更新 sandbox_runtime 表的 cluster 字段为新集群名称
func MigrateSbxPVCToNewCluster(ctx context.Context, sandboxUUID string, binding *model.SandboxPVCBinding, oldClusterName string, appID int32) error {
	// 检查是否满足迁移条件
	if binding == nil {
		return nil
	}
	targetClusterName := SbxTargetClusterName
	if env.RunMode() == env.RunModeRelease {
		if appID != TargetAppID {
			return nil
		}
		if oldClusterName == TargetClusterName {
			return nil
		}
		targetClusterName = TargetClusterName
	} else {
		if appID != SbxTargetAppID {
			return nil
		}
		if oldClusterName == SbxTargetClusterName {
			return nil
		}
	}
	// 上锁，防止迁移过程中恢复沙箱
	lockKey := constant.SandboxRestoreLockPrefix + sandboxUUID
	lock, err := resource.RedisClient.SetNX(ctx, lockKey, "1", 10*time.Minute).Result()
	if err != nil {
		resource.LoggerService.Error(ctx, "set migrate pvc lock failed",
			logit.String("sandboxID", sandboxUUID),
			logit.String("error", err.Error()))
		return fmt.Errorf("set lock failed: %w", err)
	}
	if !lock {
		return nil
	}
	defer resource.RedisClient.Del(context.Background(), lockKey)

	resource.LoggerService.Notice(ctx, "set migrate pvc lock success,start migrate pvc",
		logit.String("sandboxID", sandboxUUID))

	// 检查 storage class 是否为 sharedcfs
	if binding.StorageClass != StorageClassCFS {
		return nil
	}

	resource.LoggerService.Notice(ctx, "start migrating PVC to new cluster",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("oldCluster", oldClusterName),
		logit.String("newCluster", targetClusterName),
		logit.String("pvcName", binding.PVCName))

	err = MigratePVCToNewCluster(ctx, binding.PVCName, oldClusterName, targetClusterName)
	if err != nil {
		resource.LoggerService.Warning(ctx, "migrate pvc failed",
			logit.String("error", err.Error()))
		return fmt.Errorf("migrate pvc failed: %w", err)
	}

	// 更新 sandbox_runtime 表的 cluster 字段
	err = resource.OrmMain.WithContext(ctx).
		Model(&model.SandboxRuntime{}).
		Where("sandbox_uuid = ?", sandboxUUID).
		Update("cluster", targetClusterName).Error
	if err != nil {
		resource.LoggerService.Warning(ctx, "update sandbox_runtime cluster failed",
			logit.String("error", err.Error()))
		return fmt.Errorf("update sandbox_runtime cluster failed: %w", err)
	}

	// 获取新集群
	newCluster := resource.ClusterManager.GetClusterByName(targetClusterName)

	// 更新 sandbox_pvc_binding 表的 cluster 字段
	updatesFields := make(map[string]any)
	updatesFields["cluster"] = targetClusterName
	updatesFields["namespace"] = newCluster.GetNamespace()

	err = resource.OrmMain.WithContext(ctx).
		Model(&model.SandboxPVCBinding{}).
		Where("sandbox_uuid = ? AND deleted = 0", sandboxUUID).Updates(updatesFields).Error
	if err != nil {
		resource.LoggerService.Warning(ctx, "update sandbox_pvc_binding cluster failed",
			logit.String("error", err.Error()))
	}

	resource.LoggerService.Notice(ctx, "PVC migrated to new cluster successfully",
		logit.String("sandboxUUID", sandboxUUID),
		logit.String("oldCluster", oldClusterName),
		logit.String("newCluster", targetClusterName))

	return nil
}

func MigratePVCToNewCluster(ctx context.Context, pvcName, oldClusterName string, newClusterName string) error {

	// 获取老集群
	oldCluster := resource.ClusterManager.GetClusterByName(oldClusterName)
	if oldCluster == nil {
		return fmt.Errorf("old cluster %s not found", oldClusterName)
	}

	// 获取新集群
	newCluster := resource.ClusterManager.GetClusterByName(newClusterName)
	if newCluster == nil {
		return fmt.Errorf("new cluster %s not found", newClusterName)
	}

	// 从老集群获取 PVC
	oldPVC, err := oldCluster.GetPVC(ctx, pvcName)
	if err != nil {
		return fmt.Errorf("get PVC from old cluster failed: %w", err)
	}

	// 获取 PV 名称
	pvName := oldPVC.Spec.VolumeName
	if pvName == "" {
		return fmt.Errorf("PVC %s has no bound PV", pvcName)
	}

	// 从老集群获取 PV
	oldPV, err := oldCluster.GetPV(ctx, pvName)
	if err != nil {
		return fmt.Errorf("get PV from old cluster failed: %w", err)
	}

	// 在新集群创建 PVC
	newPVC := buildPVCForNewCluster(oldPVC)
	newPVC, createErr := createPVCWithRetry(ctx, newCluster, newPVC)
	if createErr != nil {
		return fmt.Errorf("create PVC in new cluster failed: %w", err)
	}
	resource.LoggerService.Notice(ctx, "PVC created in new cluster",
		logit.String("pvcName", newPVC.Name))

	// 在新集群创建 PV
	newPV := buildPVForNewCluster(oldPV, newPVC)
	if err := createPVWithRetry(ctx, newCluster, newPV); err != nil {
		return fmt.Errorf("create PV in new cluster failed: %w", err)
	}
	return nil
}

// buildPVForNewCluster 根据老集群的 PV 构建新集群的 PV
func buildPVForNewCluster(oldPV *corev1.PersistentVolume, newPvc *corev1.PersistentVolumeClaim) *corev1.PersistentVolume {
	newPV := oldPV.DeepCopy()
	newPV.ResourceVersion = ""
	newPV.UID = ""
	newPV.CreationTimestamp = metav1.Time{}
	newPV.ManagedFields = nil
	newPV.ObjectMeta.Annotations = oldPV.Annotations

	// 更新 claimRef 的 namespace
	newPV.Spec.ClaimRef = &corev1.ObjectReference{
		Kind:      "PersistentVolumeClaim",
		Namespace: newPvc.Namespace,
		Name:      newPvc.Name,
		UID:       newPvc.UID,
	}
	newPV.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimDelete
	newPV.Status = corev1.PersistentVolumeStatus{}
	return newPV
}

// buildPVCForNewCluster 根据老集群的 PVC 构建新集群的 PVC
func buildPVCForNewCluster(oldPVC *corev1.PersistentVolumeClaim) *corev1.PersistentVolumeClaim {
	newPVC := oldPVC.DeepCopy()
	newPVC.ResourceVersion = ""
	newPVC.UID = ""
	newPVC.CreationTimestamp = metav1.Time{}
	newPVC.ManagedFields = nil

	return newPVC
}

// createPVWithRetry 创建 PV，如果已存在则跳过
func createPVWithRetry(ctx context.Context, cluster *k8s.Cluster, pv *corev1.PersistentVolume) error {
	_, err := cluster.GetClientSet().CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			// PV 已存在，跳过
			return nil
		}
		return err
	}
	return nil
}

// createPVCWithRetry 创建 PVC，如果已存在则跳过
func createPVCWithRetry(ctx context.Context, cluster *k8s.Cluster, pvc *corev1.PersistentVolumeClaim) (*corev1.PersistentVolumeClaim, error) {
	pvc, err := cluster.GetClientSet().CoreV1().PersistentVolumeClaims(cluster.GetNamespace()).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			// 已存在，则查询出来;
			getPvc, getErr := cluster.GetClientSet().CoreV1().PersistentVolumeClaims(cluster.GetNamespace()).Get(ctx, pvc.Name, metav1.GetOptions{})
			return getPvc, getErr
		}
		return nil, err
	}
	return pvc, nil
}
