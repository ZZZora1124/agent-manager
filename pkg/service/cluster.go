package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/baidubce/bce-sdk-go/services/cce/v2/types"
	"gorm.io/gorm"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/cce"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/dns"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/noah"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/gdp/logit"
	"strings"
	"time"
)

type clusterService struct{}

func NewClusterService() ClusterService {
	return &clusterService{}
}

type ClusterService interface {
	// ExpandCluster 集群扩容
	ExpandCluster(ctx context.Context, req *ExpandClusterRequest) (*ExpandClusterResponse, error)

	// SyncClusterStatus 同步集群节点状态
	SyncClusterStatus(ctx context.Context, req *SyncClusterStatusRequest) (*SyncClusterStatusResponse, error)
}

// ExpandClusterRequest 扩容请求
type ExpandClusterRequest struct {
	ClusterID    string             `json:"clusterID" binding:"required"`   // 集群ID
	Count        int                `json:"count" binding:"required,min=1"` // 扩容节点数量
	BnsName      string             `json:"bnsName"`                        // BNS 名称
	InstanceSpec types.InstanceSpec `json:"instanceSpec"`
}

// ExpandClusterResponse 扩容响应
type ExpandClusterResponse struct {
	TaskID string             `json:"taskID"` // 任务ID
	Nodes  []*ClusterNodeInfo `json:"nodes"`  // 创建的节点列表
}

// ClusterNodeInfo 节点信息
type ClusterNodeInfo struct {
	HostID     int64  `json:"hostID"`     // 节点ID
	InstanceID string `json:"instanceID"` // 实例ID
	Status     string `json:"status"`     // 状态
}

// SyncClusterStatusRequest 同步状态请求
type SyncClusterStatusRequest struct {
	ClusterID string `json:"clusterID" binding:"required"` // 集群ID
}

// SyncClusterStatusResponse 同步状态响应
type SyncClusterStatusResponse struct {
	Synced  int `json:"synced"`  // 同步的节点数
	Ready   int `json:"ready"`   // 就绪的节点数
	Pending int `json:"pending"` // 处理中的节点数
}

// 节点状态常量
const (
	NodeStatusCreating = "creating" // 创建中
	NodeStatusRunning  = "running"  // 运行中
	NodeStatusReady    = "ready"    // 就绪
)

var idcMap = map[string]string{
	"bcc-bjyz":  "bjyz",
	"bcc-cq02":  "cq02",
	"bcc-bjrs":  "bjrs",
	"bcc-bjdd":  "bjdd",
	"bcc-bjlsh": "bjlsh",
	"bcc-gajl":  "gajl",
	"bcc-tjdm":  "tjdm",
}

// ExpandCluster 集群扩容
func (s *clusterService) ExpandCluster(ctx context.Context, req *ExpandClusterRequest) (*ExpandClusterResponse, error) {
	logger := resource.LoggerService
	logger.Notice(ctx, "expand cluster", logit.String("cluster_id", req.ClusterID), logit.Int("count", req.Count))

	// 获取 CCE 客户端
	cceApi := cce.GetCceApi()

	// 生成任务ID
	taskID := fmt.Sprintf("expand-%s-%d", req.ClusterID, time.Now().Unix())

	var nodes []*ClusterNodeInfo

	_, idcErr := getIdcByHostname(req.InstanceSpec.InstanceName)
	if idcErr != nil {
		logger.Warning(ctx, "get idc failed", logit.String("error", idcErr.Error()), logit.String("hostname", req.InstanceSpec.InstanceName))
		return nil, fmt.Errorf("get idc failed: %w", idcErr)
	}
	// 在事务中创建节点记录
	err := dao.Use(resource.OrmMain).Transaction(func(tx *dao.Query) error {
		for i := 0; i < req.Count; i++ {
			// 获取下一个 host_id
			hostID, err := getNextHostID(tx)
			if err != nil {
				logger.Error(ctx, "get next host_id failed", logit.String("error", err.Error()))
				return fmt.Errorf("get next host_id failed: %w", err)
			}

			// 构建实例规格
			instanceName := cce.FormatInstanceName(req.InstanceSpec.InstanceName, i)
			instanceSpec := req.InstanceSpec
			instanceSpec.InstanceName = instanceName

			// 调用 CCE API 创建实例
			result, err := cceApi.CreateInstance(req.ClusterID, &instanceSpec)
			if err != nil {
				logger.Error(ctx, "create cce instance failed", logit.String("error", err.Error()), logit.Int64("host_id", hostID))
				return fmt.Errorf("create cce instance failed: %w", err)
			}

			// 获取实例ID
			var instanceID string
			if len(result.CCEInstanceIDs) > 0 {
				instanceID = result.CCEInstanceIDs[0]
			} else {
				return fmt.Errorf("no instance id returned from cce")
			}

			// 创建节点记录
			node := &model.ClusterNode{
				HostID:        hostID,
				ClusterID:     req.ClusterID,
				InstanceID:    instanceID,
				InstanceName:  instanceName,
				Hostname:      instanceName,
				BnsName:       req.BnsName,
				Status:        NodeStatusCreating,
				InstancePhase: "",
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}

			nodeDao := tx.ClusterNode
			if err := nodeDao.Create(node); err != nil {
				logger.Error(ctx, "create cluster node record failed", logit.String("error", err.Error()), logit.Int64("host_id", hostID))
				return fmt.Errorf("create cluster node record failed: %w", err)
			}

			nodes = append(nodes, &ClusterNodeInfo{
				HostID:     hostID,
				InstanceID: instanceID,
				Status:     NodeStatusCreating,
			})

			logger.Notice(ctx, "create cluster node success", logit.Int64("host_id", hostID), logit.String("instance_id", instanceID))
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return &ExpandClusterResponse{
		TaskID: taskID,
		Nodes:  nodes,
	}, nil
}

// SyncClusterStatus 同步集群节点状态
func (s *clusterService) SyncClusterStatus(ctx context.Context, req *SyncClusterStatusRequest) (*SyncClusterStatusResponse, error) {
	logger := resource.LoggerService
	logger.Notice(ctx, "sync cluster status", logit.String("cluster_id", req.ClusterID))

	// 获取 CCE 客户端
	cceApi := cce.GetCceApi()
	noahApi := noah.GetNoahApi()
	dnsApi := dns.GetDnsApi()

	// 查询所有非 ready 状态的节点
	nodeDao := dao.Use(resource.OrmMain).ClusterNode
	nodes, err := nodeDao.Where(
		nodeDao.ClusterID.Eq(req.ClusterID),
		nodeDao.Status.Neq(NodeStatusReady),
	).Find()

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Error(ctx, "query cluster nodes failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("query cluster nodes failed: %w", err)
	}

	synced := 0
	ready := 0
	pending := len(nodes)

	// 处理每个节点
	for _, node := range nodes {
		// 查询实例状态
		result, err := cceApi.GetInstance(req.ClusterID, node.InstanceID)
		if err != nil {
			logger.Error(ctx, "get instance status failed", logit.String("error", err.Error()), logit.String("instance_id", node.InstanceID))
			continue
		}

		instance := result.Instance
		if instance == nil {
			logger.Warning(ctx, "instance not found", logit.String("instance_id", node.InstanceID))
			continue
		}

		// 更新节点状态
		instancePhase := instance.Status.InstancePhase
		node.InstancePhase = string(instancePhase)

		// 判断是否需要执行网络配置
		if cce.IsInstanceRunning(node.InstancePhase) && node.Status == NodeStatusCreating {
			// 网络配置成功，更新状态为 ready
			node.IP = instance.Status.Machine.VPCIP
			// 执行网络配置
			if configErr := configureNetwork(ctx, node, noahApi, dnsApi, logger); configErr != nil {
				logger.Error(ctx, "configure network failed", logit.String("error", configErr.Error()), logit.Int64("host_id", node.HostID))
				// 网络配置失败，保持 creating 状态，继续处理下一个节点
				continue
			} else {
				node.Status = NodeStatusReady
			}

			ready++
		} else if cce.IsInstanceRunning(node.InstancePhase) && node.Status == NodeStatusRunning {
			// 网络配置完成，更新状态为 ready
			node.Status = NodeStatusReady
			ready++
		}

		node.UpdatedAt = time.Now()

		// 更新数据库
		if err := nodeDao.Save(node); err != nil {
			logger.Error(ctx, "update cluster node failed", logit.String("error", err.Error()), logit.Int64("host_id", node.HostID))
			continue
		}

		synced++
		pending--
		logger.Notice(ctx, "sync cluster node success", logit.Int64("host_id", node.HostID), logit.String("status", node.Status))
	}

	return &SyncClusterStatusResponse{
		Synced:  synced,
		Ready:   ready,
		Pending: pending,
	}, nil
}

// getNextHostID 获取下一个 host_id
func getNextHostID(tx *dao.Query) (int64, error) {
	hostSeqDao := tx.HostSequence

	// 尝试获取现有记录
	seq, err := hostSeqDao.First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}

	// 如果不存在，创建新记录
	if errors.Is(err, gorm.ErrRecordNotFound) {
		seq = &model.HostSequence{
			CurrentHostID: 0,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		if err := hostSeqDao.Create(seq); err != nil {
			return 0, err
		}
	}

	// 获取下一个 host_id
	nextHostID := seq.CurrentHostID + 1

	// 更新 host_id
	seq.CurrentHostID = nextHostID
	seq.UpdatedAt = time.Now()
	if err := hostSeqDao.Save(seq); err != nil {
		return 0, err
	}

	return nextHostID, nil
}

func getIdcByHostname(hostname string) (string, error) {
	// 1. 绑定 host_id
	hostnameSplits := strings.Split(hostname, ".")
	if len(hostnameSplits) < 2 {
		return "", fmt.Errorf("invalid hostname format")
	}
	idc, ok := idcMap[hostnameSplits[1]]
	if !ok {
		return "", fmt.Errorf("invalid hostname format,idc not found")
	}
	return idc, nil
}

// configureNetwork 配置网络（host_id、BNS、DNS）
func configureNetwork(ctx context.Context, node *model.ClusterNode, noahApi *noah.Api, dnsApi *dns.Api, logger logit.Logger) error {

	var finalErr error = nil

	// 1. 绑定 host_id
	hostname := node.Hostname
	idc, err := getIdcByHostname(hostname)
	if err != nil {
		logger.Warning(ctx, "get idc failed", logit.String("error", err.Error()), logit.String("hostname", hostname))
		return err
	}

	if err := noahApi.BindHostID(node.HostID, hostname, idc); err != nil {
		logger.Warning(ctx, "bind host_id failed", logit.String("error", err.Error()), logit.Int64("host_id", node.HostID))
		finalErr = err
	} else {
		logger.Notice(ctx, "bind host_id success", logit.Int64("host_id", node.HostID), logit.String("hostname", hostname))
	}

	// 2. 绑定 BNS（示例，需要根据实际需求调整）
	bnsID := fmt.Sprintf("%d", node.HostID)
	bnsName := node.BnsName
	if err := noahApi.BindBNS(bnsID, bnsName); err != nil {
		logger.Warning(ctx, "bind bns failed", logit.String("error", err.Error()), logit.Int64("host_id", node.HostID))
		finalErr = err
	} else {
		logger.Notice(ctx, "bind bns success", logit.Int64("host_id", node.HostID), logit.String("bns", bnsName))
	}

	// 3. 绑定 DNS
	if node.IP != "" {
		if err := dnsApi.BindDNS(hostname, node.IP); err != nil {
			logger.Warning(ctx, "bind dns failed", logit.String("error", err.Error()), logit.Int64("host_id", node.HostID))
			finalErr = err
		} else {
			logger.Notice(ctx, "bind dns success", logit.Int64("host_id", node.HostID), logit.String("hostname", hostname), logit.String("ip", node.IP))
		}

		if err := dnsApi.BindPtrDNS(hostname, node.IP); err != nil {
			logger.Warning(ctx, "bind ptr dns failed", logit.String("error", err.Error()), logit.Int64("host_id", node.HostID))
			return nil
		} else {
			logger.Notice(ctx, "bind ptr dns success", logit.Int64("host_id", node.HostID), logit.String("hostname", hostname), logit.String("ip", node.IP))
		}

	}

	return finalErr
}

// UpsertHostSequence 初始化或更新 host_sequence 记录
func UpsertHostSequence(currentHostID int64) error {
	hostSeqDao := dao.Use(resource.OrmMain).HostSequence

	seq, err := hostSeqDao.First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		seq = &model.HostSequence{
			CurrentHostID: currentHostID,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		return hostSeqDao.Create(seq)
	}

	seq.CurrentHostID = currentHostID
	seq.UpdatedAt = time.Now()
	return hostSeqDao.Save(seq)
}

// GetClusterNode 获取集群节点
func GetClusterNode(hostID int64) (*model.ClusterNode, error) {
	nodeDao := dao.Use(resource.OrmMain).ClusterNode
	return nodeDao.Where(nodeDao.HostID.Eq(hostID)).First()
}

// GetClusterNodes 获取集群节点列表
func GetClusterNodes(clusterID string) ([]*model.ClusterNode, error) {
	nodeDao := dao.Use(resource.OrmMain).ClusterNode
	return nodeDao.Where(nodeDao.ClusterID.Eq(clusterID)).Find()
}

// UpdateClusterNode 更新集群节点
func UpdateClusterNode(node *model.ClusterNode) error {
	nodeDao := dao.Use(resource.OrmMain).ClusterNode
	return nodeDao.Save(node)
}
