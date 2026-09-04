package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/authcenter"

	"icode.baidu.com/baidu/gdp/env"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/card"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/devopsauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/runtimeapi"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"icode.baidu.com/baidu/gdp/logit"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/magellanauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/mcpcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
)

type sandboxRuntimeService struct {
	db *gorm.DB
}

func NewSandboxRuntimeService() SandboxRuntimeService {
	return &sandboxRuntimeService{}
}

var (
	// ErrSandboxRuntimeConflict 表示实例处于 running/pending 等不允许 restore 的状态。
	ErrSandboxRuntimeConflict = stderrors.New("sandbox runtime status conflict")
	// ErrSandboxRuntimeNoPVC 表示该实例未开启 PVC 持久化，不支持 restore。
	ErrSandboxRuntimeNoPVC = stderrors.New("sandbox runtime pvc not enabled")
)

type SandboxRuntimeService interface {
	CreateSandboxRuntime(ctx context.Context, req SandboxRuntimeRequest) (map[string]interface{}, error)
	RestoreSandboxRuntime(ctx context.Context, sandboxRuntimeID int64, role string, triggerType string, envVars map[string]string) (map[string]interface{}, error)
	// ReleaseSandboxRuntime 释放沙箱实例
	// force=false: 普通释放（删除Pod，PVC进入回收期）
	// force=true: 强制强制释放（删除Pod+PVC，清理所有缓存，状态→released）
	ReleaseSandboxRuntime(ctx context.Context, sandboxRuntimeID int64, role string, status string, force bool) (*SandboxRuntimeResponse, error)
	SelectSandboxRuntime(ctx context.Context, sandboxRuntimeID int64, role string) (*SandboxRuntimeResponse, error)
	SelectSandboxRuntimeAll(ctx context.Context, templateID int32, role string) (*[]SandboxRuntimeResponse, error)
	GetSandboxRuntimeByCondition(ctx context.Context, req SelectSandboxRuntimeConditionRequest, role string) (*SandboxRuntimeListResponse, error)
	RenewSandboxRuntime(ctx context.Context, sandboxRuntimeID int64, timeout int32, role string) error
	ReleaseSandboxPVC(ctx context.Context, sandboxUUID string) error
	CheckAndAlertResourceUsage(ctx context.Context, appID int32) error
	GetSandboxRuntimeEnvVars(ctx context.Context, sandboxRuntimeID int64) (map[string]string, error)
	GetOrphanedPods(ctx context.Context) (map[string][]string, error)
}

type SandboxRuntimeRequest struct {
	AppID              int32             `json:"appId"`
	TemplateID         int32             `json:"templateId"`
	Timeout            int32             `json:"timeout"`
	CreateUser         string            `json:"createUser"`
	ImageID            int32             `json:"imageId"`
	Resource           *ResourceSpec     `json:"resource"`
	EnvirVars          map[string]string `json:"envVars"`
	SandboxRuntimeName string            `json:"sandboxRuntimeName"`
	Icode              bool              `json:"icode"`
	Ducc               bool              `json:"ducc"`
	Remind             bool              `json:"remind"`
	PeriodTime         int32             `json:"periodTime"`
	Token              string            `json:"token"`
	CodeBean           *CodeBean         `json:"codeBean"`
	PostScript         string            `json:"postScript"`
	// 可选：true 使用 sandbox 专属 PVC；false/不传 使用 emptyDir。
	UsePVC *bool `json:"usePVC,omitempty"`
	// 可选：PVC 容量大小（单位 GB），范围 1-100，默认 20。
	PVCSize *int `json:"pvcSize,omitempty"`
	// 可选：存储类名称（"local-path" 或 "hp1"），默认 "local-path"。
	StorageClass *string `json:"storageClass,omitempty"`
}

// validatePVCSize 校验 PVC 大小参数
func validatePVCSize(pvcSize *int) error {
	if pvcSize == nil {
		return nil // 使用默认值
	}
	if *pvcSize < 1 || *pvcSize > 100 {
		return fmt.Errorf("pvcSize must be between 1 and 100, got %d", *pvcSize)
	}
	return nil
}

// resolvePVCSize 计算实际 PVC 大小（带默认值）
func resolvePVCSize(pvcSize *int) int {
	if pvcSize == nil || *pvcSize <= 0 {
		return 20 // 默认 20G
	}
	return *pvcSize
}

type SelectSandboxRuntimeConditionRequest struct {
	TemplateID         int32  `json:"templateId"`
	CreateUser         string `json:"createUser"`
	SandboxRuntimeName string `json:"sandboxRuntimeName"`
	Status             string `json:"status"`
	StartTimeBegin     string `json:"startTimeBegin"` // 启动时间范围-开始
	StartTimeEnd       string `json:"startTimeEnd"`   // 启动时间范围-结束
	Page               int    `json:"page"`
	PageSize           int    `json:"pageSize"`
	SortField          string `json:"sortField"`
	SortOrder          string `json:"sortOrder"`
}
type SandboxRuntimeResponse struct {
	SandboxRuntimeID int64  `json:"sandboxRuntimeId"`
	TemplateID       int32  `json:"templateId"`
	AppID            int32  `json:"appId"`
	ImageID          int32  `json:"imageId"`
	ImageURL         string `json:"imageUrl"`
	// 运行中(running)、已释放(kill)、系统释放(expire_kill)、待运行(pending)、运行失败(failed)、运行成功(succeeded)、已强制释放(released)
	SandboxRuntimeStatus string         `json:"sandboxRuntimeStatus"`
	Memory               int32          `json:"memory"`
	CPU                  int32          `json:"cpu"`
	Disk                 int32          `json:"disk"`
	CreateTime           time.Time      `json:"createTime"`
	ReleaseTime          *time.Time     `json:"releaseTime"`
	TimeoutTime          time.Time      `json:"timeoutTime"`
	ReleaseUser          string         `json:"releaseUser"`
	CreateUser           string         `json:"createUser"`
	SandboxRuntimeName   string         `json:"sandboxRuntimeName"`
	SandboxRuntimeUuid   string         `json:"sandboxRuntimeUuid"`
	EnvirVars            datatypes.JSON `json:"envVars"`
	Icode                bool           `json:"icode"`
	Ducc                 bool           `json:"ducc"`
	LogUrl               string         `json:"logUrl"`
	PageUrl              string         `json:"pageUrl"`
	FailedReason         string         `json:"failedReason"`
	CodeBean             *CodeBean      `json:"codeBean"`
	PostScript           string         `json:"postScript"`
	UsePVC               bool           `json:"usePVC"`                 // 是否使用 PVC 持久化存储
	PVCReclaimTime       *time.Time     `json:"pvcReclaimTime"`         // PVC 回收时间（仅当 usePVC=true 时有值）
	StorageClass         string         `json:"storageClass,omitempty"` // 存储类名称（仅当 usePVC=true 时有值）
}

// 分页信息结构体
type PaginationInfo struct {
	Page       int   `json:"page"`       // 当前页码
	PageSize   int   `json:"pageSize"`   // 每页条数
	Total      int64 `json:"total"`      // 总记录数
	TotalPages int   `json:"totalPages"` // 总页数
}

// 带分页的查询结果
type SandboxRuntimeListResponse struct {
	SandboxRuntimeList []SandboxRuntimeResponse `json:"sandboxRuntimeList"`
	Pagination         *PaginationInfo          `json:"pagination"`
}

var k8sStatusMapping = map[corev1.PodPhase]string{
	corev1.PodPending:   constant.StatusPending,
	corev1.PodRunning:   constant.StatusRunning,
	corev1.PodSucceeded: constant.StatusSucceeded,
	corev1.PodFailed:    constant.StatusFailed,
}

// 创建实例
func (s *sandboxRuntimeService) CreateSandboxRuntime(ctx context.Context, req SandboxRuntimeRequest) (map[string]interface{}, error) {
	logger := resource.LoggerService
	// 检查 app 是否存在 account（优先检查，快速失败）
	if err := CheckAppAccount(ctx, req.AppID); err != nil {
		return nil, err
	}
	// 检验参数信息
	if ok, err := checkSandBoxParams(req); !ok {
		return nil, err
	}
	// 校验镜像权限和大小（针对自定义镜像）
	if err := checkCustomImageAccess(ctx, req.ImageID); err != nil {
		return nil, err
	}
	// 校验 PVC 大小参数
	if err := validatePVCSize(req.PVCSize); err != nil {
		return nil, err
	}
	// 查询目前运行中的实例数量是否超过限制数量
	ok := getRunningInstance(ctx, req.AppID)
	if !ok {
		return nil, fmt.Errorf("当前实例数量已超过限制，请释放后重试")
	}
	// 实例名称是否重复
	if v, _ := checkName(req.SandboxRuntimeName, req.TemplateID); v {
		return nil, fmt.Errorf("当前实例名称已存在，请更换名称")
	}

	startTime := time.Now()
	// 设置 Prometheus 指标标签
	appIDLabel := fmt.Sprintf("%d", req.AppID)
	templateIDLabel := fmt.Sprintf("%d", req.TemplateID)

	loginUser := ctx.Value(constant.CtxLoginUserKey).(string)
	// 优先尝试从预热池获取可用的pod
	poolService := NewSandboxRuntimePoolService()
	poolItem, err := poolService.AcquireAndRemove(ctx, req.TemplateID, loginUser)
	if err != nil {
		logger.Warning(ctx, "acquire from pool failed, fallback to create new", logit.String("error", err.Error()))
	}
	if poolItem != nil {
		// 从预热池获取成功，直接使用
		logger.Notice(ctx, "using pool item", logit.Int64("id", poolItem.ID), logit.String("sandboxUUID", poolItem.SandboxUUID))

		var tpl model.Template
		templateDao := dao.Use(resource.OrmMain).Template
		err = templateDao.Where(templateDao.ID.Eq(req.TemplateID)).Scan(&tpl)
		if err != nil {
			logger.Error(ctx, "get template failed", logit.String("error", err.Error()))
			return nil, fmt.Errorf("db get template error: %w", err)
		}

		// 添加COMATE_AUTH_TOKEN 环境变量
		if tpl.Type == "All-in-one" || tpl.Type == "Coding agent" || tpl.Type == "Code" {
			if req.EnvirVars == nil {
				req.EnvirVars = make(map[string]string)
			}
			flag, token := mcpcenter.GetMcpCenterApi().GetToken(req.Token)
			if !flag {
				return nil, fmt.Errorf("get token failed: %s", token)
			}
			req.EnvirVars["COMATE_AUTH_TOKEN"] = token
		}

		envJSON, err := json.Marshal(req.EnvirVars)
		if err != nil {
			logger.Error(ctx, "marshal environment error", logit.String("error", err.Error()))
			return nil, fmt.Errorf("marshal environment error: %w", err)
		}

		codeBeanJSON := poolItem.CodeBean
		if req.CodeBean != nil {
			codeBeanBytes, err := json.Marshal(req.CodeBean)
			if err != nil {
				logger.Error(ctx, "marshal codeBean error", logit.String("error", err.Error()))
				return nil, fmt.Errorf("marshal codeBean error: %w", err)
			}
			codeBeanJSON = string(codeBeanBytes)
		}

		currentTime := time.Now()
		timeoutSeconds := req.Timeout * 60
		sandboxRuntime := model.SandboxRuntime{
			AppID:       req.AppID,
			TemplateID:  req.TemplateID,
			ImageID:     tpl.ImageID,
			CPU:         poolItem.CPU,
			Memory:      poolItem.Memory,
			Disk:        poolItem.Disk,
			EnvirVars:   string(envJSON),
			CreateTime:  currentTime,
			StartTime:   currentTime,
			Timeout:     timeoutSeconds,
			TimeoutTime: GetTimeoutTime(timeoutSeconds, currentTime),
			Status:      constant.StatusRunning,
			CreateUser:  loginUser,
			Name:        req.SandboxRuntimeName,
			ReleaseName: "",
			Icode:       req.Icode,
			Ducc:        req.Ducc,
			SandboxUUID: poolItem.SandboxUUID,
			PortIP:      poolItem.PortIP,
			NodeIP:      poolItem.NodeIP,
			CommandPort: poolItem.CommandPort,
			CodePort:    poolItem.CodePort,
			Token:       poolItem.Token,
			Cluster:     poolItem.Cluster,
			LogUUID:     poolItem.LogUUID,
			Metadata:    poolItem.Metadata,
			Remind:      req.Remind,
			PeriodTime:  req.PeriodTime * 60,
			CodeBean:    codeBeanJSON,
			PostScript:  req.PostScript,
			PodName:     poolItem.PodName,
		}

		// 写库
		sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
		if err := sandboxRuntimeDao.Create(&sandboxRuntime); err != nil {
			logger.Error(ctx, "create sandboxRuntime from pool failed", logit.String("error", err.Error()))
			return nil, fmt.Errorf("db error: %w", err)
		}

		poolCluster := resource.ClusterManager.GetClusterByName(poolItem.Cluster)

		// 设置Redis缓存
		resource.RedisClient.Set(ctx, constant.BackendIPPrefix+poolItem.SandboxUUID, poolItem.PortIP, 0)
		if poolCluster.GetNetworkType() == constant.NetworkTypeVpc {
			_ = resource.RedisClient.Set(context.Background(), constant.NginxEndpointRedisKey+poolItem.SandboxUUID, poolCluster.GetNginxEndpoint()).Err()
		}
		// 执行启动后脚本
		if len(req.PostScript) > 0 {
			execRet := runtimeapi.GetRuntimeApi().StartCommand(poolItem.SandboxUUID, poolItem.Token, req.PostScript)
			if !execRet {
				logger.Warning(ctx, "execute post script fail",
					logit.String("sandboxID", poolItem.SandboxUUID),
					logit.String("postScript", req.PostScript))
			} else {
				logger.Notice(ctx, "execute post script success",
					logit.String("sandboxID", poolItem.SandboxUUID),
					logit.String("postScript", req.PostScript))
			}
		}

		logger.Notice(ctx, "create sandbox from pool success",
			logit.String("sandboxID", poolItem.SandboxUUID),
			logit.Int64("poolItemId", poolItem.ID))

		// 检查水位：运行实例数达到 limit*80% 时向空间管理员发送告警
		if err := s.CheckAndAlertResourceUsage(ctx, req.AppID); err != nil {
			logger.Warning(ctx, "check and alert resource usage failed after create sandbox from pool",
				logit.Int32("appID", req.AppID),
				logit.String("sandboxID", poolItem.SandboxUUID),
				logit.String("error", err.Error()))
		}
		return map[string]interface{}{"sandboxRuntimeId": sandboxRuntime.ID}, nil
	}

	// 从预热池未获取，降级到实时创建
	logger.Debug(ctx, "pool item not available, creating new pod")
	srDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxUuid := util.GenerateUniqueSandboxID(8, func(id string) bool {
		count, _ := srDao.Where(srDao.SandboxUUID.Eq(id)).Count()
		return count > 0
	})
	token := util.GenerateRandomString(32)
	logUuid := uuid.New().String()
	clusterName := NewAppService().GetAppCluster(req.AppID, ctx.Value(constant.CtxLoginUserKey).(string))
	cluster := resource.ClusterManager.GetClusterByName(clusterName)

	var metadata = make(map[string]string)
	if req.Icode {
		metadata["codeBean"] = fmt.Sprintf("{\"username\": \"%s\"}",
			ctx.Value(constant.CtxLoginUserKey).(string),
		)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		logger.Error(ctx, "marshal metadata error", logit.String("error", err.Error()))
		return nil, fmt.Errorf("marshal metadata error: %w", err)
	}

	var tpl model.Template
	templateDao := dao.Use(resource.OrmMain).Template
	err = templateDao.Where(templateDao.ID.Eq(req.TemplateID)).Scan(&tpl)
	if err != nil {
		logger.Error(ctx, "get template failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("db get template error: %w", err)
	}
	// 添加COMATE_AUTH_TOKEN 环境变量
	var oneApiKey string
	var openClawToken string
	var ugateToken string
	var ugateSandboxToken string
	imageURL := tpl.Image

	if len(loginUser) > 0 {
		// 将环境变量 map 转换为 JSON 字符串用于异步推送
		envirVarsJSON, _ := json.Marshal(req.EnvirVars)
		ugateSandboxToken, ugateToken = authcenter.GetAuthCenterApi().GetSandboxUgateToken(sandboxUuid, loginUser, req.AppID, string(envirVarsJSON))
	}

	if tpl.Type == "All-in-one" || tpl.Type == "Coding agent" || tpl.Type == "Code" || tpl.Type == "MoltBot" {
		if req.EnvirVars == nil {
			req.EnvirVars = make(map[string]string)
		}
		flag, apiToken := mcpcenter.GetMcpCenterApi().GetToken(req.Token)
		if !flag {
			return nil, fmt.Errorf("get token failed: %s", apiToken)
		}
		req.EnvirVars["COMATE_AUTH_TOKEN"] = apiToken
		req.EnvirVars["SANDBOX_USERNAME"] = ctx.Value(constant.CtxLoginUserKey).(string)
		req.EnvirVars["SANDBOX_RUN_MODE"] = env.RunMode()

		// 根据白名单决定使用哪种方式获取 oneApiKey
		if common.IsPersonalTokenUser(ctx.Value(constant.CtxLoginUserKey).(string)) {
			// 使用新的个人 token 接口
			personalToken, err := devopsauth.GetAuthApi().GetOpenclawGWToken(ctx.Value(constant.CtxLoginUserKey).(string))
			if err != nil {
				resource.LoggerService.Notice(ctx, "get personal token failed", logit.String("error", err.Error()))
				oneApiKey = ""
			} else {
				oneApiKey = personalToken
			}
		} else {
			// 使用原有的 model token 接口
			modelToken, err := devopsauth.GetAuthApi().GetModelInfo(apiToken)
			if err != nil || modelToken == nil {
				resource.LoggerService.Notice(ctx, "get model token failed", logit.String("error", err.Error()))
				oneApiKey = ""
			} else {
				oneApiKey = modelToken.ModelToken
			}
		}
		if openClawTokenValue, openClawTokenOk := req.EnvirVars[constant.OpenClawEnvKey]; openClawTokenOk {
			openClawToken = openClawTokenValue
		} else {
			openClawToken = "123456"
		}
	}
	useSmallImageByTpl, smallImageByTpl := common.GetOpenClawImageSmallByTplID(tpl.AppID, tpl.Name, loginUser, "")
	if useSmallImageByTpl && len(smallImageByTpl) > 0 {
		imageURL = smallImageByTpl
	}
	envJSON, err := json.Marshal(req.EnvirVars)
	if err != nil {
		logger.Error(ctx, "marshal environment error", logit.String("error", err.Error()))
		return nil, fmt.Errorf("marshal environment error: %w", err)
	}
	codeBeanJSON := "{}"

	var codeBean *k8s.CodeBean
	if req.Icode {
		codeBean = &k8s.CodeBean{
			Username: ctx.Value(constant.CtxLoginUserKey).(string),
		}
	}

	if req.Icode && req.CodeBean != nil {
		codeBean.Module = req.CodeBean.Module
		codeBean.Branch = req.CodeBean.Branch
		if len(req.CodeBean.Branch) == 0 {
			codeBean.Branch = "master"
		}
		codeBeanBytes, marshalErr := json.Marshal(codeBean)
		if marshalErr != nil {
			logger.Error(ctx, "marshal codeBean error", logit.String("error", marshalErr.Error()))
			return nil, fmt.Errorf("marshal codeBean error: %w", marshalErr)
		}
		codeBeanJSON = string(codeBeanBytes)
	}
	var currentTime = time.Now()
	timeoutSeconds := req.Timeout * 60
	sandboxRuntime := model.SandboxRuntime{
		AppID:       req.AppID,
		TemplateID:  req.TemplateID,
		ImageID:     tpl.ImageID,
		CPU:         req.Resource.CPU,
		Memory:      req.Resource.Memory,
		Disk:        req.Resource.Disk,
		EnvirVars:   string(envJSON),
		CreateTime:  currentTime,
		StartTime:   currentTime,
		Timeout:     timeoutSeconds,
		TimeoutTime: GetTimeoutTime(timeoutSeconds, currentTime),
		Status:      constant.StatusPending,
		CreateUser:  ctx.Value(constant.CtxLoginUserKey).(string),
		Name:        req.SandboxRuntimeName,
		ReleaseName: "",
		Icode:       req.Icode,
		Ducc:        req.Ducc,
		SandboxUUID: sandboxUuid,
		PortIP:      "",
		NodeIP:      "",
		CommandPort: 0,
		CodePort:    0,
		Token:       token,
		Cluster:     clusterName,
		LogUUID:     logUuid,
		Metadata:    string(raw),
		Remind:      req.Remind,
		PeriodTime:  req.PeriodTime * 60,
		CodeBean:    codeBeanJSON,
		PostScript:  req.PostScript,
		UsePvc:      req.UsePVC != nil && *req.UsePVC,
	}
	// 写库
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	if err := sandboxRuntimeDao.Create(&sandboxRuntime); err != nil {
		logger.Error(ctx, "create sandboxRuntime failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("db error: %w", err)
	}
	var img model.Image
	imageDao := dao.Use(resource.OrmMain).Image
	err = imageDao.Where(imageDao.ID.Eq(req.ImageID)).Scan(&img)
	if err != nil {
		logger.Error(ctx, "get image failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("db get image error: %w", err)
	}
	appPvcDao := dao.Use(resource.OrmMain).AppPvcBinding
	pvcBindings, err := appPvcDao.Where(appPvcDao.AppID.Eq(req.AppID)).Find()
	if err != nil {
		logger.Error(ctx, "get pvc failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("db get pvc error: %w", err)
	}
	pvcs := make([]*k8s.PvcBinding, 0, len(pvcBindings))
	for _, b := range pvcBindings {
		pvcs = append(pvcs, &k8s.PvcBinding{
			PvcName:   b.PvcName,
			MountPath: b.MountPath,
		})
	}
	resultChan := make(chan k8s.PodEvent, 1)

	pullSecrets := make([]string, 0)
	pullSecrets = append(pullSecrets, constant.RegistryPullSecret)

	var app model.App
	appDao := dao.Use(resource.OrmMain).App
	err = appDao.Where(appDao.ID.Eq(tpl.AppID)).Scan(&app)
	workspaceID := 0
	if err == nil {
		workspaceID = int(app.WorkspaceID)
	}

	createPodFields := k8s.NewCreatePodFieldsBuilder(sandboxUuid).
		WithImage(imageURL, tpl.Type).
		WithResources(req.Resource.CPU, req.Resource.Memory, req.Resource.Disk).
		WithToken(token).
		WithEnvVars(req.EnvirVars).
		WithBindPvcs(pvcs).
		WithLogUuid(logUuid).
		WithCodeBean(codeBean).
		WithOneApiKey(oneApiKey).
		WithOpenClawToken(openClawToken).
		WithUgateToken(ugateSandboxToken).
		WithReadLessPort(util.GetReadProbPort(tpl.Type)).
		WithClusterName(clusterName).
		WithUsername(ctx.Value(constant.CtxLoginUserKey).(string)).
		WithAppID(req.AppID).
		WithRegistryPullSecrets(pullSecrets).
		WithPlatform(app.Platform).
		Build()
	// usePVC=true 时创建 sandbox 专属 PVC 并替换 workspace 卷；usePVC=false 时使用 emptyDir。
	if req.UsePVC != nil && *req.UsePVC {
		pvcSizeGB := resolvePVCSize(req.PVCSize) // 解析 PVC 大小，默认 20G
		username := ctx.Value(constant.CtxLoginUserKey).(string)
		storageClass, scErr := ValidateSandboxStorageClass(req.StorageClass, username)
		if scErr != nil {
			sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).
				Updates(map[string]interface{}{
					"status":        constant.StatusFailed,
					"end_time":      sandboxRuntime.StartTime,
					"failed_reason": fmt.Sprintf("invalid storage class: %s", scErr.Error()),
				})
			return map[string]interface{}{}, fmt.Errorf("invalid storage class: %w", scErr)
		}
		binding, bindErr := EnsureSandboxPVCBinding(ctx, req.AppID, sandboxUuid, k8s.WorkspaceVolumeMountPath, clusterName, cluster.GetNamespace(), pvcSizeGB, storageClass, cluster, username)
		if bindErr != nil {
			sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).
				Updates(map[string]interface{}{
					"status":        constant.StatusFailed,
					"end_time":      sandboxRuntime.StartTime,
					"failed_reason": fmt.Sprintf("ensure pvc binding failed: %s", bindErr.Error()),
				})
			return map[string]interface{}{}, fmt.Errorf("ensure pvc binding failed: %w", bindErr)
		}
		createPodFields.WorkspacePVCName = binding.PVCName
	} else if req.UsePVC != nil && !*req.UsePVC {
		createPodFields.WorkspacePVCName = ""
	}

	err = cluster.CreatePod(createPodFields, resultChan)
	if err != nil {
		RecordCreateFailed(appIDLabel, templateIDLabel)
		sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).
			Updates(map[string]interface{}{
				"status":        constant.StatusFailed,
				"end_time":      sandboxRuntime.StartTime,
				"failed_reason": fmt.Sprintf("创建pod失败: %s", err.Error()),
			})
		// usePVC=true 时，runtime 已进入终态（failed），再释放 PVC，避免误释放 running/pending。
		if req.UsePVC != nil && *req.UsePVC {
			_ = ReleaseSandboxPVCBinding(ctx, sandboxUuid, sandboxRuntime.StartTime, DefaultSandboxPVCGracePeriod())
		}
		// 调服务号通知用户
		mcpcenter.GetMcpCenterApi().UseAIRobot(sandboxRuntime.CreateUser, "沙箱实例创建失败通知", req.SandboxRuntimeName, tpl.Name, fmt.Sprintf("该沙箱实例创建失败，失败原因： %s", err.Error()), workspaceID)
		resource.LoggerService.Error(ctx, "create pod fail",
			logit.String("sandboxID", sandboxRuntime.SandboxUUID),
			logit.String("cluster", clusterName),
			logit.String("error", err.Error()))
		return map[string]interface{}{}, fmt.Errorf("create pod error: %w", err)
	}

	if tpl.Type == "MoltBot" && len(sandboxRuntime.CreateUser) > 0 && req.AppID == constant.OpenClawAppID {
		sandboxTypeRedisKey := "sandbox_type:" + sandboxRuntime.SandboxUUID
		_ = resource.RedisClient.Set(context.Background(), sandboxTypeRedisKey, "openclaw").Err()
	}

	go func() {

		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		//使用 select 等待数据到达或超时
		select {
		case data := <-resultChan:
			sandboxRuntime.PortIP = data.PortIp
			sandboxRuntime.NodeIP = data.NodeIp
			sandboxRuntime.CommandPort = data.CommandPort
			sandboxRuntime.CodePort = data.CodePort
			sandboxRuntime.StartTime = time.Now()
			// timeout_time 必须永远等于 start_time + timeout(秒)
			sandboxRuntime.TimeoutTime = GetTimeoutTime(sandboxRuntime.Timeout, sandboxRuntime.StartTime)
			sandboxRuntime.EndTime = time.Now()
			sandboxRuntime.PodName = data.PodName
			sandboxRuntime.Status = k8sStatusMapping[data.PodPhase]
			if k8sStatusMapping[data.PodPhase] == constant.StatusFailed {
				RecordCreateFailed(appIDLabel, templateIDLabel)
				sandboxRuntime.FailedReason = data.ErrMsg
				mcpcenter.GetMcpCenterApi().UseAIRobot(sandboxRuntime.CreateUser, "沙箱实例创建失败通知", req.SandboxRuntimeName, tpl.Name, fmt.Sprintf("该沙箱实例创建失败，失败原因： %s", data.ErrMsg), workspaceID)
			} else if k8sStatusMapping[data.PodPhase] == constant.StatusRunning {
				RecordCreateSuccess(appIDLabel, templateIDLabel)
				if req.UsePVC != nil && *req.UsePVC {
					_ = ActivateSandboxPVCBinding(context.Background(), sandboxUuid)
				}
			}
			duration := time.Since(startTime).Seconds()
			// 耗时指标不区分成功失败
			CreateSandboxDuration.WithLabelValues(appIDLabel, templateIDLabel).Observe(duration)

			redisKey := "sandbox:last_access:" + sandboxUuid
			_ = resource.RedisClient.Set(context.Background(), redisKey, time.Now().Unix(), time.Hour*24).Err()
			sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).Updates(sandboxRuntime)

			resource.RedisClient.Set(context.Background(), constant.BackendIPPrefix+sandboxUuid, data.PortIp)
			if cluster.GetNetworkType() == constant.NetworkTypeVpc {
				_ = resource.RedisClient.Set(context.Background(), constant.NginxEndpointRedisKey+sandboxUuid, cluster.GetNginxEndpoint()).Err()

			}
			// 写入 IP -> Owner 缓存
			_ = common.SetSandboxOwnerCache(context.Background(), resource.RedisClient,
				resource.LoggerService, data.PortIp, sandboxRuntime.CreateUser, sandboxUuid, ugateToken, sandboxRuntime.AppID, app.Platform)
			if k8sStatusMapping[data.PodPhase] == constant.StatusFailed {
				// PodEvent 失败时，runtime 已落库为 failed，再释放 PVC，避免误释放 running/pending。
				if req.UsePVC != nil && *req.UsePVC {
					_ = ReleaseSandboxPVCBinding(context.Background(), sandboxUuid, sandboxRuntime.EndTime, DefaultSandboxPVCGracePeriod())
				}
			}
			resource.LoggerService.Notice(bgCtx, "create sandbox success",
				logit.String("sandboxID", sandboxUuid),
				logit.String("cluster", clusterName),
				logit.String("podPhase", string(data.PodPhase)))
			if len(req.PostScript) > 0 {
				execRet := runtimeapi.GetRuntimeApi().StartCommand(sandboxRuntime.SandboxUUID, sandboxRuntime.Token, req.PostScript)
				if !execRet {
					resource.LoggerService.Warning(bgCtx, "execute post script fail",
						logit.String("sandboxID", sandboxUuid),
						logit.String("cluster", clusterName),
						logit.String("postScript", req.PostScript))
				} else {
					resource.LoggerService.Notice(bgCtx, "execute post script success",
						logit.String("sandboxID", sandboxUuid),
						logit.String("cluster", clusterName),
						logit.String("postScript", req.PostScript))
				}
			}
		case <-bgCtx.Done():
			RecordCreateFailed(appIDLabel, templateIDLabel)
			resource.LoggerService.Error(bgCtx, "create sandbox timeout",
				logit.String("sandboxID", sandboxUuid),
				logit.String("cluster", clusterName))
			sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).
				Updates(map[string]interface{}{
					"status":   constant.StatusFailed,
					"end_time": sandboxRuntime.StartTime,
					// 写明失败原因
					"failed_reason": "沙箱创建超时",
				})
			// 调服务号通知用户
			mcpcenter.GetMcpCenterApi().UseAIRobot(sandboxRuntime.CreateUser, "沙箱实例创建失败通知", req.SandboxRuntimeName, tpl.Name, fmt.Sprintf("该沙箱实例创建失败，失败原因： 沙箱创建超时，请重试"), workspaceID)
			err = DeleteSandbox(bgCtx, sandboxUuid, req.AppID)
			if err != nil {
				resource.LoggerService.Error(bgCtx, "delete pod fail",
					logit.String("sandboxID", sandboxUuid),
					logit.String("error", err.Error()))
			}
		}
	}()
	// 检查水位：运行实例数达到 limit*80% 时向空间管理员发送告警
	if err := s.CheckAndAlertResourceUsage(ctx, req.AppID); err != nil {
		logger.Warning(ctx, "check and alert resource usage failed after create sandbox",
			logit.Int32("appID", req.AppID),
			logit.String("sandboxID", sandboxUuid),
			logit.String("error", err.Error()))
	}
	return map[string]interface{}{"sandboxRuntimeId": sandboxRuntime.ID}, nil
}

// 恢复实例：复用同 sandbox_uuid 的 PVC，并在同一条 runtime 记录上更新状态（不新增 runtime 历史）。

func (s *sandboxRuntimeService) RestoreSandboxRuntime(ctx context.Context, sandboxRuntimeID int64, role string,
	triggerType string, requestEnvVars map[string]string) (map[string]interface{}, error) {
	logger := resource.LoggerService

	// 记录恢复操作开始时间，用于统计耗时
	startTime := time.Now()

	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	var sandboxRuntime model.SandboxRuntime
	var err error
	if role == constant.RoleMember {
		err = sandboxRuntimeDao.Where(
			sandboxRuntimeDao.ID.Eq(sandboxRuntimeID),
			sandboxRuntimeDao.CreateUser.Eq(ctx.Value(constant.CtxLoginUserKey).(string)),
		).Scan(&sandboxRuntime)
	} else {
		err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeID)).Scan(&sandboxRuntime)
	}
	if err != nil {
		return nil, fmt.Errorf("沙箱实例未找到: %w", err)
	}
	if sandboxRuntime.ID == 0 {
		return nil, fmt.Errorf("该实例由他人创建，当前用户无权限操作")
	}
	// 检查 app 是否存在 account
	if err := CheckAppAccount(ctx, sandboxRuntime.AppID); err != nil {
		return nil, err
	}
	appDao := dao.Use(resource.OrmMain).App
	app, err := appDao.Where(appDao.ID.Eq(sandboxRuntime.AppID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "get app info failed", logit.String("error", err.Error()))
		return nil, err
	}
	successMessage := common.GetPostStartNotificationMessage(ctx, resource.RedisClient, sandboxRuntime.CreateUser, sandboxRuntime.StartTime)
	// 设置 Prometheus 指标标签
	appIDLabel := fmt.Sprintf("%d", sandboxRuntime.AppID)
	templateIDLabel := fmt.Sprintf("%d", sandboxRuntime.TemplateID)

	if sandboxRuntime.Status == constant.StatusRunning || sandboxRuntime.Status == constant.StatusPending {
		RecordRestoreValidationFailed(appIDLabel, templateIDLabel, triggerType, constant.RestoreValidationFailureReasonStatusConflict)
		return nil, fmt.Errorf("我已处于工作状态，可以直接和我对话啦")
	}
	if !sandboxRuntime.UsePvc {
		RecordRestoreValidationFailed(appIDLabel, templateIDLabel, triggerType, constant.RestoreValidationFailureReasonNoPVC)
		return nil, ErrSandboxRuntimeNoPVC
	}

	originalStatus := sandboxRuntime.Status

	lockKey := constant.SandboxRestoreLockPrefix + sandboxRuntime.SandboxUUID
	locked, lockErr := resource.RedisClient.SetNX(ctx, lockKey, "1", 10*time.Minute).Result()
	if lockErr != nil {
		return nil, fmt.Errorf("set restore lock failed: %w", lockErr)
	}
	if !locked {
		return nil, fmt.Errorf("实例正在启动")
	}
	releaseLock := func() {
		resource.RedisClient.Del(context.Background(), lockKey)
	}

	cluster := resource.ClusterManager.GetClusterByName(sandboxRuntime.Cluster)
	if cluster == nil {
		RecordRestoreValidationFailed(appIDLabel, templateIDLabel, triggerType, constant.RestoreValidationFailureReasonClusterNotFound)
		releaseLock()
		return nil, fmt.Errorf("集群不存在: %s", sandboxRuntime.Cluster)
	}

	// 跨存储类迁移中禁止恢复，避免新旧 PVC 数据不一致
	if IsStorageMigrationInProgress(ctx, sandboxRuntime.SandboxUUID) {
		RecordRestoreValidationFailed(appIDLabel, templateIDLabel, triggerType, constant.RestoreValidationFailureReasonStorageMigrationInProgress)
		releaseLock()
		return nil, fmt.Errorf("存储迁移进行中，请稍后再恢复")
	}

	// 复用/确保绑定存在（released 状态也可复用，真正 running 后再 Activate）。
	// 注意：恢复时使用默认 PVC 大小（复用场景不允许修改大小）
	pvcSizeGB := 20 // TODO: 从 sandboxRuntime.PVCSize 读取（需要数据库迁移后）
	// 恢复时确定存储类：
	// - PVC 未被删除（binding.deleted=0）：复用原存储类，保证非默认存储类可正确重建。
	// - PVC 已被删除（binding.deleted=1）：使用默认存储类（CFS），与新建沙箱行为一致。
	latestBinding, _ := GetLatestSandboxPVCBinding(ctx, sandboxRuntime.SandboxUUID)
	restoreStorageClass := defaultSandboxPVCStorageClass
	if latestBinding != nil && !latestBinding.Deleted {
		restoreStorageClass = latestBinding.StorageClass
	}
	username := ctx.Value(constant.CtxLoginUserKey).(string)
	binding, bindErr := EnsureSandboxPVCBinding(ctx, sandboxRuntime.AppID, sandboxRuntime.SandboxUUID, k8s.WorkspaceVolumeMountPath, sandboxRuntime.Cluster, cluster.GetNamespace(), pvcSizeGB, restoreStorageClass, cluster, username)
	if bindErr != nil {
		RecordRestoreValidationFailed(appIDLabel, templateIDLabel, triggerType, constant.RestoreValidationFailureReasonPVCBindingFailed)
		_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).
			Updates(map[string]interface{}{
				"status":        originalStatus,
				"end_time":      time.Now(),
				"failed_reason": fmt.Sprintf("ensure pvc binding failed: %s", bindErr.Error()),
			})
		releaseLock()
		return nil, fmt.Errorf("持久化存储绑定失败: %w", bindErr)
	}

	// 读取模板信息（仅用于镜像与类型，不重新推导 CPU/MEM/Disk 等规格）。
	templateDao := dao.Use(resource.OrmMain).Template
	tpl, tplErr := templateDao.Where(templateDao.ID.Eq(sandboxRuntime.TemplateID)).First()
	if tplErr != nil || tpl == nil {
		endAt := time.Now()
		_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).
			Updates(map[string]interface{}{"status": originalStatus, "end_time": endAt, "failed_reason": "template not found"})
		_ = ReleaseSandboxPVCBinding(ctx, sandboxRuntime.SandboxUUID, endAt, DefaultSandboxPVCGracePeriod())
		releaseLock()
		return nil, fmt.Errorf("template not found: %w", tplErr)
	}

	imageURL := tpl.Image
	imageDao := dao.Use(resource.OrmMain).Image
	var img model.Image
	if imgErr := imageDao.Where(imageDao.ID.Eq(tpl.ImageID)).Scan(&img); imgErr == nil && img.ImageURL != "" {
		imageURL = img.ImageURL
	}

	// 从数据库读取环境变量
	envVars := make(map[string]string)
	if sandboxRuntime.EnvirVars != "" {
		_ = json.Unmarshal([]byte(sandboxRuntime.EnvirVars), &envVars)
	}
	// 合并请求中的环境变量，请求的优先级更高
	for k, v := range requestEnvVars {
		envVars[k] = v
	}

	oneApiKey := ""
	ugateToken := ""
	ugateSandboxToken := ""
	openClawToken := "123456"
	if v, ok := envVars[constant.OpenClawEnvKey]; ok && v != "" {
		openClawToken = v
	}

	// 获取 ugate token（所有类型沙箱都需要）
	if len(sandboxRuntime.CreateUser) > 0 {
		// 将环境变量 map 转换为 JSON 字符串用于异步推送
		envirVarsJSON, _ := json.Marshal(envVars)
		ugateSandboxToken, ugateToken = authcenter.GetAuthCenterApi().GetSandboxUgateToken(sandboxRuntime.SandboxUUID, sandboxRuntime.CreateUser, sandboxRuntime.AppID, string(envirVarsJSON))
	}

	// MoltBot 的启动脚本需要 oneApiKey/openClawToken；从已持久化的 COMATE_AUTH_TOKEN 重算 oneApiKey。
	if tpl.Type == "MoltBot" {
		if apiToken, ok := envVars[constant.DevopsJwtEnvKey]; ok && apiToken != "" {
			if common.IsPersonalTokenUser(sandboxRuntime.CreateUser) {
				// 使用新的个人 token 接口
				if personalToken, pErr := devopsauth.GetAuthApi().GetOpenclawGWToken(sandboxRuntime.CreateUser); pErr == nil {
					oneApiKey = personalToken
				}
			} else {
				// 使用原有的 model token 接口
				if modelToken, mErr := devopsauth.GetAuthApi().GetModelInfo(apiToken); mErr == nil && modelToken != nil {
					oneApiKey = modelToken.ModelToken
				}
			}
		} else {
			comateAuthToken := authcenter.GetAuthCenterApi().GetPersonalTokenByUsername(sandboxRuntime.CreateUser)
			if common.IsPersonalTokenUser(sandboxRuntime.CreateUser) {
				// 使用新的个人 token 接口
				if personalToken, pErr := devopsauth.GetAuthApi().GetOpenclawGWToken(sandboxRuntime.CreateUser); pErr == nil {
					oneApiKey = personalToken
					envVars[constant.DevopsJwtEnvKey] = comateAuthToken
				}
			} else {
				// 使用原有的 model token 接口
				if modelToken, mErr := devopsauth.GetAuthApi().GetModelInfo(comateAuthToken); mErr == nil && modelToken != nil {
					oneApiKey = modelToken.ModelToken
					envVars[constant.DevopsJwtEnvKey] = comateAuthToken
				}
			}
		}

	}
	useSmallImageByTpl, smallImageByTpl := common.GetOpenClawImageSmallByTplID(tpl.AppID, tpl.Name, sandboxRuntime.CreateUser, sandboxRuntime.SandboxUUID)
	if useSmallImageByTpl && len(smallImageByTpl) > 0 {
		imageURL = smallImageByTpl
	}

	appPvcDao := dao.Use(resource.OrmMain).AppPvcBinding
	pvcBindings, pvcErr := appPvcDao.Where(appPvcDao.AppID.Eq(sandboxRuntime.AppID)).Find()
	if pvcErr != nil {
		RecordRestoreValidationFailed(appIDLabel, templateIDLabel, triggerType, constant.RestoreValidationFailureReasonPVCConfigQueryFailed)
		endAt := time.Now()
		_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).
			Updates(map[string]interface{}{"status": originalStatus, "end_time": endAt, "failed_reason": fmt.Sprintf("db get pvc error: %s", pvcErr.Error())})
		_ = ReleaseSandboxPVCBinding(ctx, sandboxRuntime.SandboxUUID, endAt, DefaultSandboxPVCGracePeriod())
		releaseLock()
		return nil, fmt.Errorf("获取持久化存储配置失败了: %w", pvcErr)
	}
	pvcs := make([]*k8s.PvcBinding, 0, len(pvcBindings))
	for _, b := range pvcBindings {
		pvcs = append(pvcs, &k8s.PvcBinding{
			PvcName:   b.PvcName,
			MountPath: b.MountPath,
		})
	}

	// 生成新 token 并先置 pending，避免并发 restore/查询时状态不一致。
	_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).Updates(map[string]interface{}{
		"status":        constant.StatusPending,
		"failed_reason": "",
	})
	pullSecrets := make([]string, 0)
	pullSecrets = append(pullSecrets, constant.RegistryPullSecret)

	createPodFields := k8s.CreatePodFields{
		PodName: sandboxRuntime.SandboxUUID,
		ImageFields: &k8s.ImageFields{
			ImageURL: imageURL,
			Type:     tpl.Type,
		},
		Cpu:                 sandboxRuntime.CPU,
		Mem:                 sandboxRuntime.Memory,
		Disk:                sandboxRuntime.Disk,
		Token:               sandboxRuntime.Token,
		EnvVars:             envVars,
		BindPvcs:            pvcs,
		LogUuid:             sandboxRuntime.LogUUID,
		OneApiKey:           oneApiKey,
		OpenClawToken:       openClawToken,
		UgateToken:          ugateSandboxToken,
		WorkspacePVCName:    binding.PVCName,
		ReadLessPort:        util.GetReadProbPort(tpl.Type),
		Username:            sandboxRuntime.CreateUser,
		ClusterName:         sandboxRuntime.Cluster,
		AppID:               sandboxRuntime.AppID,
		RegistryPullSecrets: pullSecrets,
		Platform:            app.Platform,
	}
	// codeBean（可选）
	if sandboxRuntime.CodeBean != "" && sandboxRuntime.CodeBean != "{}" {
		codeBean := &k8s.CodeBean{}
		if cbErr := json.Unmarshal([]byte(sandboxRuntime.CodeBean), codeBean); cbErr == nil {
			createPodFields.CodeBean = codeBean
			// 恢复沙箱时,只注入权限,不做代码库clone操作,module设置为空,则恢复时不会clone;
			createPodFields.CodeBean.Module = ""
		}
	}

	resultChan := make(chan k8s.PodEvent, 1)
	// 先尝试清理同名旧 Pod，避免 Terminating/残留导致 AlreadyExists。
	_ = cluster.DeletePod(sandboxRuntime.SandboxUUID)
	if err = cluster.CreatePod(&createPodFields, resultChan); err != nil {
		RecordRestoreFailed(appIDLabel, templateIDLabel, triggerType)
		endAt := time.Now()
		_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).Updates(map[string]interface{}{
			"status":        originalStatus,
			"end_time":      endAt,
			"failed_reason": fmt.Sprintf("restore create pod failed: %s", err.Error()),
		})
		resource.LoggerService.Error(ctx, "restore pod fail",
			logit.String("sandboxID", sandboxRuntime.SandboxUUID),
			logit.String("cluster", sandboxRuntime.Cluster),
			logit.String("error", err.Error()))
		_ = ReleaseSandboxPVCBinding(ctx, sandboxRuntime.SandboxUUID, endAt, DefaultSandboxPVCGracePeriod())
		releaseLock()
		return nil, fmt.Errorf("唤起失败了，需要联系管理员介入处理！: %w", err)
	}

	// 在 goroutine 外部提取是否是定时任务的标识
	isScheduledTask := false
	if val := ctx.Value(constant.CtxScheduledTaskKey); val != nil {
		if scheduled, ok := val.(bool); ok && scheduled {
			isScheduledTask = true
		}
	}

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		defer releaseLock()

		select {
		case data := <-resultChan:
			status := k8sStatusMapping[data.PodPhase]
			startAt := time.Now()
			endAt := time.Now()
			updateData := map[string]interface{}{
				"pod_name":      data.PodName,
				"port_ip":       data.PortIp,
				"node_ip":       data.NodeIp,
				"command_port":  data.CommandPort,
				"code_port":     data.CodePort,
				"start_time":    startAt,
				"timeout_time":  GetTimeoutTime(sandboxRuntime.Timeout, startAt),
				"end_time":      endAt,
				"status":        status,
				"use_pvc":       true,
				"failed_reason": "",
			}
			if status == constant.StatusFailed {
				RecordRestoreFailed(appIDLabel, templateIDLabel, triggerType)
				updateData["status"] = originalStatus
				updateData["failed_reason"] = data.ErrMsg
				go SendSandboxRestartMessage(bgCtx, envVars, sandboxRuntime.CreateUser, sandboxRuntime.SandboxUUID, "唤起失败了，需要联系管理员介入处理", sandboxRuntime.AppID)
			} else if status == constant.StatusRunning {
				RecordRestoreSuccess(fmt.Sprintf("%d", sandboxRuntime.AppID), fmt.Sprintf("%d", sandboxRuntime.TemplateID), triggerType)
				_ = ActivateSandboxPVCBinding(context.Background(), sandboxRuntime.SandboxUUID)
				_ = resource.RedisClient.Set(context.Background(), constant.BackendIPPrefix+sandboxRuntime.SandboxUUID, data.PortIp, 0).Err()
				if cluster.GetNetworkType() == constant.NetworkTypeVpc {
					_ = resource.RedisClient.Set(context.Background(), constant.NginxEndpointRedisKey+sandboxRuntime.SandboxUUID, cluster.GetNginxEndpoint()).Err()
				}

				// 设置 last_access 时间
				// 根据触发类型调整起始时间，配合统一的3小时空闲阈值，实现不同的休眠时长
				redisKey := "sandbox:last_access:" + sandboxRuntime.SandboxUUID
				var lastAccessTime time.Time
				if triggerType == constant.TriggerTypeScheduled {
					// 定时唤醒：往前推2小时，配合3小时阈值实现1小时后自动休眠
					lastAccessTime = time.Now().Add(-2 * time.Hour)
				} else {
					// 手动唤醒：正常时间，3小时后自动休眠
					lastAccessTime = time.Now()
				}
				_ = resource.RedisClient.Set(context.Background(), redisKey, lastAccessTime.Unix(), time.Hour*24).Err()

				// 写入 IP -> Owner 缓存（恢复时IP会变化）
				_ = common.SetSandboxOwnerCache(context.Background(), resource.RedisClient, resource.LoggerService, data.PortIp, sandboxRuntime.CreateUser,
					sandboxRuntime.SandboxUUID, ugateToken, sandboxRuntime.AppID, app.Platform)

				// 只在非定时任务触发时发送消息通知用户容器已启动
				if !isScheduledTask {
					go func() {
						// 延迟30秒发送，确保容器内服务完全就绪后再通知用户
						time.Sleep(5 * time.Second)
						// 获取通知话术（根据用户、sandbox启动时间和redis配置判断）
						SendSandboxRestartMessage(context.Background(), envVars, sandboxRuntime.CreateUser, sandboxRuntime.SandboxUUID, successMessage, sandboxRuntime.AppID)
					}()

				} else {
					logger.Debug(bgCtx, "skip sending running message for scheduled task restore",
						logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
						logit.String("createUser", sandboxRuntime.CreateUser))
				}
			}
			duration := time.Since(startTime).Seconds()
			// 耗时指标不区分成功失败
			RestoreSandboxDuration.WithLabelValues(appIDLabel, templateIDLabel, triggerType).Observe(duration)
			_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).Updates(updateData)
			if status == constant.StatusFailed {
				_ = ReleaseSandboxPVCBinding(context.Background(), sandboxRuntime.SandboxUUID, endAt, DefaultSandboxPVCGracePeriod())
				go SendSandboxRestartMessage(bgCtx, envVars, sandboxRuntime.CreateUser, sandboxRuntime.SandboxUUID, "唤起失败了，需要联系管理员介入处理！", sandboxRuntime.AppID)
			}

			postScript := sandboxRuntime.PostScript
			if postScript != "" && status == constant.StatusRunning {
				_ = runtimeapi.GetRuntimeApi().StartCommand(sandboxRuntime.SandboxUUID, sandboxRuntime.Token, postScript)
			}

			// 检查水位：恢复成功且状态为 running 时向空间管理员发送告警
			if status == constant.StatusRunning {
				if err := s.CheckAndAlertResourceUsage(bgCtx, sandboxRuntime.AppID); err != nil {
					logger.Warning(bgCtx, "check and alert resource usage failed after restore sandbox",
						logit.Int32("appID", sandboxRuntime.AppID),
						logit.String("sandboxID", sandboxRuntime.SandboxUUID),
						logit.String("error", err.Error()))
				}
			}

			logger.Notice(bgCtx, "restore sandbox runtime finished",
				logit.Int64("sandboxRuntimeID", sandboxRuntimeID),
				logit.String("sandboxID", sandboxRuntime.SandboxUUID),
				logit.String("cluster", sandboxRuntime.Cluster),
				logit.String("status", status))
		case <-bgCtx.Done():
			endAt := time.Now()
			RecordRestoreFailed(appIDLabel, templateIDLabel, triggerType)
			_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).Updates(map[string]interface{}{
				"status":        originalStatus,
				"end_time":      endAt,
				"failed_reason": "restore timeout",
			})
			_ = ReleaseSandboxPVCBinding(context.Background(), sandboxRuntime.SandboxUUID, endAt, DefaultSandboxPVCGracePeriod())
			_ = DeleteSandbox(bgCtx, sandboxRuntime.SandboxUUID, sandboxRuntime.AppID)
			go SendSandboxRestartMessage(bgCtx, envVars, sandboxRuntime.CreateUser, sandboxRuntime.SandboxUUID, "唤起失败了，需要联系管理员介入处理！", sandboxRuntime.AppID)
		}
	}()

	return map[string]interface{}{"sandboxRuntimeId": sandboxRuntime.ID}, nil
}

// 释放实例
// ReleaseSandboxRuntime 释放沙箱实例（统一入口）
// completely=false: 普通释放（删除Pod，PVC进入回收期，状态→kill）
// completely=true: 强制释放（删除Pod+PVC，清理所有缓存，状态→released）
func (s *sandboxRuntimeService) ReleaseSandboxRuntime(ctx context.Context, sandboxRuntimeID int64, role string, status string, force bool) (*SandboxRuntimeResponse, error) {
	logger := resource.LoggerService
	logger.Notice(ctx, "release sandbox runtime",
		logit.Int64("sandboxRuntimeID", sandboxRuntimeID),
		logit.Bool("force", force),
		logit.String("status", status))

	// ===== 阶段1: 验证和加载沙箱信息 =====
	var sandboxRuntime model.SandboxRuntime
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	var err error

	if role == constant.RoleMember {
		err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeID), sandboxRuntimeDao.CreateUser.Eq(ctx.Value(constant.CtxLoginUserKey).(string))).Scan(&sandboxRuntime)
	} else {
		err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeID)).Scan(&sandboxRuntime)
	}

	if err != nil {
		return nil, fmt.Errorf("沙箱实例未找到: %w", err)
	}
	if sandboxRuntime.ID == 0 {
		return nil, fmt.Errorf("该实例由他人创建，当前用户无权限操作")
	}

	// 保存原始状态，用于后续判断是否需要记录历史
	originalStatus := sandboxRuntime.Status

	// ===== 阶段2: 状态检查（根据completely不同） =====
	if force {
		// 强制释放：已经released状态不允许重复操作
		if sandboxRuntime.Status == constant.StatusReleased {
			return nil, fmt.Errorf("实例已强制释放，无法重复操作")
		}

		// 如果不是kill状态，先调用普通释放
		if sandboxRuntime.Status != constant.StatusKill && sandboxRuntime.Status != constant.StatusExpiredKill {
			logger.Notice(ctx, "sandbox is not kill status, release it first",
				logit.String("currentStatus", sandboxRuntime.Status))

			// 递归调用普通释放（会删除 Pod 并更新状态为 kill）
			_, err = s.ReleaseSandboxRuntime(ctx, sandboxRuntimeID, role, "released", false)
			if err != nil {
				logger.Error(ctx, "failed to release sandbox runtime",
					logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
					logit.String("error", err.Error()))
				return nil, fmt.Errorf("释放沙箱实例失败: %w", err)
			}

			// 重新查询更新后的状态
			err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeID)).Scan(&sandboxRuntime)
			if err != nil {
				return nil, fmt.Errorf("查询沙箱状态失败: %w", err)
			}
		}
	} else {
		// 普通释放：kill/expired_kill/released状态不允许重复操作
		if sandboxRuntime.Status == constant.StatusKill || sandboxRuntime.Status == constant.StatusExpiredKill || sandboxRuntime.Status == constant.StatusReleased {
			return nil, fmt.Errorf("实例已释放，无法重复释放")
		}
	}

	clusterName := sandboxRuntime.Cluster
	sandboxId := sandboxRuntime.SandboxUUID

	// ===== 阶段3: 处理定时任务（仅普通释放首次执行） =====
	if !force {
		ProcessScheduledTasksBeforeShutdown(ctx, sandboxRuntime.ID, sandboxId, sandboxRuntime.CreateUser)
	}

	// ===== 阶段4: 删除Pod资源 =====
	cluster := resource.ClusterManager.GetClusterByName(clusterName)
	if cluster != nil {
		err = cluster.DeletePod(sandboxId)
		// 删除pod失败，并且非不存在的问题时报错;也就是说,不存在时,继续后续处理;
		if err != nil && !errors.IsNotFound(err) {
			logger.Error(ctx, "delete pod fail",
				logit.String("sandboxID", sandboxId),
				logit.String("cluster", clusterName),
				logit.String("error", err.Error()))
			return nil, fmt.Errorf("delete pod error: %w", err)
		}
	}

	// ===== 阶段5: 更新endTime（用于计费） =====
	// 注意：强制释放时，如果沙箱已经是kill状态，endTime在之前的普通释放步骤中已设置，跳过此阶段
	if sandboxRuntime.Status == constant.StatusPending {
		// Pending状态杀死时,不计费,把endTime设置为StartTime
		sandboxRuntime.EndTime = sandboxRuntime.StartTime
	} else if sandboxRuntime.Status == constant.StatusRunning {
		// 运行状态杀死时,计费,把endTime设置为当前时间
		sandboxRuntime.EndTime = time.Now()
	} else if !force {
		// 普通释放时，只允许 Pending 或 Running 状态，其他状态报错
		logger.Warning(ctx, "delete sandbox fail, status not match",
			logit.String("sandboxID", sandboxId),
			logit.String("currentStatus", sandboxRuntime.Status))
		return nil, fmt.Errorf("实例状态不匹配")
	}
	// 强制释放时，如果状态是 kill/expire_kill（阶段2已处理过），则跳过 endTime 设置

	// 强制释放时确保endTime被设置
	if force && sandboxRuntime.EndTime.IsZero() {
		sandboxRuntime.EndTime = time.Now()
	}

	// ===== 阶段6: 处理PVC（关键分支点） =====
	if sandboxRuntime.UsePvc {
		if force {
			// 强制释放：立即删除PVC/PV
			logger.Notice(ctx, "completely release: releasing PVC/PV resources",
				logit.String("sandboxUUID", sandboxId))

			err = s.ReleaseSandboxPVC(ctx, sandboxId)
			if err != nil {
				// PVC 删除失败不阻塞流程，记录警告
				logger.Warning(ctx, "failed to release PVC/PV",
					logit.String("sandboxUUID", sandboxId),
					logit.String("error", err.Error()))
			} else {
				logger.Notice(ctx, "PVC/PV released successfully",
					logit.String("sandboxUUID", sandboxId))
			}
		} else {
			// 普通释放：PVC进入回收期 + 迁移逻辑
			logger.Notice(ctx, "normal release: PVC enters grace period",
				logit.String("sandboxUUID", sandboxId))

			// 获取 PVC binding 信息（用于迁移逻辑）
			binding, bindErr := GetSandboxPVCBinding(ctx, sandboxRuntime.SandboxUUID)
			// 跨集群 PVC 迁移：将 app_id=2071 且 storage_class=sharedcfs 的 PVC 从老集群迁移到 agent-sandbox2-prod
			if bindErr == nil && binding != nil && common.IsCfsMigrateUser(sandboxRuntime.CreateUser) {
				if err := MigrateSbxPVCToNewCluster(ctx, sandboxRuntime.SandboxUUID, binding, sandboxRuntime.Cluster, sandboxRuntime.AppID); err != nil {
					logger.Warning(ctx, "migrate PVC to new cluster failed",
						logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
						logit.String("error", err.Error()))
				}
			}

			// 关闭后进入回收窗口（7天），PVC 绑定置为 kill。
			_ = ReleaseSandboxPVCBinding(ctx, sandboxRuntime.SandboxUUID, sandboxRuntime.EndTime, DefaultSandboxPVCGracePeriod())

			// 懒迁移触发：1.将 local-path PVC 数据异步拷贝到 CFS  2.CFS → CFS-N 迁移
			if bindErr == nil && (shouldLazyMigrate(&sandboxRuntime, binding) || shouldMigrateToNewCFS(&sandboxRuntime, binding)) {
				// 重新获取最新的 cluster 信息（可能已被跨集群迁移更新）
				sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
				latestRuntime, latestErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxRuntime.SandboxUUID)).First()
				migrateClusterName := sandboxRuntime.Cluster
				if latestErr == nil && latestRuntime != nil {
					migrateClusterName = latestRuntime.Cluster
				}
				cluster := resource.ClusterManager.GetClusterByName(migrateClusterName)
				if cluster != nil {
					go TriggerLazyStorageMigration(ctx, sandboxRuntime.SandboxUUID, binding, cluster, sandboxRuntime.CreateUser)
				}
			}
		}
	}

	// ===== 阶段7: 清理Redis缓存（分支） =====
	if force {
		// 强制释放：清理所有Redis缓存
		logger.Notice(ctx, "completely release: cleaning all redis keys",
			logit.String("sandboxUUID", sandboxId))

		redisKeys := []string{
			constant.BackendIPPrefix + sandboxId,       // backend_ip:{uuid}
			"sandbox:last_access:" + sandboxId,         // 最后访问时间
			"sandbox:scheduled_wakeup:" + sandboxId,    // 定时任务标记
			constant.NginxEndpointRedisKey + sandboxId, // nginx endpoint
		}

		deletedKeys := 0
		for _, key := range redisKeys {
			result, err := resource.RedisClient.Del(ctx, key).Result()
			if err != nil {
				logger.Warning(ctx, "failed to delete redis key",
					logit.String("key", key),
					logit.String("error", err.Error()))
			} else if result > 0 {
				deletedKeys++
				logger.Notice(ctx, "deleted redis key",
					logit.String("key", key))
			}
		}

		logger.Notice(ctx, "redis keys cleaned",
			logit.Int("deletedCount", deletedKeys),
			logit.Int("totalKeys", len(redisKeys)))

		// 删除 IP -> Owner 缓存
		_ = common.DeleteSandboxOwnerCache(ctx, resource.RedisClient, logger, sandboxRuntime.PortIP)
	} else {
		// 普通释放：只清理基础Redis缓存
		// 删除 IP -> Owner 缓存
		_ = common.DeleteSandboxOwnerCache(ctx, resource.RedisClient, logger, sandboxRuntime.PortIP)

		redisKey := constant.BackendIPPrefix + sandboxId
		logger.Notice(ctx, "deleting redis key for sandbox",
			logit.String("sandboxID", sandboxId),
			logit.String("redisKey", redisKey))

		_, err = resource.RedisClient.Del(ctx, redisKey).Result()
		if err != nil {
			logger.Error(ctx, "delete redis key failed",
				logit.String("sandboxID", sandboxId),
				logit.String("redisKey", redisKey),
				logit.String("error", err.Error()))
		} else {
			logger.Notice(ctx, "delete redis key success",
				logit.String("sandboxID", sandboxId),
				logit.String("redisKey", redisKey))
		}
	}

	// ===== 阶段8: 软删除定时任务（仅强制释放） =====
	if force {
		logger.Notice(ctx, "soft deleting scheduled tasks",
			logit.String("sandboxUUID", sandboxId))

		scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask
		result, err := scheduledTaskDao.Where(
			scheduledTaskDao.SandboxUUID.Eq(sandboxId),
			scheduledTaskDao.Deleted.Is(false),
		).Update(scheduledTaskDao.Deleted, true)

		if err != nil {
			logger.Warning(ctx, "failed to soft delete scheduled tasks",
				logit.String("sandboxUUID", sandboxId),
				logit.String("error", err.Error()))
		} else {
			logger.Notice(ctx, "scheduled tasks soft deleted",
				logit.Int64("deletedCount", result.RowsAffected))
		}
	}

	// ===== 阶段9: 更新数据库状态（分支） =====
	if force {
		// 强制释放：状态→released
		sandboxRuntime.Status = constant.StatusReleased
	} else {
		// 普通释放：状态→kill
		sandboxRuntime.Status = constant.StatusKill
	}
	sandboxRuntime.ReleaseName = ctx.Value(constant.CtxLoginUserKey).(string)

	_, err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntime.ID)).Updates(&sandboxRuntime)
	if err != nil {
		logger.Error(ctx, "update sandbox status fail",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		return nil, fmt.Errorf("update sandbox status error: %w", err)
	}

	// ===== 阶段10: 记录历史（分支） =====
	// 只有当沙箱从运行状态首次停止时才记录历史
	// 如果原始状态已经是 kill/expire_kill，说明运行历史已经记录过，避免重复记录
	if originalStatus == constant.StatusRunning || originalStatus == constant.StatusPending {
		historyStatus := status
		historyMsg := ""
		if force {
			historyStatus = "release"
			historyMsg = "强制释放：删除实例及所有持久化数据，不可恢复"
		}

		// 获取 workspaceID（如果获取失败则使用默认值 0）
		workspaceID := int32(0)
		appDao := dao.Use(resource.OrmMain).App
		app, appErr := appDao.Where(appDao.ID.Eq(sandboxRuntime.AppID)).First()
		if appErr == nil && app != nil {
			workspaceID = app.WorkspaceID
		}

		// 获取 appType（如果获取失败则使用默认值 "unknown"）
		appType := "unknown"
		if appErr == nil && app != nil {
			appType = app.Type
		}

		RecordDestroyHistory(
			sandboxRuntime.SandboxUUID,
			sandboxRuntime.AppID,
			sandboxRuntime.TemplateID,
			sandboxRuntime.Name,
			sandboxRuntime.Cluster,
			sandboxRuntime.CreateUser,
			sandboxRuntime.ReleaseName,
			sandboxRuntime.StartTime,
			sandboxRuntime.EndTime,
			historyStatus,
			sandboxRuntime.NodeIP,
			sandboxRuntime.PortIP,
			historyMsg,
			sandboxRuntime.CPU,
			sandboxRuntime.Memory,
			sandboxRuntime.Disk,
			workspaceID,
			appType,
		)

		logger.Notice(ctx, "sandbox runtime history recorded",
			logit.String("sandboxUUID", sandboxId),
			logit.String("originalStatus", originalStatus),
			logit.String("historyStatus", historyStatus))
	} else {
		// 从 kill/expire_kill → released，跳过历史记录（已记录过）
		logger.Notice(ctx, "skip history record: sandbox already stopped",
			logit.String("sandboxUUID", sandboxId),
			logit.String("originalStatus", originalStatus),
			logit.String("currentStatus", sandboxRuntime.Status),
			logit.Bool("force", force))
	}

	logger.Notice(ctx, "sandbox released successfully",
		logit.String("sandboxUUID", sandboxId),
		logit.String("finalStatus", sandboxRuntime.Status),
		logit.Bool("force", force))

	return nil, nil
}

// 查询实例
func (s *sandboxRuntimeService) SelectSandboxRuntime(ctx context.Context, sandboxRuntimeID int64, role string) (*SandboxRuntimeResponse, error) {
	list, err := s.GetSandboxRuntime(ctx, 0, sandboxRuntimeID, role)
	if err != nil {
		return nil, err
	}
	if list == nil || len(list) == 0 {
		return nil, fmt.Errorf("当前应用下不存在实例")
	}
	return &list[0], nil
}

// 查询全部实例
func (s *sandboxRuntimeService) SelectSandboxRuntimeAll(ctx context.Context, templateID int32, role string) (*[]SandboxRuntimeResponse, error) {
	resp, err := s.GetSandboxRuntime(ctx, templateID, 0, role)
	return &resp, err
}

// 查询（全部）实例通用
func (s *sandboxRuntimeService) GetSandboxRuntime(ctx context.Context, templateID int32, sandboxRuntimeID int64, role string) ([]SandboxRuntimeResponse, error) {
	var list []model.SandboxRuntime
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	if templateID == 0 {
		// 查询单实例
		err := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeID), sandboxRuntimeDao.Deleted.Is(false)).Scan(&list)
		if err != nil {
			resource.LoggerService.Error(ctx, "select sandbox status fail",
				logit.String("sandboxRuntimeID", fmt.Sprintf("sandboxRuntimeID: %d", sandboxRuntimeID)),
				logit.String("error", err.Error()))
			return nil, fmt.Errorf("sandboxRuntime not found: %w", err)
		}
		// 权限控制：如果实例的 templateID 为 2263 且用户角色为 Member，只能查询自己创建的实例
		if len(list) > 0 && list[0].TemplateID == 2263 && role == constant.RoleMember {
			createUser := ctx.Value(constant.CtxLoginUserKey).(string)
			if list[0].CreateUser != createUser {
				return nil, fmt.Errorf("该实例由他人创建，当前用户无权限查看")
			}
		}
	} else {
		// 查询所有实例
		if templateID == 2263 && role == constant.RoleMember {
			// 成员查看自己的实例
			err := sandboxRuntimeDao.Where(sandboxRuntimeDao.TemplateID.Eq(templateID),
				sandboxRuntimeDao.CreateUser.Eq(ctx.Value(constant.CtxLoginUserKey).(string)),
				sandboxRuntimeDao.Deleted.Is(false)).Scan(&list)
			if err != nil {
				resource.LoggerService.Error(ctx, "select sandbox status fail",
					logit.String("templateID", fmt.Sprintf("templateID: %d", templateID)),
					logit.String("error", err.Error()))
				return nil, fmt.Errorf("sandboxRuntime not found: %w", err)
			}
		} else {
			err := sandboxRuntimeDao.Where(sandboxRuntimeDao.TemplateID.Eq(templateID), sandboxRuntimeDao.Deleted.Is(false)).Scan(&list)
			if err != nil {
				resource.LoggerService.Error(ctx, "select sandbox status fail",
					logit.String("templateID", fmt.Sprintf("templateID: %d", templateID)),
					logit.String("error", err.Error()))
				return nil, fmt.Errorf("sandboxRuntime not found: %w", err)
			}
		}
	}
	imageIds := make([]int32, len(list))
	for i, r := range list {
		imageIds[i] = r.ImageID
	}
	imgMap, err := GetImageMap(imageIds)
	if err != nil {
		return nil, fmt.Errorf("getImageMap failed: %w", err)
	}
	// 判断是否是否超时
	for i := range list {
		out := GetTimeoutTime(list[i].Timeout, list[i].StartTime)
		if list[i].ReleaseName == "" &&
			time.Now().After(out) &&
			list[i].Status != constant.StatusFailed &&
			list[i].Timeout > 0 &&
			(list[i].Status == constant.StatusRunning || list[i].Status == constant.StatusPending) {
			list[i].ReleaseName = "admin"
			prevStatus := list[i].Status
			if prevStatus == constant.StatusPending {
				list[i].EndTime = list[i].StartTime
			} else {
				list[i].EndTime = out
			}
			if list[i].UsePvc {
				list[i].Status = constant.StatusExpiredKill
			} else {
				// usePVC=false 也进入 kill，与 usePVC=true 保持一致
				list[i].Status = constant.StatusKill
			}
			err = resource.ClusterManager.GetClusterByName(list[i].Cluster).DeletePod(list[i].SandboxUUID)
			redisKey := constant.BackendIPPrefix + list[i].SandboxUUID
			resource.LoggerService.Notice(ctx, "deleting redis key for expired sandbox",
				logit.String("sandboxID", list[i].SandboxUUID),
				logit.String("redisKey", redisKey))
			_, err = resource.RedisClient.Del(ctx, redisKey).Result()
			if err != nil {
				resource.LoggerService.Error(ctx, "delete redis key failed for expired sandbox",
					logit.String("sandboxID", list[i].SandboxUUID),
					logit.String("redisKey", redisKey),
					logit.String("error", err.Error()))
			}
			// 先落库关闭态：usePVC=true -> expire_kill；usePVC=false -> kill
			updateData := map[string]interface{}{
				"status":       list[i].Status,
				"release_name": list[i].ReleaseName,
				"end_time":     list[i].EndTime,
			}
			sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(list[i].ID)).Updates(updateData)
			if list[i].UsePvc {
				// usePVC=true：关闭后 PVC 绑定置为 kill，并写入 reclaim_after
				_ = ReleaseSandboxPVCBinding(ctx, list[i].SandboxUUID, list[i].EndTime, DefaultSandboxPVCGracePeriod())
			}
			resource.LoggerService.Notice(ctx, "update expired sandbox status success",
				logit.String("sandboxID", list[i].SandboxUUID),
				logit.String("status", list[i].Status),
				logit.Time("endTime", list[i].EndTime))
		}
	}

	// 批量查询 PVC binding 信息
	sandboxUUIDs := make([]string, 0, len(list))
	for i := range list {
		if list[i].UsePvc {
			sandboxUUIDs = append(sandboxUUIDs, list[i].SandboxUUID)
		}
	}
	pvcBindingMap := make(map[string]*model.SandboxPVCBinding)
	if len(sandboxUUIDs) > 0 {
		var err error
		pvcBindingMap, err = GetSandboxPVCBindingsByUUIDs(ctx, sandboxUUIDs)
		if err != nil {
			resource.LoggerService.Warning(ctx, "batch query pvc bindings failed",
				logit.String("error", err.Error()),
				logit.Int("count", len(sandboxUUIDs)))
			// 查询失败不影响主流程，继续返回，只是 pvcReclaimTime 为空
		}
	}

	//  组装返回数据
	resp := make([]SandboxRuntimeResponse, len(list))
	for i, t := range list {
		imageUrl := ""
		envJSON := t.EnvirVars
		codeBean := &CodeBean{}
		if imgMap != nil && imgMap[t.ImageID] != nil {
			imageUrl = imgMap[t.ImageID].ImageURL
		}
		if envJSON == "" {
			envJSON = "{}"
		}
		if t.CodeBean == "" || t.CodeBean == "{}" {
			codeBean = nil
		} else {
			json.Unmarshal([]byte(t.CodeBean), codeBean)
		}
		pageUrl := ""
		if imgMap != nil && imgMap[list[i].ImageID] != nil && imgMap[list[i].ImageID].Type == "All-in-one" {
			pageUrl = fmt.Sprintf("https://8080-%s.agent-sandbox.baidu-int.com/", list[i].SandboxUUID)
		} else if imgMap != nil && imgMap[list[i].ImageID] != nil && imgMap[list[i].ImageID].Type == "MoltBot" {
			pageUrl = fmt.Sprintf("https://8081-%s.agent-sandbox.baidu-int.com/", list[i].SandboxUUID)
		} else if imgMap != nil && imgMap[list[i].ImageID] != nil && imgMap[list[i].ImageID].Type == "Coding agent" {
			pageUrl = fmt.Sprintf("https://8200-%s.agent-sandbox.baidu-int.com/", list[i].SandboxUUID)
		}
		var releaseTime *time.Time
		if list[i].ReleaseName != "" {
			releaseTime = &list[i].EndTime
		}
		podName := list[i].PodName
		logUrl := ""
		if len(podName) > 0 {
			logUrl = magellanauth.GetAuthApi().GetLogURL(list[i].LogUUID, podName)
		} else {
			logUrl = magellanauth.GetAuthApi().GetLogURL(list[i].LogUUID, list[i].SandboxUUID)
		}

		// 查询 PVC 回收时间和存储类
		var pvcReclaimTime *time.Time
		var storageClassVal string
		if list[i].UsePvc {
			if binding, ok := pvcBindingMap[list[i].SandboxUUID]; ok {
				if binding.ReclaimAfter != nil {
					pvcReclaimTime = binding.ReclaimAfter
				}
				storageClassVal = binding.StorageClass
			}
		}

		resp[i] = SandboxRuntimeResponse{
			SandboxRuntimeID:     list[i].ID,
			TemplateID:           list[i].TemplateID,
			AppID:                list[i].AppID,
			ImageID:              list[i].ImageID,
			ImageURL:             imageUrl,
			SandboxRuntimeStatus: list[i].Status,
			Memory:               list[i].Memory,
			CPU:                  list[i].CPU,
			Disk:                 list[i].Disk,
			CreateTime:           list[i].CreateTime,
			ReleaseTime:          releaseTime,
			TimeoutTime:          GetTimeoutTime(list[i].Timeout, list[i].StartTime),
			ReleaseUser:          list[i].ReleaseName,
			CreateUser:           list[i].CreateUser,
			SandboxRuntimeName:   list[i].Name,
			SandboxRuntimeUuid:   list[i].SandboxUUID,
			EnvirVars:            datatypes.JSON(envJSON),
			LogUrl:               logUrl,
			PageUrl:              pageUrl,
			Icode:                list[i].Icode,
			Ducc:                 list[i].Ducc,
			FailedReason:         list[i].FailedReason,
			CodeBean:             codeBean,
			PostScript:           list[i].PostScript,
			UsePVC:               list[i].UsePvc,
			PVCReclaimTime:       pvcReclaimTime,
			StorageClass:         storageClassVal,
		}
	}
	return resp, nil
}

// 按照条件查询实例
func (s *sandboxRuntimeService) GetSandboxRuntimeByCondition(ctx context.Context, req SelectSandboxRuntimeConditionRequest, role string) (*SandboxRuntimeListResponse, error) {
	var list []model.SandboxRuntime
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	query := sandboxRuntimeDao.Where()

	// 1. 构建查询条件
	if req.TemplateID != 0 {
		query = query.Where(sandboxRuntimeDao.TemplateID.Eq(req.TemplateID))
	} else {
		return nil, fmt.Errorf("模版ID必填")
	}

	// 权限过滤：如果是 Member 且 templateID 为 2263，只能查看自己创建的实例
	if req.TemplateID == 2263 && role == constant.RoleMember {
		query = query.Where(sandboxRuntimeDao.CreateUser.Eq(ctx.Value(constant.CtxLoginUserKey).(string)))
	}

	if req.CreateUser != "" {
		query = query.Where(sandboxRuntimeDao.CreateUser.Eq(req.CreateUser))
	}
	if req.SandboxRuntimeName != "" {
		query = query.Where(sandboxRuntimeDao.Name.Eq(req.SandboxRuntimeName))
	}
	if req.Status != "" {
		query = query.Where(sandboxRuntimeDao.Status.Eq(req.Status))
	}

	// 启动时间范围查询
	if req.StartTimeBegin != "" {
		// 使用 ParseInLocation 明确指定本地时区,避免时区偏差
		startTimeBegin, err := time.ParseInLocation("2006-01-02 15:04:05", req.StartTimeBegin, time.Local)
		if err != nil {
			return nil, fmt.Errorf("启动时间范围-开始格式错误,正确格式: 2006-01-02 15:04:05")
		}
		query = query.Where(sandboxRuntimeDao.StartTime.Gte(startTimeBegin))
	}
	if req.StartTimeEnd != "" {
		// 使用 ParseInLocation 明确指定本地时区,避免时区偏差
		startTimeEnd, err := time.ParseInLocation("2006-01-02 15:04:05", req.StartTimeEnd, time.Local)
		if err != nil {
			return nil, fmt.Errorf("启动时间范围-结束格式错误,正确格式: 2006-01-02 15:04:05")
		}
		query = query.Where(sandboxRuntimeDao.StartTime.Lte(startTimeEnd))
	}

	// 2. 先统计总数(在应用排序和分页之前)
	var total int64
	total, err := query.Count()
	if err != nil {
		resource.LoggerService.Error(ctx, "count sandbox failed",
			logit.String("createUser", req.CreateUser),
			logit.String("sandboxRuntimeName", req.SandboxRuntimeName),
			logit.String("status", req.Status),
			logit.String("error", err.Error()))
		return nil, fmt.Errorf("count sandboxRuntime failed: %w", err)
	}

	// 3. 应用排序
	switch req.SortField {
	case "startTime":
		if req.SortOrder == "asc" {
			query = query.Order(sandboxRuntimeDao.StartTime)
		} else {
			query = query.Order(sandboxRuntimeDao.StartTime.Desc())
		}
	case "createTime":
		if req.SortOrder == "asc" {
			query = query.Order(sandboxRuntimeDao.CreateTime)
		} else {
			query = query.Order(sandboxRuntimeDao.CreateTime.Desc())
		}
	case "releaseTime":
		// releaseTime 对应数据库的 end_time 字段
		if req.SortOrder == "asc" {
			query = query.Order(sandboxRuntimeDao.EndTime)
		} else {
			query = query.Order(sandboxRuntimeDao.EndTime.Desc())
		}
	case "timeoutTime":
		// 直接使用数据库中的 timeout_time 字段排序
		if req.SortOrder == "asc" {
			query = query.Order(sandboxRuntimeDao.TimeoutTime)
		} else {
			query = query.Order(sandboxRuntimeDao.TimeoutTime.Desc())
		}
	}

	// 4. 应用分页
	offset := (req.Page - 1) * req.PageSize
	query = query.Limit(req.PageSize).Offset(offset)

	// 5. 执行查询
	err = query.Scan(&list)
	if err != nil {
		resource.LoggerService.Error(ctx, "select sandbox failed",
			logit.String("createUser", req.CreateUser),
			logit.String("sandboxRuntimeName", req.SandboxRuntimeName),
			logit.String("status", req.Status),
			logit.String("error", err.Error()))
		return nil, fmt.Errorf("sandboxRuntime found failed: %w", err)
	}

	imageIds := make([]int32, len(list))
	for i, r := range list {
		imageIds[i] = r.ImageID
	}
	imgMap, err := GetImageMap(imageIds)
	if err != nil {
		return nil, fmt.Errorf("getImageMap failed: %w", err)
	}

	// 判断是否是否超时
	for i := range list {
		out := GetTimeoutTime(list[i].Timeout, list[i].StartTime)
		if list[i].ReleaseName == "" &&
			time.Now().After(out) &&
			list[i].Status != constant.StatusFailed &&
			list[i].Timeout > 0 &&
			(list[i].Status == constant.StatusRunning || list[i].Status == constant.StatusPending) {
			list[i].ReleaseName = "admin"
			prevStatus := list[i].Status
			if prevStatus == constant.StatusPending {
				list[i].EndTime = list[i].StartTime
			} else {
				list[i].EndTime = out
			}
			if list[i].UsePvc {
				list[i].Status = constant.StatusExpiredKill
			} else {
				// usePVC=false 也进入 kill，与 usePVC=true 保持一致
				list[i].Status = constant.StatusKill
			}
			err = resource.ClusterManager.GetClusterByName(list[i].Cluster).DeletePod(list[i].SandboxUUID)
			redisKey := constant.BackendIPPrefix + list[i].SandboxUUID
			resource.LoggerService.Notice(ctx, "deleting redis key for expired sandbox",
				logit.String("sandboxID", list[i].SandboxUUID),
				logit.String("redisKey", redisKey))
			_, err = resource.RedisClient.Del(ctx, redisKey).Result()
			if err != nil {
				resource.LoggerService.Error(ctx, "delete redis key failed for expired sandbox",
					logit.String("sandboxID", list[i].SandboxUUID),
					logit.String("redisKey", redisKey),
					logit.String("error", err.Error()))
			}
			// 先落库关闭态：usePVC=true -> expire_kill；usePVC=false -> kill
			updateData := map[string]interface{}{
				"status":       list[i].Status,
				"release_name": list[i].ReleaseName,
				"end_time":     list[i].EndTime,
			}
			sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(list[i].ID)).Updates(updateData)
			if list[i].UsePvc {
				// usePVC=true：关闭后 PVC 绑定置为 kill，并写入 reclaim_after
				_ = ReleaseSandboxPVCBinding(ctx, list[i].SandboxUUID, list[i].EndTime, DefaultSandboxPVCGracePeriod())
			}
			resource.LoggerService.Notice(ctx, "update expired sandbox status success",
				logit.String("sandboxID", list[i].SandboxUUID),
				logit.String("status", list[i].Status),
				logit.Time("endTime", list[i].EndTime))
		}
	}

	// 5. 批量查询 PVC binding 信息
	sandboxUUIDs := make([]string, 0, len(list))
	for i := range list {
		if list[i].UsePvc {
			sandboxUUIDs = append(sandboxUUIDs, list[i].SandboxUUID)
		}
	}
	pvcBindingMap := make(map[string]*model.SandboxPVCBinding)
	if len(sandboxUUIDs) > 0 {
		var err error
		pvcBindingMap, err = GetSandboxPVCBindingsByUUIDs(ctx, sandboxUUIDs)
		if err != nil {
			resource.LoggerService.Warning(ctx, "batch query pvc bindings failed",
				logit.String("error", err.Error()),
				logit.Int("count", len(sandboxUUIDs)))
			// 查询失败不影响主流程，继续返回，只是 pvcReclaimTime 为空
		}
	}

	//  组装返回数据
	resp := make([]SandboxRuntimeResponse, len(list))
	for i, t := range list {
		imageUrl := ""
		envJSON := t.EnvirVars
		codeBean := &CodeBean{}
		if imgMap != nil && imgMap[t.ImageID] != nil {
			imageUrl = imgMap[t.ImageID].ImageURL
		}
		if envJSON == "" {
			envJSON = "{}"
		}
		if t.CodeBean == "" || t.CodeBean == "{}" {
			codeBean = nil
		} else {
			json.Unmarshal([]byte(t.CodeBean), codeBean)
		}
		pageUrl := ""
		if imgMap != nil && imgMap[list[i].ImageID] != nil && imgMap[list[i].ImageID].Type == "All-in-one" {
			pageUrl = fmt.Sprintf("https://8080-%s.agent-sandbox.baidu-int.com/", list[i].SandboxUUID)
		} else if imgMap != nil && imgMap[list[i].ImageID] != nil && imgMap[list[i].ImageID].Type == "MoltBot" {
			pageUrl = fmt.Sprintf("https://8081-%s.agent-sandbox.baidu-int.com/", list[i].SandboxUUID)
		} else if imgMap != nil && imgMap[list[i].ImageID] != nil && imgMap[list[i].ImageID].Type == "Coding agent" {
			pageUrl = fmt.Sprintf("https://8200-%s.agent-sandbox.baidu-int.com/", list[i].SandboxUUID)
		}
		var releaseTime *time.Time
		if list[i].ReleaseName != "" {
			releaseTime = &list[i].EndTime
		}
		logUrl := ""
		if len(list[i].PodName) > 0 {
			logUrl = magellanauth.GetAuthApi().GetLogURL(list[i].LogUUID, list[i].PodName)
		} else {
			logUrl = magellanauth.GetAuthApi().GetLogURL(list[i].LogUUID, list[i].SandboxUUID)
		}
		// 查询 PVC 回收时间和存储类
		var pvcReclaimTime *time.Time
		var storageClassVal string
		if list[i].UsePvc {
			if binding, ok := pvcBindingMap[list[i].SandboxUUID]; ok {
				if binding.ReclaimAfter != nil {
					pvcReclaimTime = binding.ReclaimAfter
				}
				storageClassVal = binding.StorageClass
			}
		}

		resp[i] = SandboxRuntimeResponse{
			SandboxRuntimeID:     list[i].ID,
			TemplateID:           list[i].TemplateID,
			AppID:                list[i].AppID,
			ImageID:              list[i].ImageID,
			ImageURL:             imageUrl,
			SandboxRuntimeStatus: list[i].Status,
			Memory:               list[i].Memory,
			CPU:                  list[i].CPU,
			Disk:                 list[i].Disk,
			CreateTime:           list[i].CreateTime,
			ReleaseTime:          releaseTime,
			TimeoutTime:          GetTimeoutTime(list[i].Timeout, list[i].StartTime),
			ReleaseUser:          list[i].ReleaseName,
			CreateUser:           list[i].CreateUser,
			SandboxRuntimeName:   list[i].Name,
			SandboxRuntimeUuid:   list[i].SandboxUUID,
			EnvirVars:            datatypes.JSON(envJSON),
			LogUrl:               logUrl,
			PageUrl:              pageUrl,
			Icode:                list[i].Icode,
			Ducc:                 list[i].Ducc,
			FailedReason:         list[i].FailedReason,
			CodeBean:             codeBean,
			PostScript:           list[i].PostScript,
			UsePVC:               list[i].UsePvc,
			PVCReclaimTime:       pvcReclaimTime,
			StorageClass:         storageClassVal,
		}
	}

	// 6. 计算总页数
	totalPages := int(total) / req.PageSize
	if int(total)%req.PageSize > 0 {
		totalPages++
	}

	// 7. 构建响应
	result := &SandboxRuntimeListResponse{
		SandboxRuntimeList: resp,
		Pagination: &PaginationInfo{
			Page:       req.Page,
			PageSize:   req.PageSize,
			Total:      total,
			TotalPages: totalPages,
		},
	}

	return result, nil
}

// 续期实例
func (s *sandboxRuntimeService) RenewSandboxRuntime(ctx context.Context, sandboxRuntimeID int64, timeout int32, role string) error {
	logger := resource.LoggerService
	logger.Notice(ctx, fmt.Sprintf("renew sandbox runtime, sandboxRuntimeID: %d, timeout: %d", sandboxRuntimeID, timeout))

	// 参数校验
	if timeout <= 0 || timeout > 10080 {
		return fmt.Errorf("过期时间无效,有效范围为1-10080分钟")
	}

	var sandboxRuntime model.SandboxRuntime
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	var err error

	// 根据用户角色查询
	if role == constant.RoleMember {
		err = sandboxRuntimeDao.Where(
			sandboxRuntimeDao.ID.Eq(sandboxRuntimeID),
			sandboxRuntimeDao.CreateUser.Eq(ctx.Value(constant.CtxLoginUserKey).(string)),
		).Scan(&sandboxRuntime)
	} else {
		err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeID)).Scan(&sandboxRuntime)
	}

	if err != nil {
		logger.Error(ctx, "query sandbox runtime failed", logit.String("error", err.Error()))
		return fmt.Errorf("沙箱实例未找到: %w", err)
	}

	if sandboxRuntime.ID == 0 {
		return fmt.Errorf("该实例由他人创建,当前用户无权限操作")
	}

	// 检查实例状态,只有运行中的实例才能续期
	if sandboxRuntime.Status != constant.StatusRunning {
		return fmt.Errorf("只有运行中的实例才能续期,当前状态: %s", sandboxRuntime.Status)
	}
	if sandboxRuntime.Timeout == 0 {
		return fmt.Errorf("当前实例为永久运行，无需续期")
	}
	// 计算新的超时时间: 原有的timeout(秒) + 新传入的timeout(分钟转秒)
	newTimeoutSeconds := sandboxRuntime.Timeout + (timeout * 60)
	// timeout_time的计算: start_time + 总的timeout(秒)
	newTimeoutTime := sandboxRuntime.StartTime.Add(time.Duration(newTimeoutSeconds) * time.Second)

	// 更新数据库
	_, err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeID)).Updates(map[string]interface{}{
		"timeout_time": newTimeoutTime,
		"timeout":      newTimeoutSeconds,
	})

	if err != nil {
		logger.Error(ctx, "update sandbox timeout failed",
			logit.String("sandboxID", sandboxRuntime.SandboxUUID),
			logit.String("error", err.Error()))
		return fmt.Errorf("更新超时时间失败: %w", err)
	}

	logger.Notice(ctx, "renew sandbox runtime success",
		logit.String("sandboxID", sandboxRuntime.SandboxUUID),
		logit.Int64("sandboxRuntimeID", sandboxRuntimeID),
		logit.Time("startTime", sandboxRuntime.StartTime),
		logit.Int32("originalTimeoutSeconds", sandboxRuntime.Timeout),
		logit.Int32("addTimeoutMinutes", timeout),
		logit.Int32("newTotalTimeoutSeconds", newTimeoutSeconds),
		logit.Time("newTimeoutTime", newTimeoutTime))

	return nil
}

func GetTimeoutTime(timeout int32, createTime time.Time) time.Time {
	return createTime.Add(time.Duration(timeout) * time.Second)
}

// 返回 true 表示已存在重名
func checkName(name string, templateID int32) (bool, error) {
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	count, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.TemplateID.Eq(templateID),
		sandboxRuntimeDao.Name.Eq(name), sandboxRuntimeDao.Deleted.Is(false)).Count()
	return count > 0, err
}

// 验证参数信息
func checkSandBoxParams(req SandboxRuntimeRequest) (bool, error) {
	if req.AppID <= 0 {
		return false, fmt.Errorf("appID 无效")
	}
	appDao := dao.Use(resource.OrmMain).App
	app, err := appDao.Where(appDao.ID.Eq(req.AppID)).First()
	if err != nil || app == nil {
		return false, fmt.Errorf("app not found")
	}
	if req.TemplateID <= 0 {
		return false, fmt.Errorf("templateID 无效")
	}
	templateDao := dao.Use(resource.OrmMain).Template
	template, err := templateDao.Where(templateDao.ID.Eq(req.TemplateID), templateDao.AppID.Eq(req.AppID)).First()
	if err != nil || template == nil {
		return false, fmt.Errorf("应用不存在")
	}
	if req.Timeout <= 0 || req.Timeout > 10080 {
		return false, fmt.Errorf("过期时间无效")
	}
	if req.ImageID <= 0 {
		return false, fmt.Errorf("imageID 无效")
	}
	imageDao := dao.Use(resource.OrmMain).Image
	image, err := imageDao.Where(imageDao.ID.Eq(req.ImageID)).First()
	if err != nil || image == nil {
		return false, fmt.Errorf("镜像不存在")
	}
	if req.Resource == nil {
		return false, fmt.Errorf("资源参数为空")
	}
	if req.Resource.Type == "" {
		return false, fmt.Errorf("资源类型为空")
	}
	if ok, v := checkResourceParams(req.Resource.CPU, req.Resource.Memory, req.Resource.Disk); !ok {
		return false, v
	}
	var nameRe = regexp.MustCompile(`^[a-zA-Z0-9\p{Han}_-]+$`)
	if req.SandboxRuntimeName == "" {
		return false, fmt.Errorf("沙箱实例名称为空")
	}
	if utf8.RuneCountInString(req.SandboxRuntimeName) < 1 || utf8.RuneCountInString(req.SandboxRuntimeName) > 64 {
		return false, fmt.Errorf("沙箱实例名称长度必须在1-64个字符之间")
	}
	if !nameRe.MatchString(req.SandboxRuntimeName) {
		return false, fmt.Errorf("沙箱实例名称只能包含中英文、数字、下划线、横杠")
	}
	if len(req.EnvirVars) > 64 {
		return false, fmt.Errorf("环境变量不能超过64个")
	}
	return true, nil
}

// checkCustomImageAccess 校验自定义镜像的权限和大小
func checkCustomImageAccess(ctx context.Context, imageID int32) error {
	imageDao := dao.Use(resource.OrmMain).Image
	image, err := imageDao.Where(imageDao.ID.Eq(imageID)).First()
	if err != nil || image == nil {
		return fmt.Errorf("镜像不存在")
	}

	// 只对自定义镜像进行校验
	if image.Scope != "custom" {
		return nil
	}

	// 获取当前用户名
	username := ctx.Value(constant.CtxLoginUserKey).(string)

	// 校验权限和大小
	_, _, err = util.CheckImageAccess(username, image.ImageURL)
	if err != nil {
		return err
	}

	return nil
}

// CheckAppAccount 检查 app 是否存在 account
func CheckAppAccount(ctx context.Context, appID int32) error {
	logger := resource.LoggerService
	appDao := dao.Use(resource.OrmMain).App

	var app model.App
	err := appDao.Where(appDao.ID.Eq(appID)).Scan(&app)
	if err != nil {
		logger.Error(ctx, "query app failed",
			logit.Int32("appID", appID),
			logit.String("error", err.Error()))
		return fmt.Errorf("查询应用信息失败: %w", err)
	}

	// 检查 account 字段是否为空
	if app.Account == "" {
		logger.Warning(ctx, "app account is empty, cannot create instance",
			logit.Int32("appID", appID))
		return fmt.Errorf("该应用未配置资源账号，无法创建实例")
	}

	logger.Notice(ctx, "app account check passed",
		logit.Int32("appID", appID),
		logit.String("account", app.Account))

	return nil
}

func getRunningInstance(ctx context.Context, appID int32) bool {
	appDao := dao.Use(resource.OrmMain).App
	templateDao := dao.Use(resource.OrmMain).Template

	// 1. 查 app
	var app model.App
	appDao.Where(appDao.ID.Eq(appID)).Scan(&app)
	limitCnt := app.LimitCnt
	var templates []model.Template
	templateDao.Where(templateDao.AppID.Eq(appID)).Scan(&templates)
	if len(templates) == 0 {
		return true
	}
	ids := make([]int32, len(templates))
	for i, t := range templates {
		ids[i] = t.ID
	}
	var running int
	db := appDao.UnderlyingDB()
	if err := db.WithContext(ctx).
		Raw(`SELECT COUNT(*) 
		     FROM sandbox_runtime
		     WHERE template_id IN ?
		       AND status = 'running'
		       AND deleted = 0`, ids).
		Scan(&running).Error; err != nil {
		return false
	}
	return running < int(limitCnt)
}

func getRunningOccupancyAndLimit(ctx context.Context, appID int32) (int64, int64, error) {
	appDao := dao.Use(resource.OrmMain).App

	var app model.App
	if err := appDao.Where(appDao.ID.Eq(appID)).Scan(&app); err != nil {
		return 0, 0, err
	}
	limitCnt := app.LimitCnt
	if limitCnt <= 0 {
		limitCnt = 5
	}

	var occupancyCount int64
	db := appDao.UnderlyingDB()
	if err := db.WithContext(ctx).
		Raw(`SELECT COUNT(*)
		     FROM sandbox_runtime
		     WHERE app_id = ?
		       AND status IN ?
		       AND deleted = 0`, appID, []string{constant.StatusRunning, constant.StatusPending}).
		Scan(&occupancyCount).Error; err != nil {
		return 0, 0, err
	}
	return occupancyCount, limitCnt, nil
}

func updateMaxOccupancyPeak(ctx context.Context, appID int32, occupancy int64) error {
	appDao := dao.Use(resource.OrmMain).App
	db := appDao.UnderlyingDB()
	return db.WithContext(ctx).Exec(`UPDATE app
		SET max_occupancy_cnt = GREATEST(IFNULL(max_occupancy_cnt, 0), ?)
		WHERE id = ?`, occupancy, appID).Error
}

// CheckAndAlertResourceUsage 检查沙箱运行实例数是否达到水位阈值，若达到则向管理员发送告警
func (s *sandboxRuntimeService) CheckAndAlertResourceUsage(ctx context.Context, appID int32) error {
	logger := resource.LoggerService
	go func() {
		bgCtx := context.Background()

		appDao := dao.Use(resource.OrmMain).App
		var app model.App
		if err := appDao.Where(appDao.ID.Eq(appID)).Scan(&app); err != nil {
			logger.Error(bgCtx, "query app failed when alert resource usage",
				logit.Int32("appID", appID),
				logit.String("error", err.Error()))
			return
		}

		runningCount, limitCnt, err := getRunningOccupancyAndLimit(bgCtx, appID)
		if err != nil {
			logger.Error(bgCtx, "query running count failed when alert resource usage",
				logit.Int32("appID", appID),
				logit.String("error", err.Error()))
			return
		}

		if err = updateMaxOccupancyPeak(bgCtx, appID, runningCount); err != nil {
			logger.Error(bgCtx, "update max occupancy peak failed",
				logit.Int32("appID", appID),
				logit.Int64("runningCount", runningCount),
				logit.String("error", err.Error()))
		}

		threshold := float64(limitCnt) * 0.8
		if float64(runningCount) < threshold {
			return
		}

		workspaceID := int64(app.WorkspaceID)
		if workspaceID <= 0 {
			logger.Warning(bgCtx, "workspace id invalid when alert resource usage",
				logit.Int32("appID", appID),
				logit.Int64("workspaceID", workspaceID))
			return
		}

		loginUser, _ := ctx.Value(constant.CtxLoginUserKey).(string)
		if loginUser == "" {
			loginUser = app.Owner
		}
		workspaceName := ""
		if ok, workspaceInfo := mcpcenter.GetMcpCenterApi().GetWorkspaceInfo(int(workspaceID), loginUser); ok && workspaceInfo != nil {
			workspaceName = workspaceInfo.Name
		}

		admins, err := mcpcenter.GetMcpCenterApi().GetWorkspaceAdmins(int(workspaceID), loginUser)
		if err != nil {
			logger.Error(bgCtx, "get workspace admins failed when alert resource usage",
				logit.Int64("workspaceID", workspaceID),
				logit.Int32("appID", appID),
				logit.String("error", err.Error()))
			return
		}
		if len(admins) == 0 {
			logger.Warning(bgCtx, "workspace admins empty when alert resource usage",
				logit.Int64("workspaceID", workspaceID),
				logit.Int32("appID", appID))
			return
		}

		status := fmt.Sprintf("您的OneTool空间 %s 当前运行沙箱实例数达到 %d （上限为 %d），请评估风险，如需扩容请联系 liangbao、yangwencai、sulin01", workspaceName, runningCount, limitCnt)
		for _, admin := range admins {
			if ok, msg := mcpcenter.GetMcpCenterApi().UseAIRobot(admin, "沙箱实例水位告警", workspaceName, "资源水位", status, int(workspaceID)); !ok {
				logger.Error(bgCtx, "send resource usage alert failed",
					logit.String("admin", admin),
					logit.Int64("workspaceID", workspaceID),
					logit.String("msg", msg))
			}
		}
	}()
	return nil
}

// ReleaseSandboxPVC 立即释放处于 kill/expire_kill 状态的 sandbox 的 PVC/PV 资源。
// 正常情况下 PVC 需要等 reclaim_after（15天）后由后台定时任务清理，
// 此接口允许用户确认不再需要持久化数据后立即释放。
func (s *sandboxRuntimeService) ReleaseSandboxPVC(ctx context.Context, sandboxUUID string) error {
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxUUID)).First()
	if err != nil {
		return fmt.Errorf("sandbox runtime not found: %w", err)
	}

	// 仅允许 kill / expire_kill 状态的 sandbox 释放 PV
	if runtime.Status != constant.StatusKill && runtime.Status != constant.StatusExpiredKill {
		return fmt.Errorf("sandbox status is %s, only kill/expire_kill status allowed", runtime.Status)
	}
	if !runtime.UsePvc {
		return fmt.Errorf("sandbox does not use PVC")
	}

	// 查询 PVC binding
	binding, err := GetSandboxPVCBinding(ctx, sandboxUUID)
	if err != nil {
		return fmt.Errorf("pvc binding not found: %w", err)
	}
	if binding.Status != model.SandboxPVCStatusKill && binding.Status != model.SandboxPVCStatusActive {
		return fmt.Errorf("pvc binding status is %s, cannot release", binding.Status)
	}

	// 删除 K8s PVC/PV 资源
	cluster := resource.ClusterManager.GetClusterByName(binding.Cluster)
	if cluster == nil {
		_ = resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCBinding{}).Where("id = ?", binding.ID).
			Updates(map[string]interface{}{"status": model.SandboxPVCStatusFailed, "update_time": time.Now()}).Error
		return fmt.Errorf("cluster %s not found", binding.Cluster)
	}

	if err = cluster.DeletePVC(ctx, binding.PVCName); err != nil {
		_ = resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCBinding{}).Where("id = ?", binding.ID).
			Updates(map[string]interface{}{"status": model.SandboxPVCStatusFailed, "update_time": time.Now()}).Error
		return fmt.Errorf("failed to delete PVC: %w", err)
	}
	// BOS 是静态供给（手动创建 PV），需要手动删除 PV；CFS/local-path/hp1 是动态供给，provisioner 根据 reclaimPolicy:Delete 自动清理
	if IsBOSStorageClass(binding.StorageClass) {
		pvName := k8s.BuildBOSPVName(sandboxUUID)
		if pvErr := cluster.DeletePV(ctx, pvName); pvErr != nil {
			resource.LoggerService.Warning(ctx, "delete BOS PV failed",
				logit.String("sandboxUUID", sandboxUUID),
				logit.String("pvName", pvName),
				logit.String("error", pvErr.Error()))
		}
	}

	// PVC/PV 删除成功，标记 binding 为 deleted=1；sandbox 继续保持 kill/expire_kill 状态
	_ = resource.OrmMain.WithContext(ctx).Model(&model.SandboxPVCBinding{}).Where("id = ?", binding.ID).
		Updates(map[string]interface{}{
			"status":        model.SandboxPVCStatusReleased,
			"deleted":       true,
			"reclaim_after": nil,
			"update_time":   time.Now(),
		}).Error

	return nil
}

// sendSandboxRestartMessage 发送沙箱启动消息通知用户
// 从环境变量中获取 INFOFLOW_APP_KEY 和 INFOFLOW_APP_SECRET，发送消息给创建用户
// 此方法不影响主流程，仅记录日志
func SendSandboxRestartMessage(ctx context.Context, envVars map[string]string, createUser, sandboxUUID string, message string, appID int32) {
	// 如果用户在免空闲释放白名单或沙箱auto-wake或非OpenClaw沙箱，则不发送通知
	if common.ShouldSkipMessage(createUser, sandboxUUID, appID) {
		resource.LoggerService.Warning(ctx, "skip sending restart message",
			logit.String("sandboxUUID", sandboxUUID),
			logit.Int("appID", int(appID)),
			logit.String("createUser", createUser))
		return
	}

	appKey, ok := envVars["INFOFLOW_APP_KEY"]
	if !ok || appKey == "" {
		resource.LoggerService.Warning(ctx, "INFOFLOW_APP_KEY not found in envVars",
			logit.String("sandboxUUID", sandboxUUID))
	}

	appSecret, ok := envVars["INFOFLOW_APP_SECRET"]
	if !ok || appSecret == "" {
		resource.LoggerService.Warning(ctx, "INFOFLOW_APP_SECRET not found in envVars",
			logit.String("sandboxUUID", sandboxUUID))
	}

	client, err := card.NewSendMessageClient(appKey, appSecret)
	if err != nil {
		resource.LoggerService.Warning(ctx, "failed to create send message client",
			logit.String("sandboxUUID", sandboxUUID))
	}
	if err := client.SendTextMessage(ctx, createUser, message, appKey, appSecret); err != nil {
		resource.LoggerService.Warning(ctx, "failed to send message",
			logit.String("sandboxUUID", sandboxUUID))
	}
}

// GetSandboxRuntimeEnvVars 获取沙箱运行时的环境变量
func (s *sandboxRuntimeService) GetSandboxRuntimeEnvVars(ctx context.Context, sandboxRuntimeID int64) (map[string]string, error) {
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	var sandboxRuntime model.SandboxRuntime

	err := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeID)).Scan(&sandboxRuntime)
	if err != nil {
		return nil, fmt.Errorf("sandbox runtime not found: %w", err)
	}

	if sandboxRuntime.ID == 0 {
		return nil, fmt.Errorf("sandbox runtime not found")
	}

	envVars := make(map[string]string)
	if sandboxRuntime.EnvirVars != "" {
		if err := json.Unmarshal([]byte(sandboxRuntime.EnvirVars), &envVars); err != nil {
			return nil, fmt.Errorf("failed to parse envVars: %w", err)
		}
	}

	return envVars, nil
}

// OrphanedPodResult 孤立 pod 查询结果
type OrphanedPodResult struct {
	ClusterName string   `json:"clusterName"`
	PodNames    []string `json:"podNames"`
}

// GetOrphanedPods 查找集群中存活的孤立 pod（不在 sandbox_runtime 表中的运行中 pod 且存活超过 1 小时）
// 返回各个集群下符合条件的 pod 名字列表
func (s *sandboxRuntimeService) GetOrphanedPods(ctx context.Context) (map[string][]string, error) {
	// 1. 查询数据库中所有 status 为 running 的记录
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runningRuntimes, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.Status.Eq(constant.StatusRunning)).Find()
	if err != nil {
		return nil, fmt.Errorf("query running sandbox runtimes failed: %w", err)
	}

	// 2. 按 cluster 字段分组，建立 sandbox_uuid -> pod_name 的映射
	// 注意：这里比较的是 sandbox_uuid 和 pod 名字
	clusterRuntimes := make(map[string][]string) // cluster -> []sandbox_uuid
	podNameToCluster := make(map[string]string)  // sandbox_uuid -> cluster

	for _, rt := range runningRuntimes {
		// 使用 pod_name 作为 sandbox_uuid，或者直接使用 sandbox_uuid
		// 根据 pod 的创建逻辑，pod 名字可能就是 sandbox_uuid
		sandboxUUID := rt.SandboxUUID
		podName := rt.PodName
		if podName != "" {
			sandboxUUID = podName
		}

		clusterRuntimes[rt.Cluster] = append(clusterRuntimes[rt.Cluster], sandboxUUID)
		podNameToCluster[sandboxUUID] = rt.Cluster
	}

	// 3. 遍历所有集群，获取运行中的 pod
	result := make(map[string][]string)

	for clusterName, k8sCluster := range resource.ClusterManager.Clusters {
		runningPods, err := k8sCluster.ListRunningPods(ctx)
		if err != nil {
			resource.LoggerService.Error(ctx, "list running pods failed",
				logit.String("cluster", clusterName),
				logit.String("error", err.Error()))
			continue
		}

		// 获取该集群在数据库中的 sandbox_uuid 列表
		dbPodNames := make(map[string]bool)
		for _, uuid := range clusterRuntimes[clusterName] {
			dbPodNames[uuid] = true
		}

		// 4. 比较 pod 名字，找出不在数据库中且存活超过 1 小时的 pod
		var orphanedPods []string
		oneHourAgo := time.Now().Add(-time.Hour)

		for _, pod := range runningPods {
			if !dbPodNames[pod.Name] && pod.CreatedAt.Before(oneHourAgo) {
				orphanedPods = append(orphanedPods, pod.Name)
			}
		}

		if len(orphanedPods) > 0 {
			result[clusterName] = orphanedPods
		}
	}

	return result, nil
}

// InjectICodeSSHKeys 向沙箱注入icode的SSH公钥和私钥
// 流程: 1. 调用imagellan获取SSH密钥 2. 写入沙箱临时目录 3. 移动到~/.ssh/并设置权限600
func InjectICodeSSHKeys(sandboxUUID string, token string, username string, repo string) error {
	if len(repo) == 0 {
		repo = "baidu/agile/agent"
	}
	sshKeyResp, err := magellanauth.GetAuthApi().GetICodeSSHKey(username, repo)
	if err != nil {
		return fmt.Errorf("get icode ssh key failed: %w", err)
	}
	if sshKeyResp == nil || len(sshKeyResp.PrivateKey) == 0 {
		return fmt.Errorf("get icode ssh key returned empty")
	}

	runtimeApi := runtimeapi.GetRuntimeApi()

	// 写入临时目录
	privateKeyPath := "/tmp/ssh_inject/id_rsa"
	publicKeyPath := "/tmp/ssh_inject/id_rsa.pub"

	if !runtimeApi.WriteFile(sandboxUUID, token, privateKeyPath, []byte(sshKeyResp.PrivateKey)) {
		return fmt.Errorf("write private key to sandbox failed")
	}
	if len(sshKeyResp.PublicKey) > 0 {
		if !runtimeApi.WriteFile(sandboxUUID, token, publicKeyPath, []byte(sshKeyResp.PublicKey)) {
			return fmt.Errorf("write public key to sandbox failed")
		}
	}
	sshConfigFilename := "~/.ssh/config"
	sshConfigContent := "Host *\nHostkeyAlgorithms +ssh-rsa\nPubkeyAcceptedKeyTypes +ssh-rsa"
	if !runtimeApi.WriteFile(sandboxUUID, token, sshConfigFilename, []byte(sshConfigContent)) {
		return fmt.Errorf("write public key to sandbox failed")
	}
	// 通过StartCommand移动密钥到~/.ssh/并设置权限
	moveCmd := "mkdir -p ~/.ssh\n" +
		"mv /tmp/ssh_inject/id_rsa ~/.ssh/id_rsa\n" +
		"mv /tmp/ssh_inject/id_rsa.pub ~/.ssh/id_rsa.pub\n" +
		"chmod 600 ~/.ssh/id_rsa\n" +
		"chmod 600 ~/.ssh/id_rsa.pub\n" +
		"chmod 600 ~/.ssh/config\n" +
		"rm -rf /tmp/ssh_inject"
	if !runtimeApi.StartCommandSync(sandboxUUID, token, moveCmd) {
		return fmt.Errorf("move ssh keys and set permissions failed")
	}

	return nil
}

func InjectEnvVariables(sandboxUUID string, token string, envVariables map[string]string) error {
	if envVariables == nil || len(envVariables) == 0 {
		return nil
	}

	// 写入临时目录
	envFilename := "~/.sandbox_env.json"
	runtimeApi := runtimeapi.GetRuntimeApi()

	envBytes, _ := json.Marshal(envVariables)

	if !runtimeApi.WriteFile(sandboxUUID, token, envFilename, envBytes) {
		return fmt.Errorf("write private key to sandbox failed")
	}

	return nil
}

func InjectUgateToken(sandboxUUID string, token string, username string, ugateToken string) error {

	runtimeApi := runtimeapi.GetRuntimeApi()

	// 写入临时目录
	ugateTokenFilename := "~/.config/uuap/.eac_ugate_token_" + username

	if !runtimeApi.WriteFile(sandboxUUID, token, ugateTokenFilename, []byte(ugateToken)) {
		return fmt.Errorf("write private key to sandbox failed")
	}

	// 通过StartCommand移动密钥到~/.ssh/并设置权限
	moveCmd := "chmod 600 " + ugateTokenFilename
	if !runtimeApi.StartCommandSync(sandboxUUID, token, moveCmd) {
		return fmt.Errorf("set permissions for ugate token failed")
	}
	return nil
}
