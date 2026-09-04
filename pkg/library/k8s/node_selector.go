package k8s

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// 评分权重：沙箱场景内存更关键（OOM 比 CPU throttle 影响更大）
	cpuWeight = 0.4
	memWeight = 0.6
)

// NodeResourceInfo 描述单个节点的资源概况与空闲评分。
type NodeResourceInfo struct {
	NodeName      string  `json:"nodeName"`
	Schedulable   bool    `json:"schedulable"`
	AllocCPUMilli int64   `json:"allocCpuMilli"` // Allocatable CPU（毫核）
	AllocMemBytes int64   `json:"allocMemBytes"` // Allocatable 内存（字节）
	UsedCPUMilli  int64   `json:"usedCpuMilli"`  // 已请求 CPU（毫核）
	UsedMemBytes  int64   `json:"usedMemBytes"`  // 已请求内存（字节）
	AvailCPUMilli int64   `json:"availCpuMilli"` // 可用 CPU（毫核）
	AvailMemBytes int64   `json:"availMemBytes"` // 可用内存（字节）
	CPURatio      float64 `json:"cpuRatio"`      // CPU 空闲率 (0~1)
	MemRatio      float64 `json:"memRatio"`      // 内存空闲率 (0~1)
	Score         float64 `json:"score"`         // 综合评分 (0~1)
	PodCount      int     `json:"podCount"`      // 活跃 Pod 数
}

// GetNodeResourceList 返回集群中所有节点的资源概况，按评分降序排列。
func (cluster *Cluster) GetNodeResourceList(ctx context.Context) ([]NodeResourceInfo, error) {
	// 1. 获取所有节点
	nodeList, err := cluster.clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	// 2. 获取所有活跃 Pod（排除 Failed/Succeeded）
	podList, err := cluster.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase!=Failed,status.phase!=Succeeded",
	})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}

	// 3. 按节点累加 Pod 的 resource.requests 和 Pod 计数
	usageByNode, podCountByNode := aggregatePodResources(podList)

	// 4. 构建每个节点的资源信息
	result := make([]NodeResourceInfo, 0, len(nodeList.Items))
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		info := buildNodeResourceInfo(node, usageByNode, podCountByNode)
		result = append(result, info)
	}

	// 5. 按评分降序排列
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})

	return result, nil
}

// SelectLeastLoadedNode 选择集群中最空闲的可调度节点。
// excludeNodes: 需要排除的节点名集合（如源节点）。
func (cluster *Cluster) SelectLeastLoadedNode(ctx context.Context, excludeNodes map[string]bool) (string, error) {
	// 1. 获取所有节点
	nodeList, err := cluster.clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}

	// 2. 过滤可调度节点
	var schedulableNodes []corev1.Node
	for i := range nodeList.Items {
		if isNodeSchedulable(&nodeList.Items[i], excludeNodes) {
			schedulableNodes = append(schedulableNodes, nodeList.Items[i])
		}
	}
	if len(schedulableNodes) == 0 {
		return "", fmt.Errorf("no schedulable node available")
	}

	// 3. 获取所有活跃 Pod（排除 Failed/Succeeded）
	podList, err := cluster.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase!=Failed,status.phase!=Succeeded",
	})
	if err != nil {
		return "", fmt.Errorf("list pods: %w", err)
	}

	// 4. 按节点累加 Pod 的 resource.requests
	usageByNode, _ := aggregatePodResources(podList)

	// 5. 计算评分，选择最空闲节点
	bestScore := -1.0
	bestNode := ""
	for i := range schedulableNodes {
		node := &schedulableNodes[i]
		info := buildNodeResourceInfo(node, usageByNode, nil)
		if info.Score > bestScore {
			bestScore = info.Score
			bestNode = node.Name
		}
	}

	return bestNode, nil
}

// nodeUsage 记录单个节点上所有 Pod 累加的 resource.requests。
type nodeUsage struct {
	cpuMillis int64
	memBytes  int64
}

// aggregatePodResources 按节点累加所有 Pod 的 resource.requests，返回用量和 Pod 计数。
func aggregatePodResources(podList *corev1.PodList) (map[string]*nodeUsage, map[string]int) {
	usageByNode := make(map[string]*nodeUsage)
	podCountByNode := make(map[string]int)

	for i := range podList.Items {
		pod := &podList.Items[i]
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}
		podCountByNode[nodeName]++

		usage, ok := usageByNode[nodeName]
		if !ok {
			usage = &nodeUsage{}
			usageByNode[nodeName] = usage
		}

		// containers: 累加
		var containerCPU, containerMem int64
		for j := range pod.Spec.Containers {
			req := pod.Spec.Containers[j].Resources.Requests
			containerCPU += req.Cpu().MilliValue()
			containerMem += req.Memory().Value()
		}

		// initContainers: 取最大值（与 K8s scheduler 一致）
		var maxInitCPU, maxInitMem int64
		for j := range pod.Spec.InitContainers {
			req := pod.Spec.InitContainers[j].Resources.Requests
			if v := req.Cpu().MilliValue(); v > maxInitCPU {
				maxInitCPU = v
			}
			if v := req.Memory().Value(); v > maxInitMem {
				maxInitMem = v
			}
		}

		// Pod 的有效 request = max(sum(containers), max(initContainers))
		podCPU := containerCPU
		if maxInitCPU > podCPU {
			podCPU = maxInitCPU
		}
		podMem := containerMem
		if maxInitMem > podMem {
			podMem = maxInitMem
		}

		usage.cpuMillis += podCPU
		usage.memBytes += podMem
	}

	return usageByNode, podCountByNode
}

// buildNodeResourceInfo 根据节点和用量数据构建 NodeResourceInfo。
func buildNodeResourceInfo(node *corev1.Node, usageByNode map[string]*nodeUsage, podCountByNode map[string]int) NodeResourceInfo {
	allocCPU := node.Status.Allocatable.Cpu().MilliValue()
	allocMem := node.Status.Allocatable.Memory().Value()

	usage := usageByNode[node.Name]
	var usedCPU, usedMem int64
	if usage != nil {
		usedCPU = usage.cpuMillis
		usedMem = usage.memBytes
	}

	// 可用量，兜底为 0
	availCPU := allocCPU - usedCPU
	if availCPU < 0 {
		availCPU = 0
	}
	availMem := allocMem - usedMem
	if availMem < 0 {
		availMem = 0
	}

	// 归一化并计算加权评分，除零保护
	var cpuRatio, memRatio float64
	if allocCPU > 0 {
		cpuRatio = float64(availCPU) / float64(allocCPU)
	}
	if allocMem > 0 {
		memRatio = float64(availMem) / float64(allocMem)
	}
	score := cpuWeight*cpuRatio + memWeight*memRatio

	schedulable := isNodeSchedulable(node, nil)

	podCount := 0
	if podCountByNode != nil {
		podCount = podCountByNode[node.Name]
	}

	return NodeResourceInfo{
		NodeName:      node.Name,
		Schedulable:   schedulable,
		AllocCPUMilli: allocCPU,
		AllocMemBytes: allocMem,
		UsedCPUMilli:  usedCPU,
		UsedMemBytes:  usedMem,
		AvailCPUMilli: availCPU,
		AvailMemBytes: availMem,
		CPURatio:      cpuRatio,
		MemRatio:      memRatio,
		Score:         score,
		PodCount:      podCount,
	}
}

// isNodeSchedulable 判断节点是否可调度
func isNodeSchedulable(node *corev1.Node, excludeNodes map[string]bool) bool {
	// 排除指定节点
	if len(excludeNodes) > 0 && excludeNodes[node.Name] {
		return false
	}

	// cordoned 节点不可调度
	if node.Spec.Unschedulable {
		return false
	}

	// NodeReady condition 不为 True 则排除
	ready := false
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			ready = cond.Status == corev1.ConditionTrue
			break
		}
	}
	if !ready {
		return false
	}

	// 带 NoSchedule 或 NoExecute taint 的节点排除
	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			return false
		}
	}

	return true
}
