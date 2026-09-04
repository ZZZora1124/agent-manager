package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
	"icode.baidu.com/baidu/gdp/logit"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PreheatService 镜像预热服务
type PreheatService struct{}

// CreatePreheatRequest 创建预热请求
type CreatePreheatRequest struct {
	ImageURL       string            `json:"imageURL" binding:"required"`
	ClusterList    []string          `json:"clusterList" binding:"required"`
	NodeSelector   map[string]string `json:"nodeSelector"`
	MaxConcurrency int               `json:"maxConcurrency" binding:"min=1,max=1000"`
}

// PreheatJobResponse 预热任务响应
type PreheatJobResponse struct {
	JobID          uint64            `json:"jobId"`
	JobName        string            `json:"jobName"`
	ImageURL       string            `json:"imageUrl"`
	ClusterList    []string          `json:"clusterList"`
	NodeSelector   map[string]string `json:"nodeSelector"`
	MaxConcurrency int               `json:"maxConcurrency"`
	Status         string            `json:"status"`
	TotalNodes     int               `json:"totalNodes"`
	SuccessNodes   int               `json:"successNodes"`
	FailedNodes    int               `json:"failedNodes"`
	CreateTime     time.Time         `json:"createTime"`
	StartTime      *time.Time        `json:"startTime"`
	EndTime        *time.Time        `json:"endTime"`
}

// CreatePreheatJob 创建镜像预热任务
func (s *PreheatService) CreatePreheatJob(ctx context.Context, req *CreatePreheatRequest) (*PreheatJobResponse, error) {
	logger := resource.LoggerService

	logger.Notice(ctx, "creating preheat job",
		logit.String("imageURL", req.ImageURL),
		logit.Int("clusterCount", len(req.ClusterList)),
		logit.Int("maxConcurrency", req.MaxConcurrency))

	// 生成job名称
	jobName := fmt.Sprintf("preheat-%s", time.Now().Format("20060102150405"))

	// 序列化cluster_list和node_selector
	clusterListJSON, err := json.Marshal(req.ClusterList)
	if err != nil {
		logger.Error(ctx, "marshal cluster list failed", logit.String("error", err.Error()))
		return nil, response.ErrParams
	}

	nodeSelectorJSON, err := json.Marshal(req.NodeSelector)
	if err != nil {
		logger.Error(ctx, "marshal node selector failed", logit.String("error", err.Error()))
		return nil, response.ErrParams
	}

	// 获取集群信息并检查
	totalNodes := 0
	for _, clusterName := range req.ClusterList {
		cluster := resource.ClusterManager.GetClusterByName(clusterName)
		if cluster == nil {
			logger.Error(ctx, "cluster not found", logit.String("cluster", clusterName))
			return nil, response.ClusterNotFoundErr
		}

		// 获取匹配的节点数量
		nodes, err := s.getMatchingNodes(ctx, cluster, req.NodeSelector)
		if err != nil {
			logger.Error(ctx, "get matching nodes failed",
				logit.String("cluster", clusterName),
				logit.String("error", err.Error()))
			return nil, response.K8sOperateErr
		}
		totalNodes += len(nodes)
	}

	if totalNodes == 0 {
		logger.Error(ctx, "no matching nodes found",
			logit.String("imageURL", req.ImageURL))
		return nil, response.ErrParams
	}

	logger.Notice(ctx, "found matching nodes for preheat",
		logit.String("imageURL", req.ImageURL),
		logit.Int("totalNodes", totalNodes))

	// 保存任务信息到数据库
	preheatJobDao := dao.Use(resource.OrmMain).ImagePreheatJob
	preheatJob := &model.ImagePreheatJob{
		AppID:          0,
		JobName:        jobName,
		ImageURL:       req.ImageURL,
		ClusterList:    string(clusterListJSON),
		NodeSelector:   string(nodeSelectorJSON),
		MaxConcurrency: int32(req.MaxConcurrency),
		Status:         constant.StatusPending,
		TotalNodes:     int32(totalNodes),
		SuccessNodes:   0,
		FailedNodes:    0,
		CreateTime:     time.Now(),
	}

	err = preheatJobDao.Create(preheatJob)
	if err != nil {
		logger.Error(ctx, "create preheat job failed",
			logit.String("jobName", jobName),
			logit.String("error", err.Error()))
		return nil, response.QueryDbErr
	}

	logger.Notice(ctx, "preheat job created successfully",
		logit.Int64("jobID", preheatJob.ID),
		logit.String("jobName", jobName))

	// 异步处理任务
	go s.processPreheatJob(preheatJob.ID, req)

	return &PreheatJobResponse{
		JobID:          uint64(preheatJob.ID),
		JobName:        jobName,
		ImageURL:       req.ImageURL,
		ClusterList:    req.ClusterList,
		NodeSelector:   req.NodeSelector,
		MaxConcurrency: req.MaxConcurrency,
		Status:         constant.StatusPending,
		TotalNodes:     totalNodes,
		SuccessNodes:   0,
		FailedNodes:    0,
		CreateTime:     preheatJob.CreateTime,
		StartTime:      nil,
		EndTime:        nil,
	}, nil
}

// getMatchingNodes 获取匹配的节点
func (s *PreheatService) getMatchingNodes(ctx context.Context, cluster *k8s.Cluster, nodeSelector map[string]string) ([]corev1.Node, error) {
	// 获取所有节点
	nodes, err := cluster.GetClientSet().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var matchingNodes []corev1.Node
	for _, node := range nodes.Items {
		// 检查节点是否匹配nodeSelector

		// 检查节点状态
		isReady := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				isReady = condition.Status == corev1.ConditionTrue
				break
			}
		}

		if s.nodeMatchesSelector(&node, nodeSelector) && isReady == true {
			matchingNodes = append(matchingNodes, node)
		}
	}

	return matchingNodes, nil
}

// nodeMatchesSelector 检查节点是否匹配选择器
func (s *PreheatService) nodeMatchesSelector(node *corev1.Node, selector map[string]string) bool {
	if len(selector) == 0 {
		return true // 如果没有选择器，匹配所有节点
	}

	for key, value := range selector {
		if nodeValue, exists := node.Labels[key]; !exists || nodeValue != value {
			return false
		}
	}
	return true
}

// processPreheatJob 处理预热任务（异步）
func (s *PreheatService) processPreheatJob(jobID int64, req *CreatePreheatRequest) {
	ctx := context.Background()
	logger := resource.LoggerService

	logger.Notice(ctx, "starting preheat job processing",
		logit.Int64("jobID", jobID),
		logit.String("imageURL", req.ImageURL),
		logit.Int("clusterCount", len(req.ClusterList)))

	// 更新任务状态为running
	preheatJobDao := dao.Use(resource.OrmMain).ImagePreheatJob
	now := time.Now()
	_, err := preheatJobDao.Where(preheatJobDao.ID.Eq(jobID)).Updates(map[string]interface{}{
		"status":     constant.StatusRunning,
		"start_time": &now,
	})
	if err != nil {
		logger.Error(ctx, "update job status to running failed",
			logit.Int64("jobID", jobID),
			logit.String("error", err.Error()))
		return
	}

	logger.Notice(ctx, "preheat job status updated to running",
		logit.Int64("jobID", jobID))

	successCount := 0
	failedCount := 0

	// 为每个集群创建预热任务
	for _, clusterName := range req.ClusterList {
		logger.Notice(ctx, "processing cluster for preheat",
			logit.Int64("jobID", jobID),
			logit.String("cluster", clusterName))
		cluster := resource.ClusterManager.GetClusterByName(clusterName)
		if cluster == nil {
			logger.Error(ctx, "cluster not found during processing",
				logit.String("cluster", clusterName))
			continue
		}

		// 获取匹配的节点
		nodes, err := s.getMatchingNodes(ctx, cluster, req.NodeSelector)
		if err != nil {
			logger.Error(ctx, "get matching nodes failed",
				logit.String("cluster", clusterName),
				logit.String("error", err.Error()))
			continue
		}

		logger.Notice(ctx, "found matching nodes for cluster",
			logit.Int64("jobID", jobID),
			logit.String("cluster", clusterName),
			logit.Int("nodeCount", len(nodes)))

		// 为每个节点创建预热任务
		for _, node := range nodes {
			if err := s.createNodePreheatJob(ctx, jobID, clusterName, &node, req); err != nil {
				logger.Error(ctx, "create node preheat job failed",
					logit.String("cluster", clusterName),
					logit.String("node", node.Name),
					logit.String("error", err.Error()))
				failedCount++
			} else {
				successCount++
			}

			// 控制并发数
			if successCount+failedCount >= req.MaxConcurrency {
				break
			}
		}
	}

	// 更新任务状态和结果
	endTime := time.Now()
	finalStatus := constant.StatusSucceeded
	if failedCount > 0 {
		finalStatus = constant.StatusFailed
	}

	_, err = preheatJobDao.Where(preheatJobDao.ID.Eq(jobID)).Updates(map[string]interface{}{
		"status":        finalStatus,
		"end_time":      &endTime,
		"success_nodes": successCount,
		"failed_nodes":  failedCount,
	})
	if err != nil {
		logger.Error(ctx, "update job final status failed",
			logit.Int64("jobID", jobID),
			logit.String("error", err.Error()))
	}

	logger.Notice(ctx, "preheat job completed",
		logit.Int64("jobID", jobID),
		logit.String("status", finalStatus),
		logit.Int("success_nodes", successCount),
		logit.Int("failed_nodes", failedCount),
		logit.Int("total_nodes", successCount+failedCount))
}

func getPullScript(username string, password string, imageURL string) string {
	return fmt.Sprintf("nerdctl login -u %s -p %s iregistry.baidu-int.com\n", username, password) +
		fmt.Sprintf("nerdctl -n k8s.io pull %s\n", imageURL)
}

// createNodePreheatJob 为单个节点创建预热任务
func (s *PreheatService) createNodePreheatJob(ctx context.Context, jobID int64, clusterName string, node *corev1.Node, req *CreatePreheatRequest) error {
	logger := resource.LoggerService
	cluster := resource.ClusterManager.GetClusterByName(clusterName)
	if cluster == nil {
		return fmt.Errorf("cluster not found: %s", clusterName)
	}

	clientSet := cluster.GetClientSet()
	namespace := cluster.GetNamespace()

	// 创建独一无二的job名称
	jobName := fmt.Sprintf("preheat-node-%s-%d", node.Name, time.Now().Unix())

	// 创建预热记录
	preheatRecordDao := dao.Use(resource.OrmMain).ImagePreheatRecord
	preheatRecord := &model.ImagePreheatRecord{
		JobID:       jobID,
		JobName:     jobName,
		ClusterName: clusterName,
		NodeName:    node.Name,
		NodeIP:      getNodeIP(node),
		ImageURL:    req.ImageURL,
		Status:      constant.StatusPending,
		CreateTime:  time.Now(),
	}
	err := preheatRecordDao.Create(preheatRecord)
	if err != nil {
		return fmt.Errorf("create preheat record failed: %w", err)
	}

	// 创建K8s Job
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":     "image-preheat",
				"job-id":  fmt.Sprintf("%d", jobID),
				"cluster": clusterName,
				"node":    node.Name,
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &[]int32{600}[0],
			ActiveDeadlineSeconds:   &[]int64{3600}[0],
			BackoffLimit:            &[]int32{1}[0],
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":     "image-preheat",
						"job-id":  fmt.Sprintf("%d", jobID),
						"cluster": clusterName,
						"node":    node.Name,
					},
				},
				Spec: corev1.PodSpec{
					NodeName:      node.Name,
					RestartPolicy: corev1.RestartPolicyNever,
					HostNetwork:   true,
					Containers: []corev1.Container{
						{
							Name:  "preheat",
							Image: constant.PreHotImage,
							Command: []string{
								"bash", "-c",
								getPullScript(constant.IRegistryUsername, constant.IRegistryPassword, req.ImageURL),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "containerd-socket",
									MountPath: "/run/containerd/containerd.sock",
								},
								{
									Name:      "containerd-config",
									MountPath: "/etc/containerd",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "containerd-socket",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/run/containerd/containerd.sock",
								},
							},
						},
						{
							Name: "containerd-config",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/etc/containerd",
								},
							},
						},
					},
				},
			},
		},
	}

	logger.Notice(ctx, "creating node preheat job",
		logit.Int64("jobID", jobID),
		logit.String("cluster", clusterName),
		logit.String("node", node.Name),
		logit.String("jobName", jobName))

	// 创建job
	_, err = clientSet.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		// 更新记录状态为failed
		preheatRecordDao.Where(preheatRecordDao.ID.Eq(preheatRecord.ID)).Updates(map[string]interface{}{
			"status":     constant.StatusFailed,
			"error_msg":  err.Error(),
			"start_time": time.Now(),
			"end_time":   time.Now(),
		})
		return fmt.Errorf("create k8s job failed: %w", err)
	}

	// 更新记录状态和pod名称
	preheatRecordDao.Where(preheatRecordDao.ID.Eq(preheatRecord.ID)).Updates(map[string]interface{}{
		"status":     constant.StatusRunning,
		"pod_name":   jobName, // pod名称与job名称相同
		"start_time": time.Now(),
	})

	logger.Notice(ctx, "created node preheat job",
		logit.String("jobName", jobName),
		logit.String("cluster", clusterName),
		logit.String("node", node.Name))

	return nil
}

// getNodeIP 获取节点IP
func getNodeIP(node *corev1.Node) string {
	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeInternalIP {
			return address.Address
		}
	}
	return ""
}
