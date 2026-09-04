package main

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// 你的Kubeconfig字符串（假设它是一个YAML格式的字符串）
	kubeconfigStr := `
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURuakNDQW9hZ0F3SUJBZ0lVYmpQaVpoWnYrT3hORmhyM21WUE8wTDJxRWI4d0RRWUpLb1pJaHZjTkFRRUwKQlFBd1p6RUxNQWtHQTFVRUJoTUNRMDR4RURBT0JnTlZCQWdUQjBKbGFVcHBibWN4RURBT0JnTlZCQWNUQjBKbAphVXBwYm1jeERqQU1CZ05WQkFvVEJXcHdZV0Z6TVJRd0VnWURWUVFMRXd0amJHOTFaRzVoZEdsMlpURU9NQXdHCkExVUVBeE1GYW5CaFlYTXdIaGNOTWpFd056SXpNREl3T1RBd1doY05NekV3TnpJeE1ESXdPVEF3V2pCbk1Rc3cKQ1FZRFZRUUdFd0pEVGpFUU1BNEdBMVVFQ0JNSFFtVnBTbWx1WnpFUU1BNEdBMVVFQnhNSFFtVnBTbWx1WnpFTwpNQXdHQTFVRUNoTUZhbkJoWVhNeEZEQVNCZ05WQkFzVEMyTnNiM1ZrYm1GMGFYWmxNUTR3REFZRFZRUURFd1ZxCmNHRmhjekNDQVNJd0RRWUpLb1pJaHZjTkFRRUJCUUFEZ2dFUEFEQ0NBUW9DZ2dFQkFMbXBuK1lzRXBrS1VZUTMKTTZkRlNkbUFQa0lsU3JMV1ZZSitmMEM2OU9jOFVjVkQ2dGJGUUtnWnduNTY2Yy90emtHaWZxMlNtM2NUTHFaVQpuQ1NGV2FjOVByb2RzbnhYTmNleU5QTW9JVmorNDE4cXRQS3BDZXBSNUVSSUZKT09TN05McGdqa1V6RUY3NW9hCjlheGhrbHU3SVhpdzJnS2FlYkNpdnhxRXdybVVzaWh6aUhjblFFWFRLZmJ3U09WTmRwV0JjNUQyd2hoYTRSZUsKWmpkaTNrSE51T0RXUVoyU2ZQVEFlVE1ucW9LVCtSVFZyOEtXdnhNSzFGczJEL0tYL2NCNVBnK2hBaXVTazBKSwpZY2JyWUlndnpnQmVTRkNVRzhrQ1diMU5tUC9aTHczMTZyY0Vld0U3Tm1UM0ZoQ3htSmhzSXl3clNhR1B3VlJjCnkzYWljcFVDQXdFQUFhTkNNRUF3RGdZRFZSMFBBUUgvQkFRREFnRUdNQThHQTFVZEV3RUIvd1FGTUFNQkFmOHcKSFFZRFZSME9CQllFRktWQ0tYSFAwdGNWVTJkdFZ4L2pFY2FVTnNjS01BMEdDU3FHU0liM0RRRUJDd1VBQTRJQgpBUUJsdzJNZXJ5eUQ4Q3dMV1UyOFArMWtnUVhzQ0wxVlc5bUFQZUlBMFVSbkFyNWxMTEZTWjJuenpCaVRCRWJRClBVdGlUM0NNQ2E1K0NVY2xqdWxIRkk3bmFDaUc5M29VS1N3ZkpVMDlnaU1rVFFxNkNxMDlOemk0MDBweEQ3WEgKbEdDeHBreHdteUhqcVVFZHVBc1lDUVAwOXhYR1FVM1hVcTNjbjdGQm5BSHJkVk1oSk8yMjNubXA2YlcxcTNtYgpraDg0Y01jRlcxSGd5UzRnSy9RUWJiYmczaWhicUZjeFdXdVlDanp5eHFZSEJoai9OWisrdjliNXZ2Y0hZcEViCml1QkNhOFlCSEVzL3RxbGNZaldaMzhRUU1TUU9BeitoSStPNmozcGQwNGxBV1V1cGZ5VFV4TlRjRWtydjlCQzgKRWJtZ0M0WDNvWjR5TWVNd3kvc2V0Ky8xCi0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K
    server: https://10.27.143.60:6443
  name: kubernetes
contexts:
- context:
    cluster: kubernetes
    namespace: comate-stack-sandbox
    user: eks-user-kubeconfig-use
  name: eks-user-kubeconfig-use
current-context: eks-user-kubeconfig-use
kind: Config
preferences: {}
users:
- name: eks-user-kubeconfig-use
  user:
    token: eyJhbGciOiJSUzI1NiIsImtpZCI6IjExYUxfV0NyR1hhZ0ZxcHpObWRjeWthWVNpVHZDemNLdEtib0RzZWU1bVUifQ.eyJpc3MiOiJrdWJlcm5ldGVzL3NlcnZpY2VhY2NvdW50Iiwia3ViZXJuZXRlcy5pby9zZXJ2aWNlYWNjb3VudC9uYW1lc3BhY2UiOiJjb21hdGUtc3RhY2stc2FuZGJveCIsImt1YmVybmV0ZXMuaW8vc2VydmljZWFjY291bnQvc2VjcmV0Lm5hbWUiOiJla3MtdXNlci1rdWJlY29uZmlnLXVzZS10b2tlbi00bWZjeCIsImt1YmVybmV0ZXMuaW8vc2VydmljZWFjY291bnQvc2VydmljZS1hY2NvdW50Lm5hbWUiOiJla3MtdXNlci1rdWJlY29uZmlnLXVzZSIsImt1YmVybmV0ZXMuaW8vc2VydmljZWFjY291bnQvc2VydmljZS1hY2NvdW50LnVpZCI6IjgyYzZiMTgxLTUyNmItNGRkNi05MzZlLWQ5Njg2NDU1MWNlMyIsInN1YiI6InN5c3RlbTpzZXJ2aWNlYWNjb3VudDpjb21hdGUtc3RhY2stc2FuZGJveDpla3MtdXNlci1rdWJlY29uZmlnLXVzZSJ9.eEBSA2WF3OuUTYFk39Jsvvv3zE0ih3e5plrhJ-pvz-7xmMQS5AAi5DaQPOK6knlZuadYC-ILJbDKQvhWNCAnYURFDnkI5W1sY6EimI64_t8B9KYLU1_BtEuJONL1N4Oy-xZjn0z_uG1CP7FrZ-aafJaJYlVPN0XD98yMe5-3fkYGzo7IxmitbRZD_i_hyMI3y-3QcnvEgUPaqPkK7R2RpaP5jQmMxiAUBDt39ZfbPXaJ509zx66Xlvz74ZlWUCk3vyYDYfYbCnPH5VpU0mxGduGR6MPh1oEkGi0Am4WW0qrsgeiHMo6Ze1CYsoX5c57QXm-t5n6zfnGBkCiH4QM93w

`
	namespace := "comate-stack-sandbox"
	pod_name := "agent-demo"

	// 将Kubeconfig字符串解析为Config对象
	config, err := clientcmd.Load([]byte(kubeconfigStr))
	if err != nil {
		panic(err.Error())
	}

	// 将Kubeconfig转换为REST配置
	restConfig, err := clientcmd.NewDefaultClientConfig(*config, nil).ClientConfig()
	if err != nil {
		panic(err.Error())
	}

	// 创建Kubernetes客户端
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		panic(err.Error())
	}

	// 定义Pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod_name,
			Namespace: namespace, // 指定命名空间
			Labels: map[string]string{
				"sandboxId": pod_name,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "nginx",
					Image: "iregistry.baidu-int.com/hub-official/nginx:1.29.1",
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: 80,
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:              resource.MustParse("200m"),
							corev1.ResourceMemory:           resource.MustParse("512Mi"),
							corev1.ResourceEphemeralStorage: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:              resource.MustParse("500m"),
							corev1.ResourceMemory:           resource.MustParse("1Gi"),
							corev1.ResourceEphemeralStorage: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
	}

	// 创建Pod
	podsClient := clientset.CoreV1().Pods(namespace)
	result, err := podsClient.Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		panic(err.Error())
	}

	fmt.Printf("Created pod %q.\n", result.GetObjectMeta().GetName())

	// 定义Service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod_name,  // 服务名称
			Namespace: namespace, // 命名空间
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort, // 设置为NodePort类型
			Selector: map[string]string{
				"sandboxId": pod_name, // 选择器，确保与Pod的标签匹配
			},
			Ports: []corev1.ServicePort{
				{
					Port:       80,                   // 服务的端口
					TargetPort: intstr.FromInt32(80), // Pod的端口
				},
			},
		},
	}

	// 创建Service
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	createdService, err := clientset.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		panic(err.Error())
	}

	fmt.Printf("Service %s created with NodePort %d\n", createdService.Name, createdService.Spec.Ports[0].NodePort)

	// 创建Pod的Informer
	podListWatcher := cache.NewListWatchFromClient(
		clientset.CoreV1().RESTClient(),
		"pods",
		"comate-stack-sandbox",
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
			fmt.Printf("Pod added: %s/%s\n", pod.Namespace, pod.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod := oldObj.(*corev1.Pod)
			newPod := newObj.(*corev1.Pod)
			fmt.Printf("Pod updated: %s/%s\n", newPod.Namespace, newPod.Name)
			fmt.Printf("Old status: %s, New status: %s\n", oldPod.Status.Phase, newPod.Status.Phase)
		},
		DeleteFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			fmt.Printf("Pod deleted: %s/%s\n", pod.Namespace, pod.Name)
		},
	})

	// 启动Informer
	stopCh := make(chan struct{})
	defer close(stopCh)

	go podInformer.Run(stopCh)

	// 监听信号，以优雅地退出
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalChan
		close(stopCh)
	}()

	// 持续运行
	for {
		time.Sleep(1 * time.Second)
	}
}
