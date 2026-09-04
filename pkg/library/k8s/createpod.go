package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common/config"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/agfs"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/magellanauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/envvar"
	"icode.baidu.com/baidu/gdp/env"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	WorkspaceVolumeName      = "workspace"
	WorkspaceVolumeMountPath = "/workspace"

	NoahVolumeName      = "noah"
	NoahVolumeMountPath = "/noah"

	Gcc482Name            = "gcc-482"
	Gcc482VolumeMountPath = "/home/opt/gcc-4.8.2.bpkg-r4"

	HomeOptCompilerGcc8Name            = "home-opt-compiler-gcc8"
	HomeOptCompilerGcc8VolumeMountPath = "/home/opt/compiler/gcc-8.2"

	HomeOptCompilerGcc8HashName            = "home-opt-compiler-gcc8-hash"
	HomeOptCompilerGcc8HashVolumeMountPath = "/home/opt/compiler/gcc-8.2.hash"

	HomeOptCompilerGcc10Name            = "home-opt-compiler-gcc10"
	HomeOptCompilerGcc10VolumeMountPath = "/home/opt/compiler/gcc-10-r5"

	HomeOptCompilerGcc10HashName            = "home-opt-compiler-gcc10-hash"
	HomeOptCompilerGcc10HashVolumeMountPath = "/home/opt/compiler/gcc-10-r5.hash"

	HomeOptCompilerGcc12Name            = "home-opt-compiler-gcc12"
	HomeOptCompilerGcc12VolumeMountPath = "/home/opt/compiler/gcc-12"

	HomeOptCompilerGcc12HashName            = "home-opt-compiler-gcc12-hash"
	HomeOptCompilerGcc12HashVolumeMountPath = "/home/opt/compiler/gcc-12.hash"

	OptCompilerGcc10Name            = "opt-compiler-gcc10"
	OptCompilerGcc10VolumeMountPath = "/opt/compiler/gcc-10"

	OptCompilerGcc12Name            = "opt-compiler-gcc12"
	OptCompilerGcc12VolumeMountPath = "/opt/compiler/gcc-12"

	OptCompilerGcc8Name            = "opt-compiler-gcc8"
	OptCompilerGcc8VolumeMountPath = "/opt/compiler/gcc-8.2"

	OptCompilerGcc482Name            = "opt-compiler-gcc482"
	OptCompilerGcc482VolumeMountPath = "/opt/compiler/gcc-4.8.2"

	NssSwitchVolumeName      = "nss-switch"
	NssSwitchVolumeMountPath = "/etc/nsswitch.conf"
	LocaltimeVolumeName      = "localtime"
	LocaltimeVolumeMountPath = "/etc/localtime"
	BnsSoVolumeName          = "bns-so"
	BnsSoVolumeMountPath     = "/usr/lib64/libnss_bns.so.2"

	InternalToolsVolumeName      = "magellan-internal-tools"
	InternalToolsVolumeMountPath = "/magellan-21061ac4-1bd1-4cbb-a304-b3704023645d"
	SshDir                       = "/magellan-21061ac4-1bd1-4cbb-a304-b3704023645d/.ssh/"
	LoginDir                     = "/magellan-21061ac4-1bd1-4cbb-a304-b3704023645d/.login/"

	// MultiUserSshBaseDir 多用户SSH目录基础路径
	MultiUserSshBaseDir = "/magellan-21061ac4-1bd1-4cbb-a304-b3704023645d/.users"

	E2bContainerName = "e2b-code"
	E2bImage         = "iregistry.baidu-int.com/magellan-public/e2bdev/code-interpreter:latest"

	DockerContainerName       = "docker"
	DockerImage               = "iregistry.baidu-int.com/irepo-public/docker:24.0.9-dind"
	DockerVolumeName          = "docker-run"
	DockerVolumeNameMountPath = "/var/run"

	MagellanLogNamespace = "log"

	AnnotationKeyLogNamespace = "magellan-log-namespace"
	AnnotationKeyLogUUID      = "magellan-log-group-uuid"
	AnnotationKeyLogJWT       = "magellan-log-group-jwt"
)

var securityContext = &corev1.SecurityContext{
	Privileged: &[]bool{false}[0],
	// RunAsUser:                &[]int64{1000}[0],
	// RunAsGroup:               &[]int64{1000}[0],
	// RunAsNonRoot:             &[]bool{true}[0],
	AllowPrivilegeEscalation: &[]bool{false}[0],
	SeccompProfile: &corev1.SeccompProfile{
		Type:             corev1.SeccompProfileTypeRuntimeDefault,
		LocalhostProfile: nil,
	},
}

type CodeBean struct {
	Username   string   `json:"username"`
	BcloudAuth bool     `json:"bcloudAuth"`
	Module     string   `json:"module"`
	Branch     string   `json:"branch"`
	Flags      []string `json:"flags"`
}
type ImageFields struct {
	ImageURL string `json:"imageURL"`
	Type     string `json:"type"`
}

func StringPtr(s string) *string {
	return &s
}

// GetImagePullPolicy 根据镜像标签返回合适的镜像拉取策略
// 如果镜像标签是 latest，返回 PullAlways，否则返回 PullIfNotPresent
func GetImagePullPolicy(imageURL string) corev1.PullPolicy {
	if strings.HasSuffix(imageURL, ":latest") {
		return corev1.PullAlways
	}
	return corev1.PullIfNotPresent
}

func (i ImageFields) getImageLabel() string {
	if "MoltBot" == i.Type {
		return "openclaw"
	}
	return ""
}

type InfoFlowFields struct {
	AppKey    string `json:"appKey"`
	SecretKey string `json:"secretKey"`
	Token     string `json:"token"`
	AesKey    string `json:"aesKey"`
}

type DockerFields struct {
	Cpu        int32
	MemoryMB   int32
	DiskGB     int32
	HttpProxy  string `json:"httpProxy"`
	HttpsProxy string `json:"httpsProxy"`
	NoProxy    string `json:"noProxy"`
	DindImage  string `json:"dindImage"`
}

type PvcBinding struct {
	PvcName   string
	SubPath   string
	MountPath string
}

type CreatePodFields struct {
	LogUuid             string
	PodName             string
	ImageFields         *ImageFields
	Cpu                 int32
	Mem                 int32
	Disk                int32
	Token               string
	EnvVars             map[string]string
	BindPvcs            []*PvcBinding
	CodeBean            *CodeBean
	CodeBeans           []*CodeBean // 多用户支持
	DockerFields        *DockerFields
	WorkingDir          string
	NodeSelector        map[string]string
	OneApiKey           string
	OpenClawToken       string
	UgateToken          string
	Username            string
	Platform            string
	RegistryPullSecrets []string
	ReadLessPort        []int32
	InfoFlowFields      *InfoFlowFields
	WorkspacePVCName    string
	HostAlias           []corev1.HostAlias
	ClusterName         string
	AppID               int32
}
type PodEvent struct {
	PodName     string
	PortIp      string
	NodeIp      string
	CommandPort int32
	CodePort    int32
	PodPhase    corev1.PodPhase
	ErrMsg      string
}

// CreatePodFieldsBuilder 用于构建 CreatePodFields
type CreatePodFieldsBuilder struct {
	fields *CreatePodFields
}

// NewCreatePodFieldsBuilder 创建一个新的 Builder
func NewCreatePodFieldsBuilder(podName string) *CreatePodFieldsBuilder {
	return &CreatePodFieldsBuilder{
		fields: &CreatePodFields{
			PodName: podName,
		},
	}
}

// WithImage 设置镜像信息
func (b *CreatePodFieldsBuilder) WithImage(imageURL, imageType string) *CreatePodFieldsBuilder {
	b.fields.ImageFields = &ImageFields{
		ImageURL: imageURL,
		Type:     imageType,
	}
	return b
}

// WithResources 设置资源
func (b *CreatePodFieldsBuilder) WithResources(cpu, mem, disk int32) *CreatePodFieldsBuilder {
	b.fields.Cpu = cpu
	b.fields.Mem = mem
	b.fields.Disk = disk
	return b
}

// WithToken 设置 Token
func (b *CreatePodFieldsBuilder) WithToken(token string) *CreatePodFieldsBuilder {
	b.fields.Token = token
	return b
}

// WithClusterName 设置 cluster
func (b *CreatePodFieldsBuilder) WithClusterName(clusterName string) *CreatePodFieldsBuilder {
	b.fields.ClusterName = clusterName
	return b
}

// WithAppID 设置 appID
func (b *CreatePodFieldsBuilder) WithAppID(appID int32) *CreatePodFieldsBuilder {
	b.fields.AppID = appID
	return b
}

// WithLogUuid 设置 LogUuid
func (b *CreatePodFieldsBuilder) WithLogUuid(logUuid string) *CreatePodFieldsBuilder {
	b.fields.LogUuid = logUuid
	return b
}

// WithEnvVars 设置环境变量
func (b *CreatePodFieldsBuilder) WithEnvVars(envVars map[string]string) *CreatePodFieldsBuilder {
	b.fields.EnvVars = envVars
	return b
}

// WithEnvVarsFromJSON 从 JSON 字符串解析环境变量
func (b *CreatePodFieldsBuilder) WithEnvVarsFromJSON(envVarsJSON string, additionalVars map[string]string) *CreatePodFieldsBuilder {
	envVars := make(map[string]string)
	if envVarsJSON != "" {
		json.Unmarshal([]byte(envVarsJSON), &envVars)
	}
	if additionalVars != nil {
		for k, v := range additionalVars {
			envVars[k] = v
		}
	}
	b.fields.EnvVars = envVars
	return b
}

// WithBindPvcs 设置 PVC 绑定
func (b *CreatePodFieldsBuilder) WithBindPvcs(pvcs []*PvcBinding) *CreatePodFieldsBuilder {
	b.fields.BindPvcs = pvcs
	return b
}

// WithCodeBean 设置单用户 CodeBean
func (b *CreatePodFieldsBuilder) WithCodeBean(codeBean *CodeBean) *CreatePodFieldsBuilder {
	b.fields.CodeBean = codeBean
	return b
}

// WithCodeBeans 设置多用户 CodeBeans
func (b *CreatePodFieldsBuilder) WithCodeBeans(codeBeans []*CodeBean) *CreatePodFieldsBuilder {
	b.fields.CodeBeans = codeBeans
	return b
}

// WithDockerFields 设置 Docker 配置
func (b *CreatePodFieldsBuilder) WithDockerFields(dockerFields *DockerFields) *CreatePodFieldsBuilder {
	b.fields.DockerFields = dockerFields
	return b
}

// WithNodeSelector 设置节点选择器
func (b *CreatePodFieldsBuilder) WithNodeSelector(nodeSelector map[string]string) *CreatePodFieldsBuilder {
	b.fields.NodeSelector = nodeSelector
	return b
}

func (b *CreatePodFieldsBuilder) WithRegistryPullSecrets(registryPullSecrets []string) *CreatePodFieldsBuilder {
	b.fields.RegistryPullSecrets = registryPullSecrets
	return b
}

func (b *CreatePodFieldsBuilder) WithOneApiKey(oneApiKey string) *CreatePodFieldsBuilder {
	b.fields.OneApiKey = oneApiKey
	return b
}

func (b *CreatePodFieldsBuilder) WithOpenClawToken(openClawToken string) *CreatePodFieldsBuilder {
	b.fields.OpenClawToken = openClawToken
	return b
}

func (b *CreatePodFieldsBuilder) WithUgateToken(ugateToken string) *CreatePodFieldsBuilder {
	b.fields.UgateToken = ugateToken
	return b
}

func (b *CreatePodFieldsBuilder) WithPlatform(platform string) *CreatePodFieldsBuilder {
	b.fields.Platform = platform
	return b
}

func (b *CreatePodFieldsBuilder) WithReadLessPort(ports []int32) *CreatePodFieldsBuilder {
	b.fields.ReadLessPort = ports
	return b
}

func (b *CreatePodFieldsBuilder) WithInfoFlowFields(fields *InfoFlowFields) *CreatePodFieldsBuilder {
	b.fields.InfoFlowFields = fields
	return b
}

func (b *CreatePodFieldsBuilder) WithUsername(username string) *CreatePodFieldsBuilder {
	b.fields.Username = username
	return b
}

// Build 构建并返回 CreatePodFields
func (b *CreatePodFieldsBuilder) Build() *CreatePodFields {
	return b.fields
}

func allContainersReady(pod *corev1.Pod) bool {
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if !containerStatus.Ready {
			return false
		}
	}
	return true
}

func getUserScript(templateData *UserScriptTemplateData) string {
	renderString, _ := renderUserScript(templateData)
	return renderString
}

func hostPathTypePtr(pathType corev1.HostPathType) *corev1.HostPathType {
	return &pathType
}

func (cluster *Cluster) CreatePod(createPodFields *CreatePodFields, podEventChan chan PodEvent) error {
	if createPodFields.EnvVars == nil {
		createPodFields.EnvVars = make(map[string]string)
	}

	if createPodFields.Username != "" {
		createPodFields.EnvVars["SANDBOX_USERNAME"] = createPodFields.Username
	}
	// MoltBot 类型自动设置 OPENCLAW_SKILLS 环境变量（每次从 Redis 读取最新值，实现动态更新）
	if "MoltBot" == createPodFields.ImageFields.Type {
		createPodFields.EnvVars["OPENCLAW_SKILLS"] = common.GetDefaultOpenClawSkills()
		// 非如流入口的oepnclaw,如果BUSINESS_ID为空,则设置为默认值
		if businessID, ok := createPodFields.EnvVars["BUSINESS_ID"]; !ok || len(businessID) == 0 {
			if createPodFields.AppID != constant.OpenClawAppID {
				createPodFields.EnvVars["BUSINESS_ID"] = constant.CommonOpenclawBusinessID
			}
		}
		if common.IsPersonalTokenUser(createPodFields.Username) {
			createPodFields.EnvVars["DEFAULT_LLM_AUTH_KEY"] = createPodFields.OneApiKey
			createPodFields.EnvVars["DEFAULT_LLM_BASE_URL"] = "https://clawguard-llm.baidu-int.com/"
			createPodFields.EnvVars["DEFAULT_LLM_MODEL"] = "oneapi/glm-4.7-internal"
		} else {
			if len(createPodFields.OneApiKey) > 0 {
				createPodFields.EnvVars["DEFAULT_LLM_AUTH_KEY"] = createPodFields.OneApiKey
				createPodFields.EnvVars["DEFAULT_LLM_BASE_URL"] = "http://oneapi-comate.baidu-int.com/v1"
				createPodFields.EnvVars["DEFAULT_LLM_MODEL"] = "oneapi/glm-4.7-internal"
			}
		}
	}

	cluster.RegisterSandboxEvent(createPodFields.PodName, podEventChan)
	templateData := GetByTemplateType(createPodFields, createPodFields.ImageFields, createPodFields.OneApiKey, createPodFields.OpenClawToken, createPodFields.UgateToken, createPodFields.Username)

	createPodFields.WorkingDir = templateData.WorkingDir
	mainCommand := getUserScript(templateData)
	logUuid := createPodFields.LogUuid

	pullSecrets := make([]corev1.LocalObjectReference, 0)
	for _, registry := range createPodFields.RegistryPullSecrets {
		pullSecrets = append(pullSecrets, corev1.LocalObjectReference{
			Name: registry,
		})
	}

	var readinessProbe *corev1.Probe
	if createPodFields.ReadLessPort != nil && len(createPodFields.ReadLessPort) > 0 {
		readProbCmd := "curl -fsS http://127.0.0.1:49983/api/health || exit 1\n"
		exitCode := 1
		for _, port := range createPodFields.ReadLessPort {
			exitCode += 1
			readProbCmd += fmt.Sprintf("nc -z 127.0.0.1 %d || exit %d\n", port, exitCode)
		}
		readinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/bin/sh", "-c", readProbCmd},
				},
			},
			InitialDelaySeconds: 1,
			TimeoutSeconds:      1,
			PeriodSeconds:       1,
			SuccessThreshold:    1,
		}
	} else {
		readinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/api/health",
					Port: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 49983,
					},
				},
			},
			InitialDelaySeconds: 1,
			TimeoutSeconds:      1,
			PeriodSeconds:       1,
			SuccessThreshold:    1,
		}
	}

	podSpec := corev1.PodSpec{
		NodeSelector:     createPodFields.NodeSelector,
		HostIPC:          false,
		HostPID:          false,
		HostNetwork:      false,
		ImagePullSecrets: pullSecrets,
		HostAliases:      createPodFields.HostAlias,
		Volumes: []corev1.Volume{
			{
				Name: InternalToolsVolumeName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name:         WorkspaceVolumeName,
				VolumeSource: getWorkspaceVolumeSource(createPodFields),
			},
			{
				Name: NoahVolumeName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: NoahVolumeMountPath,
					},
				},
			},
			{
				Name: HomeOptCompilerGcc8Name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: HomeOptCompilerGcc8VolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathDirectory),
					},
				},
			},
			{
				Name: HomeOptCompilerGcc8HashName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: HomeOptCompilerGcc8HashVolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathFileOrCreate),
					},
				},
			},
			{
				Name: HomeOptCompilerGcc10Name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: HomeOptCompilerGcc10VolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathDirectory),
					},
				},
			},
			{
				Name: HomeOptCompilerGcc10HashName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: HomeOptCompilerGcc10HashVolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathFileOrCreate),
					},
				},
			},
			{
				Name: HomeOptCompilerGcc12Name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: HomeOptCompilerGcc12VolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathDirectory),
					},
				},
			},
			{
				Name: HomeOptCompilerGcc12HashName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: HomeOptCompilerGcc12HashVolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathFile),
					},
				},
			},
			{
				Name: Gcc482Name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: Gcc482VolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathDirectory),
					},
				},
			},
			{
				Name: OptCompilerGcc482Name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: OptCompilerGcc482VolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathDirectory),
					},
				},
			},
			{
				Name: OptCompilerGcc8Name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: OptCompilerGcc8VolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathDirectory),
					},
				},
			},
			{
				Name: OptCompilerGcc10Name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: OptCompilerGcc10VolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathDirectory),
					},
				},
			},
			{
				Name: OptCompilerGcc12Name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: OptCompilerGcc12VolumeMountPath,
						Type: hostPathTypePtr(corev1.HostPathDirectory),
					},
				},
			},
			{
				Name: NssSwitchVolumeName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: NssSwitchVolumeMountPath,
					},
				},
			},
			{
				Name: LocaltimeVolumeName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: LocaltimeVolumeMountPath,
					},
				},
			},
			{
				Name: BnsSoVolumeName,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: BnsSoVolumeMountPath,
					},
				},
			},
			{
				Name: "docker-graph-storage",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{}, // 或使用 PVC
				},
			},
			{
				Name: "modules",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/lib/modules",
					},
				},
			},
			{
				Name: "cgroup",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: "/sys/fs/cgroup",
					},
				},
			},
		},
		Containers: []corev1.Container{
			{
				Name:            "main",
				Image:           createPodFields.ImageFields.ImageURL,
				SecurityContext: securityContext,
				ImagePullPolicy: GetImagePullPolicy(createPodFields.ImageFields.ImageURL),
				Command: []string{
					"bash", "-exc", mainCommand,
				},
				Ports: []corev1.ContainerPort{
					{
						ContainerPort: 49983,
					},
				},
				WorkingDir: templateData.WorkingDir,
				Env: []corev1.EnvVar{
					{
						Name:  "SANDBOX_RUNTIME_ACCESS_TOKEN",
						Value: createPodFields.Token,
					},
					{
						Name:  "SANDBOX_ID",
						Value: createPodFields.PodName,
					},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse(fmt.Sprintf("%dm", createPodFields.Cpu/4)),
						corev1.ResourceMemory:           resource.MustParse(fmt.Sprintf("%dMi", createPodFields.Mem/2)),
						corev1.ResourceEphemeralStorage: resource.MustParse(fmt.Sprintf("%dGi", createPodFields.Disk/2)),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse(fmt.Sprintf("%dm", createPodFields.Cpu)),
						corev1.ResourceMemory:           resource.MustParse(fmt.Sprintf("%dMi", createPodFields.Mem)),
						corev1.ResourceEphemeralStorage: resource.MustParse(fmt.Sprintf("%dGi", createPodFields.Disk)),
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      InternalToolsVolumeName,
						MountPath: InternalToolsVolumeMountPath,
					},
					{
						Name:      WorkspaceVolumeName,
						MountPath: createPodFields.WorkingDir,
					},
					{
						Name:      NoahVolumeName,
						MountPath: NoahVolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      HomeOptCompilerGcc8Name,
						MountPath: HomeOptCompilerGcc8VolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      HomeOptCompilerGcc8HashName,
						MountPath: HomeOptCompilerGcc8HashVolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      HomeOptCompilerGcc10Name,
						MountPath: HomeOptCompilerGcc10VolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      HomeOptCompilerGcc10HashName,
						MountPath: HomeOptCompilerGcc10HashVolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      HomeOptCompilerGcc12Name,
						MountPath: HomeOptCompilerGcc12VolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      HomeOptCompilerGcc12HashName,
						MountPath: HomeOptCompilerGcc12HashVolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      Gcc482Name,
						MountPath: Gcc482VolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      OptCompilerGcc482Name,
						MountPath: OptCompilerGcc482VolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      OptCompilerGcc8Name,
						MountPath: OptCompilerGcc8VolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      OptCompilerGcc10Name,
						MountPath: OptCompilerGcc10VolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      OptCompilerGcc12Name,
						MountPath: OptCompilerGcc12VolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      NssSwitchVolumeName,
						MountPath: NssSwitchVolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      LocaltimeVolumeName,
						MountPath: LocaltimeVolumeMountPath,
						ReadOnly:  true,
					},
					{
						Name:      BnsSoVolumeName,
						MountPath: BnsSoVolumeMountPath,
						ReadOnly:  true,
					},
				},
				ReadinessProbe: readinessProbe,
			},
		},
	}

	if templateData.InjectE2bCode {
		container := getE2bCodeContainer(templateData.WorkingDir)
		podSpec.Containers = append(podSpec.Containers, container)
	}

	if createPodFields.DockerFields != nil {
		assembleDockerContainer2PodSpec(createPodFields, &podSpec)
	}
	// 添加pvc持久化存储
	assemblePvcs2PodSpec(createPodFields, &podSpec)

	// 添加 AgFS FUSE 卷
	assembleFuseVolume2PodSpec(createPodFields, &podSpec)

	// 添加初始化容器
	assembleCloneCode2PodSpec(createPodFields, &podSpec)

	// 添加环境变量
	assembleEnvs2PodSpec(createPodFields, &podSpec)

	// 定义pod
	// 检查用户是否在 UUAP 代理小流量白名单中
	inWhitelist, localDns := common.IsUserInClusterDnsWhitelist(createPodFields.AppID, createPodFields.Username)
	uuapProxySmall := inWhitelist && constant.IsRedirectUUAPCluster(createPodFields.ClusterName)

	labels := make(map[string]string)
	labels["sandbox-id"] = createPodFields.PodName
	labels["workload-type"] = "sandbox"
	labels["image-type"] = createPodFields.ImageFields.getImageLabel()
	if common.AppInNetPolicyWhiteList(createPodFields.AppID) && createPodFields.Platform != "" {
		labels["platform"] = createPodFields.Platform
	}
	if uuapProxySmall {
		labels["cce.baidu.com/node-local-dns-injection"] = "disabled"
		podSpec.DNSPolicy = corev1.DNSNone
		nameserver := "172.16.41.98"
		if localDns != "" {
			nameserver = localDns
		}
		podSpec.DNSConfig = &corev1.PodDNSConfig{
			Nameservers: []string{nameserver},
			Searches:    []string{"agent-sandbox-prod-public.svc.cluster.local", "svc.cluster.local", "cluster.local"},
			Options: []corev1.PodDNSConfigOption{
				{
					Name:  "ndots",
					Value: StringPtr("3"),
				},
				{
					Name:  "attempts",
					Value: StringPtr("2"),
				},
				{
					Name:  "timeout",
					Value: StringPtr("1"),
				},
			},
		}
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      createPodFields.PodName,
			Namespace: cluster.namespace,
			Labels:    labels,
			Annotations: map[string]string{
				AnnotationKeyLogJWT:       magellanauth.GetAuthApi().GetLogAuthJwt(MagellanLogNamespace, logUuid),
				AnnotationKeyLogNamespace: MagellanLogNamespace,
				AnnotationKeyLogUUID:      logUuid,
			},
		},
		Spec: podSpec,
	}

	// 创建容器
	client := cluster.clientSet.CoreV1().Pods(cluster.namespace)
	if _, err := client.Create(context.TODO(), pod, metav1.CreateOptions{}); err != nil {
		return err
	}
	return nil
}

func getWorkspaceVolumeSource(createPodFields *CreatePodFields) corev1.VolumeSource {
	claimName := createPodFields.WorkspacePVCName
	if claimName == "" {
		return corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		}
	}
	return corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
			ClaimName: claimName,
		},
	}
}

func assemblePvcs2PodSpec(createPodFields *CreatePodFields, podSpec *corev1.PodSpec) {
	// 添加pvc持久化存储
	if createPodFields.BindPvcs != nil && len(createPodFields.BindPvcs) > 0 {
		for idx, bindPvc := range createPodFields.BindPvcs {
			volumeName := fmt.Sprintf("%s-%d", "pvc-volume", idx)
			podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
				Name: volumeName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: bindPvc.PvcName,
					},
				},
			})
			volumeMount := corev1.VolumeMount{
				Name:      volumeName,
				MountPath: bindPvc.MountPath,
			}
			if bindPvc.SubPath != "" {
				volumeMount.SubPath = bindPvc.SubPath
			}
			podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, volumeMount)
		}
	}
}

// assembleFuseVolume2PodSpec 添加 AgFS CSI 卷到 Pod Spec
// 参考 Kubernetes CSI Volume 配置，使用 agfs.csi.baidu.com 驱动
func assembleFuseVolume2PodSpec(createPodFields *CreatePodFields, podSpec *corev1.PodSpec) {
	username := createPodFields.Username
	enableAgfs := false

	// 测试环境dodo && 小流量判断
	if env.RunMode() == env.RunModeTest && (createPodFields.AppID == 160 || createPodFields.AppID == 171) &&
		common.IsAgfsSmallTrafficEnabled(username) {
		enableAgfs = true
	}
	// 线上环境dodo && 小流量判断
	if env.RunMode() == env.RunModeRelease && createPodFields.AppID == config.GetDodoAppId() &&
		common.IsAgfsSmallTrafficEnabled(username) {
		enableAgfs = true
	}

	// 检测当前应用是否为dodo，并只放开测试用户使用
	if enableAgfs {
		agfsConfig := agfs.GetAgfsConfig()
		// 获取 comate-auth-token
		comateAuthToken := createPodFields.EnvVars["COMATE_AUTH_TOKEN"]
		if comateAuthToken == "" {
			// 目前对于dodo，环境变量存在COMATE_AUTH_TOKEN
			// comateAuthToken = authcenter.GetAuthCenterApi().GetPersonalTokenByUsername(createPodFields.Username)
		}
		// 增加label，只调度到支持agfs的节点上
		podSpec.NodeSelector = map[string]string{
			"agfs-csi-node": "standard",
		}

		// 为每个挂载路径创建 AgFS Volume
		volumeName := "agfs-dodo-desktop"
		mountPath := "/dodo_desktop"
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				CSI: &corev1.CSIVolumeSource{
					Driver: agfsConfig.Driver,
					VolumeAttributes: map[string]string{
						"proxyUrl":          agfsConfig.Endpoint,
						"pcid":              createPodFields.PodName, // pcid == podName == sandboxId
						"comate-auth-token": comateAuthToken,
						"username":          username,
					},
				},
			},
		})
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		})
	}
}

func assembleCloneCode2PodSpec(createPodFields *CreatePodFields, podSpec *corev1.PodSpec) error {
	// 先处理单用户模式（向下兼容）
	if createPodFields.CodeBean != nil {
		return assembleCloneCodeSingleUser2PodSpec(createPodFields, podSpec)
	}

	// 处理多用户模式
	if len(createPodFields.CodeBeans) > 0 {
		return assembleCloneCodeMultiUser2PodSpec(createPodFields, podSpec)
	}

	return nil
}

// 单用户模式（向下兼容）
func assembleCloneCodeSingleUser2PodSpec(createPodFields *CreatePodFields, podSpec *corev1.PodSpec) error {
	moduleName := createPodFields.CodeBean.Module
	noCode := true
	dest := ""
	if len(moduleName) > 0 {
		noCode = false
		dest = fmt.Sprintf("%s/%s", createPodFields.WorkingDir, moduleName)
	} else {
		moduleName = "baidu/agile/agent"
	}
	jwt := magellanauth.GetAuthApi().GetCodeAuthJwt("ICODE", moduleName)
	if jwt == "" {
		return errors.New("get magellan jwt error,can't inject icode key")
	}
	codeBeanMap := map[string]any{
		"repo":     moduleName,
		"userName": createPodFields.CodeBean.Username,
		"repoType": "ICODE",
		"sshKey":   "true",
		"branch":   createPodFields.CodeBean.Branch,
		"host":     envvar.GetICodeHost(),
	}
	if createPodFields.CodeBean.Flags != nil && len(createPodFields.CodeBean.Flags) > 0 {
		codeBeanMap["flags"] = createPodFields.CodeBean.Flags
	}
	codeBean, _ := json.Marshal(codeBeanMap)
	cloneContainer := getCloneCodeContainer(jwt, string(codeBean), createPodFields.CodeBean.BcloudAuth, noCode, dest)

	cloneContainer.VolumeMounts = append(cloneContainer.VolumeMounts, corev1.VolumeMount{
		Name:      WorkspaceVolumeName,
		MountPath: createPodFields.WorkingDir,
	})
	podSpec.InitContainers = []corev1.Container{
		cloneContainer,
	}
	// main容器中需要设置一下,用于处理ssh key的拷贝逻辑
	podSpec.Containers[0].Env = append(podSpec.Containers[0].Env,
		corev1.EnvVar{
			Name:  "MAGELLAN_CODE_AUTH",
			Value: "true",
		},
	)
	if createPodFields.CodeBean.BcloudAuth {
		podSpec.Containers[0].Env = append(podSpec.Containers[0].Env,
			corev1.EnvVar{
				Name:  "MAGELLAN_BCLOUD_AUTH",
				Value: "true",
			},
		)
	}
	return nil
}

// 多用户模式
func assembleCloneCodeMultiUser2PodSpec(createPodFields *CreatePodFields, podSpec *corev1.PodSpec) error {

	users := make([]string, 0)
	// 为每个用户创建单独的init container
	for i, codeBean := range createPodFields.CodeBeans {
		if codeBean == nil || codeBean.Username == "" {
			continue
		}
		moduleName := codeBean.Module
		noCode := true
		dest := ""
		if len(moduleName) > 0 {
			noCode = false
			dest = fmt.Sprintf("%s/%s", createPodFields.WorkingDir, moduleName)
		} else {
			moduleName = "baidu/agile/agent"
		}
		jwt := magellanauth.GetAuthApi().GetCodeAuthJwt("ICODE", moduleName)
		if jwt == "" {
			return fmt.Errorf("get magellan jwt error for user %s, can't inject icode key", codeBean.Username)
		}
		users = append(users, codeBean.Username)
		codeBeanMap := map[string]any{
			"repo":     moduleName,
			"userName": codeBean.Username,
			"repoType": "ICODE",
			"sshKey":   "true",
			"branch":   codeBean.Branch,
			"host":     envvar.GetICodeHost(),
		}
		if codeBean.Flags != nil && len(codeBean.Flags) > 0 {
			codeBeanMap["flags"] = codeBean.Flags
		}
		codeBeanJson, _ := json.Marshal(codeBeanMap)

		// 为每个用户创建专属的SSH目录
		userSshDir := fmt.Sprintf("%s/%s/.ssh/", MultiUserSshBaseDir, codeBean.Username)

		// 创建对应的init container
		cloneContainer := getCloneCodeContainerForUser(jwt, string(codeBeanJson), codeBean.BcloudAuth, userSshDir, i, noCode, dest)
		cloneContainer.VolumeMounts = append(cloneContainer.VolumeMounts, corev1.VolumeMount{
			Name:      WorkspaceVolumeName,
			MountPath: createPodFields.WorkingDir,
		})
		podSpec.InitContainers = append(podSpec.InitContainers, cloneContainer)
	}

	// main容器添加多用户环境变量
	podSpec.Containers[0].Env = append(podSpec.Containers[0].Env,
		corev1.EnvVar{
			Name:  "MAGELLAN_CODE_AUTH",
			Value: "true",
		},
		corev1.EnvVar{
			Name:  "MAGELLAN_MULTI_USER",
			Value: "true",
		},
		corev1.EnvVar{
			Name:  "MAGELLAN_MULTI_USERS",
			Value: strings.Join(users, ","),
		},
		corev1.EnvVar{
			Name:  "MAGELLAN_SSH_BASE_DIR",
			Value: MultiUserSshBaseDir,
		},
	)

	return nil
}

func assembleDockerContainer2PodSpec(createPodFields *CreatePodFields, podSpec *corev1.PodSpec) error {
	container := getDockerContainer(createPodFields)
	podSpec.Containers = append(podSpec.Containers, *container)
	mainContainer := &podSpec.Containers[0]
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: DockerVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})
	mainContainer.VolumeMounts = append(mainContainer.VolumeMounts, corev1.VolumeMount{
		Name:      DockerVolumeName,
		MountPath: DockerVolumeNameMountPath,
	})

	return nil
}

func assembleEnvs2PodSpec(createPodFields *CreatePodFields, podSpec *corev1.PodSpec) {
	// 添加环境变量
	if createPodFields.EnvVars != nil && len(createPodFields.EnvVars) > 0 {
		for key, value := range createPodFields.EnvVars {
			podSpec.Containers[0].Env = append(podSpec.Containers[0].Env, corev1.EnvVar{
				Name:  key,
				Value: value,
			})
		}
	}
}

func getCloneCodeContainer(jwt string, codeBean string, bCloudAuth bool, noCode bool, dest string) corev1.Container {
	return getCloneCodeContainerForUser(jwt, codeBean, bCloudAuth, InternalToolsVolumeMountPath+"/.ssh/", 0, noCode, dest)
}

func getCloneCodeContainerForUser(jwt string, codeBean string, bCloudAuth bool, sshDir string, userIndex int, noCode bool, dest string) corev1.Container {
	containerName := "clone-code"
	if userIndex > 0 {
		containerName = fmt.Sprintf("clone-code-user-%d", userIndex)
	}

	envVars := []corev1.EnvVar{
		{
			Name:  "jwt",
			Value: jwt,
		},
		{
			Name:  "authEndpoint",
			Value: fmt.Sprintf("%s/auth", envvar.GetMagellanConfig().Endpoint),
		},
		{
			Name:  "codeBean",
			Value: codeBean,
		},
		{
			Name:  "sshDir",
			Value: sshDir,
		},
		{
			Name:  "noCode",
			Value: fmt.Sprintf("%t", noCode),
		},
		{
			Name:  "namespace",
			Value: envvar.GetMagellanConfig().Namespace,
		},
		{
			Name:  "bcloudAuth",
			Value: fmt.Sprintf("%t", bCloudAuth),
		},
	}
	if len(dest) > 0 {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "dest",
			Value: dest,
		})
	}
	return corev1.Container{
		Name:            containerName,
		Image:           "iregistry.baidu-int.com/magellan-public/clone-code:20260519_1779172305876",
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: securityContext,
		Env:             envVars,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      InternalToolsVolumeName,
				MountPath: InternalToolsVolumeMountPath,
			},
		},
	}
}

func getE2bCodeContainer(workingDir string) corev1.Container {
	return corev1.Container{
		SecurityContext: securityContext,
		Name:            E2bContainerName,
		Image:           E2bImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 49999,
			},
		},
		WorkingDir: workingDir,
		Env: []corev1.EnvVar{
			{
				Name:  "SANDBOX_RUNTIME_AUTH_TOKEN",
				Value: "",
			},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(fmt.Sprintf("%dm", 250)),
				corev1.ResourceMemory:           resource.MustParse(fmt.Sprintf("%dMi", 512)),
				corev1.ResourceEphemeralStorage: resource.MustParse(fmt.Sprintf("%dGi", 10)),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(fmt.Sprintf("%dm", 1000)),
				corev1.ResourceMemory:           resource.MustParse(fmt.Sprintf("%dMi", 1024)),
				corev1.ResourceEphemeralStorage: resource.MustParse(fmt.Sprintf("%dGi", 10)),
			},
		},

		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      WorkspaceVolumeName,
				MountPath: workingDir,
			},
			// e2b codeInterpreter需要/home/user目录,此处mock一个;
			{
				Name:      WorkspaceVolumeName,
				MountPath: "/home/user",
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/health",
					Port: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: 49999,
					},
				},
			},
			InitialDelaySeconds: 1,
			TimeoutSeconds:      1,
			PeriodSeconds:       1,
			SuccessThreshold:    1,
		},
	}
}

func getDockerContainer(createPodFields *CreatePodFields) *corev1.Container {
	privileged := true
	runAsUser := int64(0) // root 用户
	dockerFields := createPodFields.DockerFields
	envVars := make([]corev1.EnvVar, 0)
	if len(dockerFields.HttpsProxy) > 0 {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "HTTPS_PROXY",
			Value: dockerFields.HttpsProxy,
		})
	}
	if len(dockerFields.HttpProxy) > 0 {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "HTTP_PROXY",
			Value: dockerFields.HttpProxy,
		})
	}
	if len(dockerFields.NoProxy) > 0 {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "NO_PROXY",
			Value: dockerFields.NoProxy,
		})
	}

	dockerImage := DockerImage
	if createPodFields.DockerFields.DindImage != "" {
		dockerImage = createPodFields.DockerFields.DindImage
	}

	dockerContainer := corev1.Container{
		SecurityContext: &corev1.SecurityContext{
			Privileged: &privileged,
			RunAsUser:  &runAsUser,
		},
		Name:            DockerContainerName,
		Image:           dockerImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"sh", "-c", "dockerd --iptables=true"},
		Env:             envVars,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(fmt.Sprintf("%dm", dockerFields.Cpu/4)),
				corev1.ResourceMemory:           resource.MustParse(fmt.Sprintf("%dMi", dockerFields.MemoryMB/2)),
				corev1.ResourceEphemeralStorage: resource.MustParse(fmt.Sprintf("%dGi", dockerFields.DiskGB)),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(fmt.Sprintf("%dm", dockerFields.Cpu)),
				corev1.ResourceMemory:           resource.MustParse(fmt.Sprintf("%dMi", dockerFields.MemoryMB)),
				corev1.ResourceEphemeralStorage: resource.MustParse(fmt.Sprintf("%dGi", dockerFields.DiskGB)),
			},
		},

		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      DockerVolumeName,
				MountPath: DockerVolumeNameMountPath,
			},
			{
				Name:      WorkspaceVolumeName,
				MountPath: WorkspaceVolumeMountPath,
			},
			{
				Name:      "docker-graph-storage",
				MountPath: "/var/lib/docker", // ✅ 必需：持久化存储
			},
			{
				Name:      "modules",
				MountPath: "/lib/modules", // ✅ 重要：内核模块
				ReadOnly:  true,
			},
			{
				Name:      "cgroup",
				MountPath: "/sys/fs/cgroup", // ✅ 必需：cgroup 支持
			},
		},

		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"sh", "-c", "docker ps"},
				},
			},
			InitialDelaySeconds: 1,
			TimeoutSeconds:      1,
			PeriodSeconds:       1,
			SuccessThreshold:    1,
		},
	}
	if createPodFields.BindPvcs != nil && len(createPodFields.BindPvcs) > 0 {
		for idx, bindPvc := range createPodFields.BindPvcs {
			volumeName := fmt.Sprintf("%s-%d", "pvc-volume", idx)
			volumeMount := corev1.VolumeMount{
				Name:      volumeName,
				MountPath: bindPvc.MountPath,
			}
			if bindPvc.SubPath != "" {
				volumeMount.SubPath = bindPvc.SubPath
			}
			dockerContainer.VolumeMounts = append(dockerContainer.VolumeMounts, volumeMount)
		}
	}
	return &dockerContainer
}

func (cluster *Cluster) CreateStaticPvcAndPv(ctx context.Context, pvName string, pvcName string, subDir string) error {
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvName,
			Namespace: cluster.namespace,
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteMany,
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceStorage: resource.MustParse("1024G"),
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			MountOptions: []string{
				"hard",
				"nfsvers=4.1",
				"nordirplus",
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				NFS: &corev1.NFSVolumeSource{
					Server: "cfs-5KNqf6S6PN.lb-464de21c.cfs.bj.baidubce.com",
					Path:   subDir,
				},
			},
		},
	}

	_, err := cluster.clientSet.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: cluster.namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteMany,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: resource.MustParse("1024G"),
				},
			},
			VolumeName: pvName,
		},
	}
	_, err = cluster.clientSet.CoreV1().PersistentVolumeClaims(cluster.namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (cluster *Cluster) CreateDynamicPVC(ctx context.Context, pvcName, storageClassName, size string) error {
	return cluster.CreateDynamicPVCWithAccessMode(ctx, pvcName, storageClassName, size, corev1.ReadWriteOnce)
}

// CreateDynamicPVCWithAccessMode 创建动态供给 PVC，支持自定义访问模式。
// 如 CFS（sharedcfs）需要 ReadWriteMany，local-path/hp1 使用 ReadWriteOnce。
func (cluster *Cluster) CreateDynamicPVCWithAccessMode(ctx context.Context, pvcName, storageClassName, size string, accessMode corev1.PersistentVolumeAccessMode) error {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: cluster.namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				accessMode,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: resource.MustParse(size),
				},
			},
			StorageClassName: &storageClassName,
		},
	}
	_, err := cluster.clientSet.CoreV1().PersistentVolumeClaims(cluster.namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// ============ BOS CSI 静态供给 ============

const (
	// BOSCSIDriver BOS CSI 插件驱动名称
	BOSCSIDriver = "csi-bos2plugin"
	// BOSStorageClass BOS 存储类名称
	BOSStorageClass = "csi-bos2"
	// BOSSecretName BOS AK/SK Secret 名称（需提前在集群中创建）
	BOSSecretName = "csi-bos-secret"
	// BOSSecretNamespace Secret 所在命名空间
	BOSSecretNamespace = "default"
	// BOSSandboxBucket 沙箱共享 BOS Bucket 名称（固定值）
	BOSSandboxBucket = "agentsandbox"
)

// DefaultBOSMountOptions 返回 BOSFS 默认挂载参数。
func DefaultBOSMountOptions() []string {
	return []string{
		"-o bos.fs.meta.expires=60",
		"-o allow_other",
	}
}

// BuildBOSPVName 构建 BOS 场景下的 PV 名称。
func BuildBOSPVName(sandboxUUID string) string {
	return fmt.Sprintf("sandbox-%s-pv", sandboxUUID)
}

// CreateBOSPVAndPVC 静态供给：先创建 PV（CSI BOS driver），再创建 PVC 绑定到该 PV。
// bucketName: BOS bucket 名称；subDir: bucket 下的子目录（如 "sandbox-{uuid}"）。
// 幂等处理：PV/PVC 已存在时跳过；PVC 创建失败时补偿删除 PV。
func (cluster *Cluster) CreateBOSPVAndPVC(ctx context.Context, pvName, pvcName, bucketName, subDir, size string) error {
	volumeHandle := fmt.Sprintf("%s/%s", bucketName, subDir)
	storageClass := BOSStorageClass

	// 1. 创建 PV
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse(size),
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			StorageClassName:              storageClass,
			MountOptions:                  DefaultBOSMountOptions(),
			// ClaimRef 预绑定：防止 PV 创建到 PVC 创建之间的窗口期被其他 PVC 意外绑定
			ClaimRef: &corev1.ObjectReference{
				Namespace: cluster.namespace,
				Name:      pvcName,
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       BOSCSIDriver,
					VolumeHandle: volumeHandle,
					NodePublishSecretRef: &corev1.SecretReference{
						Name:      BOSSecretName,
						Namespace: BOSSecretNamespace,
					},
				},
			},
		},
	}
	_, err := cluster.clientSet.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create BOS PV %s failed: %w", pvName, err)
	}

	// 2. 创建 PVC，静态绑定到上面的 PV
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: cluster.namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(size),
				},
			},
			StorageClassName: &storageClass,
			VolumeName:       pvName,
		},
	}
	_, err = cluster.clientSet.CoreV1().PersistentVolumeClaims(cluster.namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		// PVC 创建失败，补偿删除已创建的 PV
		_ = cluster.clientSet.CoreV1().PersistentVolumes().Delete(ctx, pvName, metav1.DeleteOptions{})
		return fmt.Errorf("create BOS PVC %s failed: %w", pvcName, err)
	}

	return nil
}

func (cluster *Cluster) DeletePVC(ctx context.Context, pvcName string) error {
	err := cluster.clientSet.CoreV1().PersistentVolumeClaims(cluster.namespace).Delete(ctx, pvcName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (cluster *Cluster) GetPVNameByPVC(ctx context.Context, pvcName string) (string, error) {
	pvc, err := cluster.clientSet.CoreV1().PersistentVolumeClaims(cluster.namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return pvc.Spec.VolumeName, nil
}

func (cluster *Cluster) DeletePV(ctx context.Context, pvName string) error {
	if pvName == "" {
		return nil
	}
	err := cluster.clientSet.CoreV1().PersistentVolumes().Delete(ctx, pvName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
