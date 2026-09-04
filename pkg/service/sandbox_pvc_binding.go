package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	corev1 "k8s.io/api/core/v1"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
)

const (
	defaultSandboxPVCStorageClass = "sharedcfs"
	defaultSandboxPVCSize         = "10Gi"
	defaultSandboxPVCGracePeriod  = 90 * 24 * time.Hour

	StorageClassLocalPath = "local-path"
	StorageClassHP1       = "hp1"
	StorageClassCFS       = "sharedcfs"
	StorageClassBOS       = "csi-bos2"

	StorageClassCFS2  = "sharedcfs-2"
	StorageClassCFS3  = "sharedcfs-3"
	StorageClassCFS4  = "sharedcfs-4"
	StorageClassCFS5  = "sharedcfs-5"
	StorageClassCFS6  = "sharedcfs-6"
	StorageClassCFS7  = "sharedcfs-7"
	StorageClassCFS8  = "sharedcfs-8"
	StorageClassCFS9  = "sharedcfs-9"
	StorageClassCFS10 = "sharedcfs-10"
)

// 新建沙箱实例 可用的 CFS StorageClass 列表（哈希分配用）
var availableCFSStorageClasses = []string{
	StorageClassCFS2,
	StorageClassCFS3,
	StorageClassCFS4,
	StorageClassCFS5,
	StorageClassCFS6,
	StorageClassCFS7,
	StorageClassCFS8,
	StorageClassCFS9,
	StorageClassCFS10,
}

var allowedStorageClasses = map[string]bool{
	StorageClassLocalPath: true,
	StorageClassHP1:       true,
	StorageClassCFS:       true,
	StorageClassBOS:       true,
	StorageClassCFS2:      true,
	StorageClassCFS3:      true,
	StorageClassCFS4:      true,
	StorageClassCFS5:      true,
	StorageClassCFS6:      true,
	StorageClassCFS7:      true,
	StorageClassCFS8:      true,
	StorageClassCFS9:      true,
	StorageClassCFS10:     true,
}

// IsCFSStorageClass 判断是否为 CFS 存储类。
func IsCFSStorageClass(sc string) bool {
	if sc == StorageClassCFS {
		return true
	}
	for _, cfsSC := range availableCFSStorageClasses {
		if sc == cfsSC {
			return true
		}
	}
	return false
}

// IsBOSStorageClass 判断是否为 BOS 存储类。
func IsBOSStorageClass(sc string) bool {
	return sc == StorageClassBOS
}

// IsSharedStorageClass 判断是否为跨节点共享存储类（CFS/BOS），这类存储不需要迁移。
func IsSharedStorageClass(sc string) bool {
	return IsCFSStorageClass(sc) || IsBOSStorageClass(sc)
}

// SelectCFSStorageClass 根据用户名哈希选择 CFS StorageClass
// 保证同一用户的所有沙箱始终映射到同一个 StorageClass
func SelectCFSStorageClass(username string) string {
	if len(availableCFSStorageClasses) == 0 {
		return defaultSandboxPVCStorageClass
	}
	if len(availableCFSStorageClasses) == 1 {
		return availableCFSStorageClasses[0]
	}

	// 使用 username 进行哈希
	hash := fnvHash(username)
	index := hash % uint32(len(availableCFSStorageClasses))

	return availableCFSStorageClasses[index]
}

// fnvHash FNV-1a 哈希算法
func fnvHash(s string) uint32 {
	const prime32 = uint32(16777619)
	hash := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		hash ^= uint32(s[i])
		hash *= prime32
	}
	return hash
}

// ValidateSandboxStorageClass 校验存储类名称，nil/空串返回默认值（哈希分配），非法值返回 error。
// 参数 username: 用户名，用于哈希选择 StorageClass
func ValidateSandboxStorageClass(storageClass *string, username string) (string, error) {
	if storageClass == nil || *storageClass == "" {
		// 如果没有指定，使用哈希策略选择 CFS StorageClass
		return SelectCFSStorageClass(username), nil
	}
	if !allowedStorageClasses[*storageClass] {
		return "", fmt.Errorf("invalid storageClass %q, allowed: local-path, hp1, sharedcfs, sharedcfs-2, sharedcfs-3, sharedcfs-4, sharedcfs-5, sharedcfs-6, sharedcfs-7, sharedcfs-8, sharedcfs-9, sharedcfs-10, csi-bos2", *storageClass)
	}
	return *storageClass, nil
}

func DefaultSandboxPVCMountPath() string {
	// 实际挂载路径由 workspace 卷固定到 /workspace；binding.mount_path 仅用于记录。
	return k8s.WorkspaceVolumeMountPath
}

func DefaultSandboxPVCGracePeriod() time.Duration {
	return defaultSandboxPVCGracePeriod
}

func buildSandboxPVCName(sandboxUUID string) string {
	return fmt.Sprintf("sandbox-%s-pvc", sandboxUUID)
}

// buildPVCSize 构建 PVC 大小字符串（如 "20Gi"）
func buildPVCSize(sizeGB int) string {
	if sizeGB <= 0 {
		sizeGB = 20 // 默认 20G
	}
	return fmt.Sprintf("%dGi", sizeGB)
}

func GetSandboxPVCBinding(ctx context.Context, sandboxUUID string) (*model.SandboxPVCBinding, error) {
	var binding model.SandboxPVCBinding
	err := resource.OrmMain.WithContext(ctx).
		Where("sandbox_uuid = ? AND deleted = 0", sandboxUUID).
		First(&binding).Error
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

// GetLatestSandboxPVCBinding 获取最近的 PVC binding（包含已删除的记录）。
// 用于恢复场景：即使 binding 已被 PVC Cleaner 清理（deleted=1），仍需读取原始 storageClass 以正确重建。
func GetLatestSandboxPVCBinding(ctx context.Context, sandboxUUID string) (*model.SandboxPVCBinding, error) {
	var binding model.SandboxPVCBinding
	err := resource.OrmMain.WithContext(ctx).
		Where("sandbox_uuid = ?", sandboxUUID).
		Order("id DESC").
		First(&binding).Error
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

// GetSandboxPVCBindingsByUUIDs 批量查询 PVC binding 信息
// 返回 map[sandboxUUID]*model.SandboxPVCBinding，用于快速查找
func GetSandboxPVCBindingsByUUIDs(ctx context.Context, sandboxUUIDs []string) (map[string]*model.SandboxPVCBinding, error) {
	if len(sandboxUUIDs) == 0 {
		return make(map[string]*model.SandboxPVCBinding), nil
	}

	var bindings []model.SandboxPVCBinding
	err := resource.OrmMain.WithContext(ctx).
		Where("sandbox_uuid IN ? AND deleted = 0", sandboxUUIDs).
		Find(&bindings).Error
	if err != nil {
		return nil, err
	}

	// 转换为 map 方便查找
	result := make(map[string]*model.SandboxPVCBinding, len(bindings))
	for i := range bindings {
		result[bindings[i].SandboxUUID] = &bindings[i]
	}
	return result, nil
}

// EnsureSandboxPVCBinding 创建/复用 sandbox 专属 PVC 并写入 binding。
// 参数 pvcSizeGB: PVC 容量大小（单位 GB），若 <=0 则使用默认值 20G
// 参数 storageClass: 存储类名称（如 "local-path", "hp1", "sharedcfs", "csi-bos2"），空串使用默认值
// 参数 username: 用户名，用于哈希选择 StorageClass
// 注意：pv_name 不再记录（不需要知道 PV 名称），删除时通过 PVC 反查即可。
func EnsureSandboxPVCBinding(ctx context.Context, appID int32, sandboxUUID, mountPath, clusterName, namespace string, pvcSizeGB int, storageClass string, cluster *k8s.Cluster, username string) (*model.SandboxPVCBinding, error) {
	binding, err := GetSandboxPVCBinding(ctx, sandboxUUID)
	if err == nil {
		// 复用现有绑定（不修改 PVC 大小和存储类）
		return binding, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if storageClass == "" {
		storageClass = SelectCFSStorageClass(username)
	}

	pvcName := buildSandboxPVCName(sandboxUUID)
	pvcSize := buildPVCSize(pvcSizeGB) // 计算实际大小

	// 根据存储类选择不同的供给方式
	switch {
	case IsBOSStorageClass(storageClass):
		// BOS 静态供给：创建 PV + PVC
		pvName := k8s.BuildBOSPVName(sandboxUUID)
		subDir := fmt.Sprintf("sandbox-%s", sandboxUUID)
		if err = cluster.CreateBOSPVAndPVC(ctx, pvName, pvcName, k8s.BOSSandboxBucket, subDir, pvcSize); err != nil {
			return nil, err
		}
	case IsCFSStorageClass(storageClass):
		// CFS 动态供给：创建 PVC，使用 ReadWriteMany 访问模式
		if err = cluster.CreateDynamicPVCWithAccessMode(ctx, pvcName, storageClass, pvcSize, corev1.ReadWriteMany); err != nil {
			return nil, err
		}
	default:
		// local-path / hp1 动态供给：创建 PVC，使用 ReadWriteOnce 访问模式
		if err = cluster.CreateDynamicPVC(ctx, pvcName, storageClass, pvcSize); err != nil {
			return nil, err
		}
	}

	now := time.Now()
	binding = &model.SandboxPVCBinding{
		SandboxUUID:  sandboxUUID,
		AppID:        appID,
		PVCName:      pvcName,
		MountPath:    mountPath,
		Cluster:      clusterName,
		Namespace:    namespace,
		StorageClass: storageClass,
		Status:       model.SandboxPVCStatusActive,
		Deleted:      false,
		CreateTime:   now,
		UpdateTime:   now,
	}
	if err = resource.OrmMain.WithContext(ctx).Create(binding).Error; err != nil {
		// DB 写失败时补偿删除已创建的 PVC，避免孤儿资源。
		_ = cluster.DeletePVC(ctx, pvcName)
		// BOS 场景额外清理 PV
		if IsBOSStorageClass(storageClass) {
			pvName := k8s.BuildBOSPVName(sandboxUUID)
			_ = cluster.DeletePV(ctx, pvName)
		}
		return nil, err
	}
	return binding, nil
}

func ReleaseSandboxPVCBinding(ctx context.Context, sandboxUUID string, endTime time.Time, gracePeriod time.Duration) error {
	reclaimAt := endTime.Add(gracePeriod)
	// 语义：沙箱关闭后将 PVC 绑定置为 kill（保留一段时间），到期后由 PVCCleaner 转为 released 并清理资源。
	// 注意：运行中/待运行的沙箱不允许释放 PVC（避免误释放）。
	return resource.OrmMain.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 防御：运行中/待运行的沙箱不允许释放 PVC（避免误删/误回收）。
		var rt model.SandboxRuntime
		if err := tx.Where("sandbox_uuid = ?", sandboxUUID).First(&rt).Error; err == nil {
			if rt.Status == constant.StatusRunning || rt.Status == constant.StatusPending {
				return fmt.Errorf("sandbox runtime is %s, refuse to release pvc binding", rt.Status)
			}
		}

		ret := tx.Model(&model.SandboxPVCBinding{}).
			Where("sandbox_uuid = ? AND deleted = 0", sandboxUUID).
			Updates(map[string]interface{}{
				"status":        model.SandboxPVCStatusKill,
				"reclaim_after": reclaimAt,
				"update_time":   time.Now(),
			})
		if ret.Error != nil {
			return ret.Error
		}
		if ret.RowsAffected == 0 {
			return nil
		}
		return nil
	})
}

func ActivateSandboxPVCBinding(ctx context.Context, sandboxUUID string) error {
	return resource.OrmMain.WithContext(ctx).
		Model(&model.SandboxPVCBinding{}).
		Where("sandbox_uuid = ? AND deleted = 0", sandboxUUID).
		Updates(map[string]interface{}{
			"status":        model.SandboxPVCStatusActive,
			"reclaim_after": nil,
			"update_time":   time.Now(),
		}).Error
}

func MarkSandboxPVCBindingFailed(ctx context.Context, sandboxUUID string) error {
	return resource.OrmMain.WithContext(ctx).
		Model(&model.SandboxPVCBinding{}).
		Where("sandbox_uuid = ? AND deleted = 0", sandboxUUID).
		Updates(map[string]interface{}{
			"status":      model.SandboxPVCStatusFailed,
			"update_time": time.Now(),
		}).Error
}
