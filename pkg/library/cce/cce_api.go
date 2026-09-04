package cce

import (
	"fmt"
	"github.com/baidubce/bce-sdk-go/services/cce/v2"
	"github.com/baidubce/bce-sdk-go/services/cce/v2/types"
	"icode.baidu.com/baidu/gdp/conf"
	"strings"
)

var cceApi *Api = nil
var cceConfig *ApiConfig = nil

type Api struct {
	Config *ApiConfig
	Client *v2.Client
}

// GetCceApi 获取 CCE API 客户端单例
func GetCceApi() *Api {
	if cceConfig == nil {
		config := &ApiConfig{}
		err := conf.Parse("cce.toml", config)
		if err != nil {
			panic(fmt.Sprintf("parse cce config error: %v", err))
		}
		cceConfig = config
	}
	if cceApi == nil {
		cceApi = &Api{
			Config: cceConfig,
		}
		cceApi.initClient()
	}
	return cceApi
}

// initClient 初始化 CCE 客户端
func (api *Api) initClient() {
	client, err := v2.NewClient(
		api.Config.AccessKey,
		api.Config.SecretKey,
		api.Config.Endpoint,
	)
	if err != nil {
		panic(fmt.Sprintf("create cce client error: %v", err))
	}
	api.Client = client
}

// CreateInstance 创建 CCE 实例
func (api *Api) CreateInstance(clusterID string, instanceSpec *types.InstanceSpec) (*v2.CreateInstancesResponse, error) {
	// 构建实例集参数
	instanceSet := &v2.InstanceSet{
		InstanceSpec: *instanceSpec,
		Count:        1,
	}

	args := &v2.CreateInstancesArgs{
		ClusterID: clusterID,
		Instances: []*v2.InstanceSet{instanceSet},
	}

	result, err := api.Client.CreateInstances(args)
	if err != nil {
		return nil, fmt.Errorf("create instance failed: %w", err)
	}

	return result, nil
}

// GetInstance 获取实例详情
func (api *Api) GetInstance(clusterID, instanceID string) (*v2.GetInstanceResponse, error) {
	args := &v2.GetInstanceArgs{
		ClusterID:  clusterID,
		InstanceID: instanceID,
	}

	result, err := api.Client.GetInstance(args)
	if err != nil {
		return nil, fmt.Errorf("get instance failed: %w", err)
	}

	return result, nil
}

// ParseInstancePhase 解析实例状态
func ParseInstancePhase(phase string) string {
	if phase == "" {
		return "unknown"
	}
	// 将大写状态转换为小写
	return strings.ToLower(phase)
}

// IsInstanceRunning 判断实例是否处于运行状态
func IsInstanceRunning(phase string) bool {
	return ParseInstancePhase(phase) == "running"
}

// FormatInstanceName 格式化实例名称
func FormatInstanceName(hostnamePrefix string, idx int) string {
	hostnameSplits := strings.Split(hostnamePrefix, ".")
	if len(hostnameSplits) < 2 {
		return fmt.Sprintf("%s-%d", hostnamePrefix, idx)
	}
	return fmt.Sprintf("%s-%d.%s", hostnameSplits[0], idx, hostnameSplits[1])
}
