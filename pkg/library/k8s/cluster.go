package k8s

import (
	"context"
	"fmt"
	"icode.baidu.com/baidu/gdp/redis"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"sync"
	"time"
)

type Cluster struct {
	namespace     string
	kubeConfig    string
	networkType   string
	nginxEndpoint string
	clientSet     *kubernetes.Clientset
	PodEvents     map[string]chan PodEvent
	redisClient   redis.Client
	lock          sync.Mutex
}

func NewCluster(kubeConfig string, namespace string, networkType string, nginxEndpoint string, redisClient redis.Client) *Cluster {
	config, err := clientcmd.Load([]byte(kubeConfig))
	if err != nil {
		panic(err.Error())
	}
	restConfig, err := clientcmd.NewDefaultClientConfig(*config, nil).ClientConfig()
	if err != nil {
		panic(err.Error())
	}
	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		panic(err.Error())
	}
	cluster := &Cluster{
		namespace:     namespace,
		kubeConfig:    kubeConfig,
		networkType:   networkType,
		nginxEndpoint: nginxEndpoint,
		clientSet:     clientSet,
		PodEvents:     make(map[string]chan PodEvent),
		redisClient:   redisClient,
	}
	cluster.startController()
	return cluster
}

func (cluster *Cluster) RegisterSandboxEvent(sandboxUuid string, eventChan chan PodEvent) {
	cluster.lock.Lock()
	cluster.PodEvents[sandboxUuid] = eventChan
	cluster.lock.Unlock()
}

func (cluster *Cluster) UnRegisterSandboxEvent(sandboxUuid string) {
	cluster.lock.Lock()
	delete(cluster.PodEvents, sandboxUuid)
	cluster.lock.Unlock()
}

func (cluster *Cluster) startController() {
	// 创建Pod的Informer
	podListWatcher := cache.NewListWatchFromClient(
		cluster.clientSet.CoreV1().RESTClient(),
		"pods",
		cluster.namespace,
		fields.Everything(),
	)

	// 设置EventHandler
	podInformer := cache.NewSharedIndexInformer(
		podListWatcher,
		&corev1.Pod{},
		0, // Skip resync
		cache.Indexers{},
	)

	// 设置事件处理程序
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			fmt.Printf("%v pod added: %s/%s\n", time.Now().Format(time.RFC3339), pod.Namespace, pod.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod := oldObj.(*corev1.Pod)
			newPod := newObj.(*corev1.Pod)
			fmt.Printf("%v pod updated,namespace %s podname: %s oldStatus: %s newStatus: %s\n",
				time.Now().Format(time.RFC3339), newPod.Namespace, newPod.Name, oldPod.Status.Phase, newPod.Status.Phase)

			// 使用 LABEL(sandbox-id) 匹配而不是 Pod 名称
			sandboxId := newPod.Labels["sandbox-id"]
			if sandboxId == "" {
				return
			}

			if newPod.Status.Phase == corev1.PodRunning {
				podEventChan := cluster.PodEvents[sandboxId]
				if podEventChan != nil {
					if allContainersReady(newPod) {
						podEventChan <- PodEvent{
							PodName:     newPod.Name,
							PortIp:      newPod.Status.PodIP,
							NodeIp:      newPod.Status.HostIP,
							CommandPort: 49983,
							CodePort:    49999,
							PodPhase:    newPod.Status.Phase,
						}
						// 发送后就移除
						cluster.PodEvents[sandboxId] = nil
					}
				}
			} else if newPod.Status.Phase == corev1.PodFailed {
				podEventChan := cluster.PodEvents[sandboxId]
				if podEventChan != nil {
					podEventChan <- PodEvent{
						PodName:     newPod.Name,
						PortIp:      newPod.Status.PodIP,
						NodeIp:      newPod.Status.HostIP,
						CommandPort: 0,
						CodePort:    0,
						PodPhase:    newPod.Status.Phase,
						ErrMsg:      newPod.Status.Message,
					}
					// 发送后就移除
					cluster.PodEvents[sandboxId] = nil

				}
			} else if newPod.Status.Phase == corev1.PodSucceeded {
				podEventChan := cluster.PodEvents[sandboxId]
				if podEventChan != nil {
					podEventChan <- PodEvent{
						PodName:     newPod.Name,
						PortIp:      newPod.Status.PodIP,
						NodeIp:      newPod.Status.HostIP,
						CommandPort: 0,
						CodePort:    0,
						PodPhase:    newPod.Status.Phase,
					}
					// 发送后就移除
					cluster.PodEvents[sandboxId] = nil
				}

			}
		},
		DeleteFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			fmt.Printf("%v pod deleted: %s/%s\n", time.Now().Format(time.RFC3339), pod.Namespace, pod.Name)
		},
	})
	// 启动Informer
	stopCh := make(chan struct{})
	go podInformer.Run(stopCh)
}

func (cluster *Cluster) DeletePod(name string) error {

	var gracePeriod int64 = 0
	podsClient := cluster.clientSet.CoreV1().Pods(cluster.namespace)
	err := podsClient.Delete(context.TODO(), name, metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod})
	return err
}

func (cluster *Cluster) DeleteDeployment(name string) error {
	deploymentsClient := cluster.clientSet.AppsV1().Deployments(cluster.namespace)
	err := deploymentsClient.Delete(context.TODO(), name, metav1.DeleteOptions{})
	return err
}

func (cluster *Cluster) GetClientSet() *kubernetes.Clientset {
	return cluster.clientSet
}

func (cluster *Cluster) GetNamespace() string {
	return cluster.namespace
}

func (cluster *Cluster) GetNetworkType() string {
	return cluster.networkType
}

func (cluster *Cluster) GetNginxEndpoint() string {
	return cluster.nginxEndpoint
}

// RunningPodInfo 运行中 pod 的基本信息
type RunningPodInfo struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListRunningPods 获取集群下所有运行中的 pod 列表及其创建时间
func (cluster *Cluster) ListRunningPods(ctx context.Context) ([]RunningPodInfo, error) {
	podList, err := cluster.clientSet.CoreV1().Pods(cluster.namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Running",
	})
	if err != nil {
		return nil, err
	}

	result := make([]RunningPodInfo, 0, len(podList.Items))
	for _, pod := range podList.Items {
		result = append(result, RunningPodInfo{
			Name:      pod.Name,
			CreatedAt: pod.CreationTimestamp.Time,
		})
	}
	return result, nil
}
