package service

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/envvar"
	"time"

	corev1 "k8s.io/api/core/v1"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
)

type AppPvcInstanceService interface {
	CreateAppPvcInstance(ctx context.Context, appID int32, name string, size string) (*model.AppPvcInstance, error)
	DeleteAppPvcInstance(ctx context.Context, appID int32, instanceID int64) error
	ListAppPvcInstances(ctx context.Context, appID int32) ([]*model.AppPvcInstance, error)
}

type appPvcInstanceService struct{}

func NewAppPvcInstanceService() AppPvcInstanceService {
	return &appPvcInstanceService{}
}

const (
	pvcBindPollInterval = 3 * time.Second
	pvcBindPollTimeout  = 5 * time.Minute
)

func (s *appPvcInstanceService) CreateAppPvcInstance(ctx context.Context, appID int32, name string, size string) (*model.AppPvcInstance, error) {
	instanceDao := dao.Use(resource.OrmMain).AppPvcInstance
	// check pvcName uniqueness
	count, err := instanceDao.Where(instanceDao.AppID.Eq(appID), instanceDao.Name.Eq(name), instanceDao.Deleted.Is(false)).Count()
	if err != nil {
		return nil, fmt.Errorf("query pvc instance failed: %w", err)
	}
	if count > 0 {
		return nil, fmt.Errorf("name %s already exists", name)
	}

	// determine access mode by storage class
	accessMode := corev1.ReadWriteMany

	storageClass := SelectCFSStorageClass(name)

	clusters := envvar.GetBroadCastCfsClusters()

	initClusterName := clusters[0]
	cluster := resource.ClusterManager.GetClusterByName(initClusterName)
	if cluster == nil {
		return nil, fmt.Errorf("cluster %s not found", initClusterName)
	}

	pvcName := fmt.Sprintf("app-%d-%s", appID, uuid.New().String())

	// create PVC via k8s
	if err = cluster.CreateDynamicPVCWithAccessMode(ctx, pvcName, storageClass, size, accessMode); err != nil {
		return nil, fmt.Errorf("create pvc failed: %w", err)
	}

	// poll until PVC is bound (PV name available)
	pvName, err := waitForPVCBound(ctx, cluster, pvcName)
	if err != nil {
		// cleanup PVC on failure
		_ = cluster.DeletePVC(ctx, pvcName)
		return nil, fmt.Errorf("wait for pvc bound failed: %w", err)
	}

	pv, err := cluster.GetPV(ctx, pvName)
	if err != nil {
		return nil, fmt.Errorf("get pv failed: %w", err)
	}
	// 从初始集群里,复制pv和pvc
	for _, targetClusterName := range clusters[1:] {
		cpErr := MigratePVCToNewCluster(ctx, pvcName, initClusterName, targetClusterName)
		if cpErr != nil {
			return nil, fmt.Errorf("migrate pvc failed: %w", cpErr)
		}
	}
	// build subDir from pvName
	subDir := pv.Spec.NFS.Path

	instance := &model.AppPvcInstance{
		AppID:        appID,
		Name:         name,
		StorageClass: storageClass,
		PvcName:      pvcName,
		PvName:       pvName,
		SubDir:       subDir,
		Deleted:      false,
	}
	if err = instanceDao.Create(instance); err != nil {
		_ = cluster.DeletePVC(ctx, pvcName)
		return nil, fmt.Errorf("save pvc instance to db failed: %w", err)
	}

	return instance, nil
}

func (s *appPvcInstanceService) DeleteAppPvcInstance(ctx context.Context, appID int32, instanceID int64) error {
	instanceDao := dao.Use(resource.OrmMain).AppPvcInstance

	instance, err := instanceDao.Where(instanceDao.ID.Eq(instanceID), instanceDao.Deleted.Is(false)).First()
	if err != nil {
		return fmt.Errorf("pvc instance not found: %w", err)
	}
	if instance.AppID != appID {
		return fmt.Errorf("appID not match")
	}

	// soft delete in DB
	_, err = instanceDao.Where(instanceDao.ID.Eq(instanceID)).UpdateSimple(
		instanceDao.Deleted.Value(true),
	)
	if err != nil {
		return fmt.Errorf("delete pvc instance failed: %w", err)
	}

	// delete PVC in k8s
	clusters := envvar.GetBroadCastCfsClusters()
	for _, clusterName := range clusters {
		cluster := resource.ClusterManager.GetClusterByName(clusterName)
		if cluster != nil {
			_ = cluster.DeletePVC(ctx, instance.PvcName)
		}
	}
	return nil
}

func (s *appPvcInstanceService) ListAppPvcInstances(ctx context.Context, appID int32) ([]*model.AppPvcInstance, error) {
	instanceDao := dao.Use(resource.OrmMain).AppPvcInstance
	return instanceDao.Where(instanceDao.AppID.Eq(appID), instanceDao.Deleted.Is(false)).Find()
}

// waitForPVCBound polls until the PVC is bound and returns the PV name.
func waitForPVCBound(ctx context.Context, cluster *k8s.Cluster, pvcName string) (string, error) {
	deadline := time.Now().Add(pvcBindPollTimeout)
	for time.Now().Before(deadline) {
		pvName, err := cluster.GetPVNameByPVC(ctx, pvcName)
		if err != nil {
			return "", err
		}
		if pvName != "" {
			return pvName, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pvcBindPollInterval):
		}
	}
	return "", fmt.Errorf("timeout waiting for pvc %s to be bound", pvcName)
}

// GetAppPvcInstanceByName queries a non-deleted AppPvcInstance by pvcName.
func GetAppPvcInstanceByName(ctx context.Context, appID int32, name string) (*model.AppPvcInstance, error) {
	instanceDao := dao.Use(resource.OrmMain).AppPvcInstance
	return instanceDao.Where(
		instanceDao.AppID.Eq(appID),
		instanceDao.Name.Eq(name), instanceDao.Deleted.Is(false)).First()
}

// PvcMountInfo is a simplified struct for merging into BindPvcs.
type PvcMountInfo struct {
	Name      string `json:"name"`
	SubPath   string `json:"subPath"`
	MountPath string `json:"mountPath"`
}
