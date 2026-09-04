package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/card"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/authcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/devopsauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/runtimeapi"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"
	corev1 "k8s.io/api/core/v1"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/eacauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/magellanauth"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/service"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
)

type CreateSandboxRequest struct {
	SandboxID           string            `json:"sandboxID"`
	TemplateID          string            `json:"templateID"`
	AllowInternetAccess bool              `json:"allow_internet_access"`
	AutoPause           bool              `json:"autoPause"`
	MetaData            map[string]string `json:"metadata"`
	EnvVars             map[string]string `json:"envVars"`
	Timeout             int32             `json:"timeout"`
}

type CreateSandboxResponse struct {
	TemplateID      string `json:"templateID"`
	SandboxID       string `json:"sandboxID"`
	ClientID        string `json:"clientID"`
	Domain          string `json:"domain"`
	EnvdVersion     string `json:"envdVersion"`
	EnvdAccessToken string `json:"envdAccessToken"`
}

type GetSandboxResponse struct {
	TemplateID      string            `json:"templateID"`
	SandboxID       string            `json:"sandboxID"`
	ClientID        string            `json:"clientID,omitempty"`
	Domain          string            `json:"domain,omitempty"`
	EnvdVersion     string            `json:"envdVersion,omitempty"`
	EnvdAccessToken string            `json:"envdAccessToken,omitempty"`
	CpuCount        int32             `json:"cpuCount"`
	EndAt           string            `json:"endAt"`
	MemoryMB        int32             `json:"memoryMB"`
	DiskSizeMB      int32             `json:"diskSizeMB"`
	StartedAt       string            `json:"startedAt"`
	State           string            `json:"state"`
	OriginStatus    string            `json:"originStatus"`
	Metadata        map[string]string `json:"metadata"`
	LogURL          string            `json:"logURL"`
}

var k8sStatusMapping = map[corev1.PodPhase]string{
	corev1.PodPending:   constant.StatusPending,
	corev1.PodRunning:   constant.StatusRunning,
	corev1.PodSucceeded: constant.StatusSucceeded,
	corev1.PodFailed:    constant.StatusFailed,
}

// e2bStatusMapping 当前E2B sdk仅支持E2bStatusRunning和E2bStatusPaused
var e2bStatusMapping = map[string]string{
	constant.StatusPending:     constant.E2bStatusPaused,
	constant.StatusKill:        constant.E2bStatusPaused,
	constant.StatusExpiredKill: constant.E2bStatusPaused,
	constant.StatusReleased:    constant.E2bStatusPaused,
	constant.StatusSucceeded:   constant.E2bStatusPaused,
	constant.StatusFailed:      constant.E2bStatusPaused,
	constant.StatusRunning:     constant.E2bStatusRunning,
}

type SetSandboxTimeoutRequest struct {
	Timeout int32 `json:"timeout"`
}

type RestoreSandboxRequest struct {
	Timeout int32             `json:"timeout"`
	EnvVars map[string]string `json:"envVars"`
}

type GetSandboxTimeoutResponse struct {
	Timeout int32 `json:"timeout"`
}

func mappingE2bStatus(status string) string {
	value, ok := e2bStatusMapping[status]
	if !ok {
		value = constant.E2bStatusPaused
	}
	return value
}

func getDevopsJwtEnvToken(envVars map[string]string) string {

	if envVars == nil {
		return ""
	}
	devopsJwtToken, ok := envVars[constant.DevopsJwtEnvKey]
	if !ok {
		return ""
	}
	return devopsJwtToken
}

func getNodeSelector(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	nodeSelector := make(map[string]string)
	nodeSelectorStr, ok := metadata[constant.MetadataNodeSelectorKey]
	if !ok {
		return nil
	}
	err := json.Unmarshal([]byte(nodeSelectorStr), &nodeSelector)
	if err != nil {
		return nil
	}
	return nodeSelector
}

func getSandboxName(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	sandboxName, ok := metadata[constant.MetadataNameKey]
	if !ok {
		return ""
	}
	return sandboxName
}

func getImage(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	image, ok := metadata[constant.MetadataImageKey]
	if !ok {
		return ""
	}
	return image
}

func getModule(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	image, ok := metadata[constant.MetadataModuleKey]
	if !ok {
		return ""
	}
	return image
}

func getSandboxCreator(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	creator, ok := metadata[constant.MetadataCreatorKey]
	if !ok {
		return ""
	}
	return creator
}

func getCodeBean(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	codeBean, ok := metadata[constant.MetadataCodeBeanKey]
	if !ok {
		return ""
	}
	return codeBean
}

func getPVCMountPath(ctx context.Context, metadata map[string]string) string {
	// 当前实现仅支持把 PVC 作为 workspace 卷的数据源，实际挂载点固定为 /workspace。
	// metadata 里传入的 pvcMountPath 不会改变 Pod 的 volumeMount，为避免误导这里强制返回 /workspace。
	if metadata != nil {
		if v, ok := metadata["agent-sandbox/pvcMountPath"]; ok && v != "" && v != k8s.WorkspaceVolumeMountPath {
			resource.LoggerService.Warning(ctx, "agent-sandbox/pvcMountPath ignored (workspace mount path is fixed)",
				logit.String("requestedMountPath", v),
				logit.String("actualMountPath", k8s.WorkspaceVolumeMountPath))
		}
	}
	return k8s.WorkspaceVolumeMountPath
}

func shouldUsePVC(metadata map[string]string) bool {
	if metadata == nil {
		return false
	}
	// 兼容两个 key，避免后续改名带来不兼容。
	if v, ok := metadata["agent-sandbox/usePVC"]; ok && v != "" {
		b, _ := strconv.ParseBool(v)
		return b
	}
	if v, ok := metadata["usePVC"]; ok && v != "" {
		b, _ := strconv.ParseBool(v)
		return b
	}
	return false
}

// getPVCSize 从 metadata 解析 PVC 容量大小(GB)，返回 nil 表示使用默认值。
// 范围: 1-100GB，超出范围返回 error。
func getPVCSize(metadata map[string]string) (*int, error) {
	if metadata == nil {
		return nil, nil
	}
	sizeStr, ok := metadata["agent-sandbox/pvcSize"]
	if !ok || sizeStr == "" {
		return nil, nil
	}
	size, err := strconv.Atoi(sizeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid pvcSize: %s", sizeStr)
	}
	if size < 1 || size > 100 {
		return nil, fmt.Errorf("pvcSize must be between 1 and 100, got %d", size)
	}
	return &size, nil
}

// getStorageClass 从 metadata 解析存储类名称，返回 nil 表示使用默认值。
func getStorageClass(metadata map[string]string) *string {
	if metadata == nil {
		return nil
	}
	if v, ok := metadata["agent-sandbox/storageClass"]; ok && v != "" {
		return &v
	}
	return nil
}

func checkAppGrantScope(ctx context.Context, appID int32, username string, devopsJwtToken string) bool {
	if devopsJwtToken != "" {
		jwtUsername := devopsauth.GetAuthApi().ValidateUser(devopsJwtToken)
		if username == jwtUsername {
			resource.LoggerService.Notice(ctx, "checkAppGrantScope jwt success",
				logit.Int32("appId", appID),
				logit.String("username", username),
			)
			return true
		}
	}
	appGrantScopeDao := dao.Use(resource.OrmMain).AppGrantScope

	userInfo, err := eacauth.GetEacAuthApi().GetUserByUsername(username, "username,orgChainNameEn")

	if err == nil && userInfo != nil && userInfo.OrgChainNameEn != "" {
		grantDeparts, queryDeptErr := appGrantScopeDao.Where(
			appGrantScopeDao.AppID.Eq(appID),
			appGrantScopeDao.Type.Eq("DEPT"),
		).Find()
		if queryDeptErr != nil {
			resource.LoggerService.Warning(ctx, "checkAppGrantScope user failed,query dept scope fail",
				logit.Int32("appId", appID),
				logit.String("username", username),
				logit.String("orgChainNameEn", userInfo.OrgChainNameEn),
			)
		} else {
			for _, grantDepart := range grantDeparts {
				if grantDepart.Subject == "*" || strings.HasPrefix(userInfo.OrgChainNameEn, grantDepart.Subject) {
					resource.LoggerService.Notice(ctx, "checkAppGrantScope dept success",
						logit.Int32("appId", appID),
						logit.String("username", username),
						logit.String("orgChainNameEn", userInfo.OrgChainNameEn),
						logit.String("grantDepart", grantDepart.Subject),
					)
					return true
				}
			}
		}
	} else {
		if err != nil {
			resource.LoggerService.Warning(ctx, "checkAppGrantScope user failed,get user info fail",
				logit.Int32("appId", appID),
				logit.String("username", username),
			)
		} else {
			if userInfo == nil || userInfo.OrgChainNameEn == "" {
				resource.LoggerService.Warning(ctx, "checkAppGrantScope user failed,get user info dept blank",
					logit.Int32("appId", appID),
					logit.String("username", username),
				)
			}
		}
	}

	// 查询授权为全部范围或者配置了具体用户名的记录
	count, err := appGrantScopeDao.Where(
		appGrantScopeDao.AppID.Eq(appID),
		appGrantScopeDao.Type.Eq("USER"),
		appGrantScopeDao.Subject.In("*", username),
	).Count()
	if err != nil {
		resource.LoggerService.Warning(ctx, "checkAppGrantScope user failed,query user scope fail",
			logit.Int32("appId", appID),
			logit.String("username", username),
		)
		return false
	}
	if count > 0 {
		resource.LoggerService.Notice(ctx, "checkAppGrantScope user success",
			logit.Int32("appId", appID),
			logit.String("username", username),
		)
		return true
	}
	resource.LoggerService.Warning(ctx, "checkAppGrantScope user failed",
		logit.Int32("appId", appID),
		logit.String("username", username),
	)
	return false
}

func getPostScript(metadata map[string]string, tpl *model.Template) string {
	if metadata == nil {
		return ""
	}
	script, ok := metadata[constant.MetadataPostScriptKey]
	if ok && len(script) > 0 {
		return script
	}
	return tpl.PostScript
}

// parseAndValidateMetadataPvcs 从 metadata 解析 agent-sandbox/pvcs 字段，
// 校验每个 pvcName 是否在 app_pvc_instance 表中存在，返回可用于 BindPvcs 的列表。
func parseAndValidateMetadataPvcs(ctx context.Context, metadata map[string]string, appID int32) ([]*k8s.PvcBinding, error) {
	pvcsStr, ok := metadata[constant.MetadataPvcsKey]
	if !ok || pvcsStr == "" {
		return nil, nil
	}

	var pvcInfos []service.PvcMountInfo
	if err := json.Unmarshal([]byte(pvcsStr), &pvcInfos); err != nil {
		return nil, fmt.Errorf("invalid %s format: %w", constant.MetadataPvcsKey, err)
	}

	if len(pvcInfos) == 0 {
		return nil, nil
	}

	result := make([]*k8s.PvcBinding, 0, len(pvcInfos))
	for _, info := range pvcInfos {
		if info.Name == "" || info.MountPath == "" {
			return nil, fmt.Errorf("pvcName and mountPath are required in %s", constant.MetadataPvcsKey)
		}
		// 校验 pvcName 在 app_pvc_instance 表中存在
		instance, err := service.GetAppPvcInstanceByName(ctx, appID, info.Name)
		if err != nil {
			return nil, fmt.Errorf("pvc %s not found in app pvc instances: %w", info.Name, err)
		}

		result = append(result, &k8s.PvcBinding{
			PvcName:   instance.PvcName,
			SubPath:   info.SubPath,
			MountPath: info.MountPath,
		})
	}
	return result, nil
}

func handleListSandbox(ctx context.Context, req ghttp.Request) ghttp.Response {
	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	templateDao := dao.Use(resource.OrmMain).Template

	runtimes, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.AppID.Eq(appID),
		sandboxRuntimeDao.Status.Eq(constant.StatusRunning),
	).Find()
	if err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}
	templateIdMap := make(map[int32]any, 0)
	for _, runtime := range runtimes {
		templateIdMap[runtime.TemplateID] = nil
	}
	templateIDs := make([]int32, 0)
	for k := range templateIdMap {
		templateIDs = append(templateIDs, k)
	}
	templates, err := templateDao.Where(templateDao.ID.In(templateIDs...)).Find()
	templateMap := make(map[int32]*model.Template, 0)
	for _, template := range templates {
		templateMap[template.ID] = template
	}
	runtimesRes := make([]*GetSandboxResponse, 0)
	for _, runtime := range runtimes {
		metadata := make(map[string]string)
		if len(runtime.Metadata) > 0 {
			err = json.Unmarshal([]byte(runtime.Metadata), &metadata)
			if err != nil {
				resource.LoggerService.Warning(ctx, "list sandbox parse metadata error", logit.Int32("appId", appID),
					logit.String("sandboxId", runtime.SandboxUUID))
			}
		}
		logUrl := ""
		if len(runtime.PodName) > 0 {
			logUrl = magellanauth.GetAuthApi().GetLogURL(runtime.LogUUID, runtime.PodName)
		} else {
			logUrl = magellanauth.GetAuthApi().GetLogURL(runtime.LogUUID, runtime.SandboxUUID)
		}
		metadata["logURL"] = logUrl
		expireTime := runtime.StartTime.Add(time.Second * time.Duration(runtime.Timeout))
		template := templateMap[runtime.TemplateID]
		if template == nil {
			resource.LoggerService.Warning(ctx, "list sandbox get template failed", logit.Int32("appId", appID),
				logit.String("sandboxId", runtime.SandboxUUID))
			continue
		}
		runtimeRes := &GetSandboxResponse{
			TemplateID:   template.Name,
			SandboxID:    runtime.SandboxUUID,
			State:        mappingE2bStatus(runtime.Status),
			OriginStatus: runtime.Status,
			ClientID:     constant.E2bClientID,
			CpuCount:     runtime.CPU / 1000,
			MemoryMB:     runtime.Memory,
			DiskSizeMB:   runtime.Disk * 1024,
			StartedAt:    runtime.StartTime.Format(time.RFC3339),
			EndAt:        expireTime.Format(time.RFC3339),
			Metadata:     metadata,
			LogURL:       logUrl,
		}
		runtimesRes = append(runtimesRes, runtimeRes)
	}
	return response.NewGResponse(http.StatusOK, runtimesRes)
}

func handleCreateSandbox(ctx context.Context, req ghttp.Request) ghttp.Response {
	request := &CreateSandboxRequest{}

	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}
	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	realUser := ctx.Value(constant.CtxLoginUserKey).(string)
	loginUser := realUser
	if len(loginUser) == 0 {
		loginUser = getSandboxCreator(request.MetaData)
	}
	templateDao := dao.Use(resource.OrmMain).Template
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	appPvcDao := dao.Use(resource.OrmMain).AppPvcBinding
	appDao := dao.Use(resource.OrmMain).App
	resource.LoggerService.Notice(ctx, "create new sandbox",
		logit.Int32("appId", appID),
		logit.String("templateID", request.TemplateID),
	)
	app, err := appDao.Where(appDao.ID.Eq(appID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "get app info failed", logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}

	// 检查 app 是否存在 account
	if app.Account == "" {
		resource.LoggerService.Warning(ctx, "app account is empty, cannot create sandbox",
			logit.Int32("appID", appID))
		return response.NewGResponse(http.StatusBadRequest,
			response.New("该应用未配置资源账号，无法创建实例", response.ParamsErrCode))
	}

	limitCnt := app.LimitCnt
	currentCnt, err := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.AppID.Eq(appID),
		sandboxRuntimeDao.Status.In(constant.StatusRunning, constant.StatusPending),
	).Count()
	if err != nil {
		resource.LoggerService.Error(ctx, "query current sandbox count failed", logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}
	if limitCnt > 0 && currentCnt >= limitCnt {
		resource.LoggerService.Warning(ctx, "sandbox count limit exceeded",
			logit.Int32("appId", appID),
			logit.Int64("currentCount", currentCnt),
			logit.Int64("limitCount", limitCnt))
		return response.NewGResponse(http.StatusBadRequest,
			response.ContainerCountLimitErr)
	}

	if request.TemplateID == "" {
		resource.LoggerService.Error(ctx, "create new sandbox fail,templateID is empty")
		return response.NewGResponse(http.StatusBadRequest,
			response.TemplateNotFoundErr)
	}
	tpl, err := templateDao.Where(
		templateDao.AppID.Eq(appID),
		templateDao.Name.Eq(request.TemplateID),
		templateDao.Deleted.Is(false),
	).First()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			tpl, err = templateDao.Where(templateDao.AppID.Eq(0), templateDao.Name.Eq(request.TemplateID)).First()
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					resource.LoggerService.Error(ctx, "create new sandbox fail,template not found",
						logit.Int32("appId", appID),
						logit.String("templateID", request.TemplateID),
					)
					return response.NewGResponse(http.StatusBadRequest,
						response.TemplateNotFoundErr)
				} else {
					resource.LoggerService.Error(ctx, "query template failed",
						logit.String("error", err.Error()))
					return response.NewGResponse(http.StatusBadRequest,
						response.QueryDbErr)
				}
			}
		} else {
			resource.LoggerService.Error(ctx, "query template failed",
				logit.String("error", err.Error()))
			return response.NewGResponse(http.StatusBadRequest,
				response.QueryDbErr)
		}
	}

	startTime := time.Now()
	// 设置 Prometheus 指标标签
	appIDLabel := fmt.Sprintf("%d", appID)
	templateIDLabel := fmt.Sprintf("%d", tpl.ID)

	var ugateToken string
	var ugateSandboxToken string

	environVarsJSON, err := json.Marshal(request.EnvVars)
	if err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	var codeBean *k8s.CodeBean
	var codeBeans []*k8s.CodeBean
	metadataBytes := []byte("{}")
	if request.MetaData != nil && len(request.MetaData) > 0 {
		metadataBytes, err = json.Marshal(request.MetaData)
		if err != nil {
			return response.NewGResponse(http.StatusInternalServerError,
				response.MarshalJsonErr)
		}
		codeBeanStr := getCodeBean(request.MetaData)
		if len(codeBeanStr) > 0 {
			codeBeanObj := &k8s.CodeBean{}
			err = json.Unmarshal([]byte(codeBeanStr), codeBeanObj)
			if err != nil {
				return response.NewGResponse(http.StatusInternalServerError,
					response.UnmarshalJsonErr)
			}
			codeBean = codeBeanObj
			hasGrant := checkAppGrantScope(ctx, appID, codeBean.Username, getDevopsJwtEnvToken(request.EnvVars))
			// 登录用户一致,或者满足平台授权
			if !(hasGrant || codeBean.Username == loginUser) {
				return response.NewGResponse(http.StatusForbidden,
					response.UserNotGrantForAppErr)
			}
		}
		codeBeansStr, ok := request.MetaData["codeBeans"]
		if ok {
			err = json.Unmarshal([]byte(codeBeansStr), &codeBeans)
			if err != nil {
				return response.NewGResponse(http.StatusInternalServerError,
					response.UnmarshalJsonErr)
			}
			for _, _codeBean := range codeBeans {
				hasGrant := checkAppGrantScope(ctx, appID, _codeBean.Username, getDevopsJwtEnvToken(request.EnvVars))
				// 登录用户一致,或者满足平台授权
				if !(hasGrant || _codeBean.Username == loginUser) {
					return response.NewGResponse(http.StatusForbidden,
						response.UserNotGrantForAppErr)
				}
			}
		}
	}

	// 优先尝试从预热池获取可用的pod
	poolService := service.NewSandboxRuntimePoolService()
	poolItem, err := poolService.AcquireAndRemove(ctx, tpl.ID, loginUser)
	if err != nil {
		resource.LoggerService.Warning(ctx, "acquire from pool failed, fallback to create new", logit.String("error", err.Error()))
	}
	if poolItem != nil {
		// 从预热池获取成功，直接使用
		resource.LoggerService.Notice(ctx, "using pool item", logit.Int64("id", poolItem.ID), logit.String("sandboxUUID", poolItem.SandboxUUID))

		currentTime := time.Now()
		sandboxRuntime := model.SandboxRuntime{
			AppID:       appID,
			TemplateID:  tpl.ID,
			ImageID:     tpl.ImageID,
			CPU:         poolItem.CPU,
			Memory:      poolItem.Memory,
			Disk:        poolItem.Disk,
			EnvirVars:   string(environVarsJSON),
			CreateTime:  currentTime,
			StartTime:   currentTime,
			Timeout:     request.Timeout,
			TimeoutTime: service.GetTimeoutTime(request.Timeout, currentTime),
			Status:      constant.StatusRunning,
			CreateUser:  loginUser,
			Name:        getSandboxName(request.MetaData),
			ReleaseName: "",
			Icode:       false,
			Ducc:        false,
			SandboxUUID: poolItem.SandboxUUID,
			PortIP:      poolItem.PortIP,
			NodeIP:      poolItem.NodeIP,
			CommandPort: poolItem.CommandPort,
			CodePort:    poolItem.CodePort,
			Token:       poolItem.Token,
			Cluster:     poolItem.Cluster,
			LogUUID:     poolItem.LogUUID,
			Metadata:    string(metadataBytes),
			Remind:      false,
			PeriodTime:  0,
			CodeBean:    "{}",
			PostScript:  "",
			PodName:     poolItem.PodName,
		}

		// 写库
		if err := sandboxRuntimeDao.Create(&sandboxRuntime); err != nil {
			resource.LoggerService.Error(ctx, "create sandboxRuntime from pool failed", logit.String("error", err.Error()))
			return response.NewGResponse(http.StatusBadRequest,
				response.QueryDbErr)
		}
		cluster := resource.ClusterManager.GetClusterByName(poolItem.Cluster)

		// 设置Redis缓存
		resource.RedisClient.Set(ctx, constant.BackendIPPrefix+poolItem.SandboxUUID, poolItem.PortIP, 0)
		if cluster.GetNetworkType() == constant.NetworkTypeVpc {
			_ = resource.RedisClient.Set(context.Background(), constant.NginxEndpointRedisKey+poolItem.SandboxUUID, cluster.GetNginxEndpoint()).Err()
		}

		res := CreateSandboxResponse{
			TemplateID:      tpl.Name,
			SandboxID:       sandboxRuntime.SandboxUUID,
			ClientID:        constant.E2bClientID,
			Domain:          constant.SandboxDomain,
			EnvdVersion:     constant.E2bEnvdVersion,
			EnvdAccessToken: sandboxRuntime.Token,
		}
		if err := service.NewSandboxRuntimeService().CheckAndAlertResourceUsage(ctx, appID); err != nil {
			resource.LoggerService.Warning(ctx, "check and alert resource usage failed after create sandbox from pool",
				logit.Int32("appID", appID),
				logit.String("error", err.Error()))
		}
		// 注入icode SSH密钥
		if codeBean != nil && len(codeBean.Username) > 0 {
			if injectErr := service.InjectICodeSSHKeys(poolItem.SandboxUUID, poolItem.Token,
				codeBean.Username, codeBean.Module); injectErr != nil {
				resource.LoggerService.Warning(ctx, "inject icode ssh keys fail",
					logit.String("sandboxID", poolItem.SandboxUUID),
					logit.String("error", injectErr.Error()))
			} else {
				resource.LoggerService.Notice(ctx, "inject icode ssh keys success",
					logit.String("sandboxID", poolItem.SandboxUUID))
			}
		}

		if request.EnvVars != nil {
			if envErr := service.InjectEnvVariables(poolItem.SandboxUUID, poolItem.Token, request.EnvVars); envErr != nil {
				resource.LoggerService.Warning(ctx, "inject env variables failed", logit.String("sandboxID", poolItem.SandboxUUID), logit.String("error", err.Error()))
			}
		}
		// 获取 ugateToken（所有类型沙箱都需要）
		if len(loginUser) > 0 {
			ugateSandboxToken, ugateToken = authcenter.GetAuthCenterApi().GetSandboxUgateTokenWithLoginUser(poolItem.SandboxUUID, loginUser, appID, string(environVarsJSON), realUser)

			if ugateSandboxTokenErr := service.InjectUgateToken(poolItem.SandboxUUID, poolItem.Token, loginUser, ugateSandboxToken); ugateSandboxTokenErr != nil {
				resource.LoggerService.Warning(ctx, "inject ugate token failed", logit.String("sandboxID", poolItem.SandboxUUID),
					logit.String("error", ugateSandboxTokenErr.Error()))
			}

			// 写入 IP -> Owner 缓存
			_ = common.SetSandboxOwnerCache(ctx, resource.RedisClient, resource.LoggerService, poolItem.PortIP,
				loginUser, poolItem.SandboxUUID, ugateToken, poolItem.AppID, app.Platform)

		}
		return response.NewGResponse(http.StatusCreated, res)
	}

	srDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxId := util.GenerateUniqueSandboxID(8, func(id string) bool {
		count, _ := srDao.Where(srDao.SandboxUUID.Eq(id)).Count()
		return count > 0
	})
	token := util.GenerateRandomString(32)
	usePVC := shouldUsePVC(request.MetaData)

	logUuid := uuid.New().String()
	envVars := make(map[string]string)
	if tpl.EnvirVars != "" {
		json.Unmarshal([]byte(tpl.EnvirVars), &envVars)
	}
	if request.EnvVars != nil {
		for k, v := range request.EnvVars {
			envVars[k] = v
		}
	}

	imageURL := tpl.Image
	if common.AppInRegistryWhiteList(appID) {
		metadataImage := getImage(request.MetaData)
		if metadataImage != "" {
			if util.ValidImageAddr(metadataImage) {
				imageURL = metadataImage
			} else {
				return response.NewGResponse(http.StatusBadRequest,
					response.ErrParams)
			}
		}
	}

	useSmallImageByTpl, smallImageByTpl := common.GetOpenClawImageSmallByTplID(appID, tpl.Name, loginUser, "")
	if useSmallImageByTpl && len(smallImageByTpl) > 0 {
		imageURL = smallImageByTpl
	}
	pullSecrets := make([]string, 0)
	pullSecrets = append(pullSecrets, constant.RegistryPullSecret)

	nodeSelector := getNodeSelector(request.MetaData)

	var oneApiKey string
	var openClawToken string
	// 获取 ugate token（所有类型沙箱都需要）
	if len(loginUser) > 0 {
		ugateSandboxToken, ugateToken = authcenter.GetAuthCenterApi().GetSandboxUgateTokenWithLoginUser(sandboxId, loginUser, appID, string(environVarsJSON), realUser)
	}

	if tpl.Type == "MoltBot" {
		comateAuthToken := request.EnvVars["COMATE_AUTH_TOKEN"]
		if comateAuthToken == "" {
			resource.LoggerService.Notice(ctx, "COMATE_AUTH_TOKEN env is empty", logit.Int32("appID", appID))
			if common.AppInTokenWhiteList(appID) && len(loginUser) > 0 {
				resource.LoggerService.Notice(ctx, "try get personal token from authCenter",
					logit.Int32("appID", appID), logit.String("loginUser", loginUser))
				comateAuthToken = authcenter.GetAuthCenterApi().GetPersonalTokenByUsername(loginUser)
			}
		}
		if comateAuthToken != "" {
			if common.IsPersonalTokenUser(loginUser) {
				// 使用新的个人 token 接口
				if personalToken, pErr := devopsauth.GetAuthApi().GetOpenclawGWToken(loginUser); pErr == nil {
					oneApiKey = personalToken
				}
			} else {
				modelToken, err := devopsauth.GetAuthApi().GetModelInfo(comateAuthToken)
				if err != nil || modelToken == nil {
					//return nil, fmt.Errorf("get model token failed: %s", err.Error())
					resource.LoggerService.Notice(ctx, "get model token failed", logit.String("error", err.Error()))
					oneApiKey = ""
				} else {
					envVars["COMATE_AUTH_TOKEN"] = comateAuthToken
					request.EnvVars["COMATE_AUTH_TOKEN"] = comateAuthToken
					oneApiKey = modelToken.ModelToken
				}
			}
		} else {
			resource.LoggerService.Warning(ctx, "modelToken get fail",
				logit.Int32("appID", appID), logit.String("loginUser", loginUser))
		}
		if openClawTokenValue, openClawTokenOk := request.EnvVars[constant.OpenClawEnvKey]; openClawTokenOk {
			openClawToken = openClawTokenValue
		} else {
			openClawToken = "123456"
		}
	}

	clusterName := service.NewAppService().GetAppCluster(appID, loginUser)
	cluster := resource.ClusterManager.GetClusterByName(clusterName)
	if cluster == nil {
		resource.LoggerService.Error(ctx, "create pod fail,cluster is not exist",
			logit.String("cluster", clusterName))
		return response.NewGResponse(http.StatusBadRequest,
			response.ClusterNotFoundErr)
	}

	pvcs, err := appPvcDao.Where(appPvcDao.AppID.Eq(appID)).Find()
	if err != nil {
		resource.LoggerService.Error(ctx, "query app pvc binding failed",
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}

	// 解析 metadata 中的 agent-sandbox/pvcs，校验并追加到 BindPvcs
	var metadataPvcs []*k8s.PvcBinding
	if request.MetaData != nil {
		metadataPvcs, err = parseAndValidateMetadataPvcs(ctx, request.MetaData, appID)
		if err != nil {
			return response.NewGResponse(http.StatusBadRequest,
				response.New(err.Error(), response.ParamsErrCode))
		}
	}
	// 合并两个渠道的 pvcs
	allPvcs := make([]*k8s.PvcBinding, 0, len(pvcs)+len(metadataPvcs))
	for _, pvc := range pvcs {
		allPvcs = append(allPvcs, &k8s.PvcBinding{
			PvcName:   pvc.PvcName,
			MountPath: pvc.MountPath,
		})
	}
	allPvcs = append(allPvcs, metadataPvcs...)

	builder := k8s.NewCreatePodFieldsBuilder(sandboxId).
		WithNodeSelector(nodeSelector).
		WithImage(imageURL, tpl.Type).
		WithResources(tpl.CPU, tpl.Memory, tpl.Disk).
		WithToken(token).
		WithEnvVars(envVars).
		WithBindPvcs(allPvcs).
		WithLogUuid(logUuid).
		WithRegistryPullSecrets(pullSecrets).
		WithReadLessPort(util.GetReadProbPort(tpl.Type)).
		WithOpenClawToken(openClawToken).
		WithOneApiKey(oneApiKey).
		WithUgateToken(ugateSandboxToken).
		WithUsername(loginUser).
		WithClusterName(clusterName).
		WithAppID(appID).
		WithCodeBeans(codeBeans).
		WithCodeBean(codeBean).
		WithPlatform(app.Platform)

	var dockerFields *k8s.DockerFields = nil
	if request.MetaData != nil && len(request.MetaData) > 0 {
		dockerStr, ok := request.MetaData[constant.MetadataDockerKey]
		if ok {
			dockerFields = &k8s.DockerFields{}
			err = json.Unmarshal([]byte(dockerStr), dockerFields)
			if err != nil {
				return response.NewGResponse(http.StatusInternalServerError,
					response.UnmarshalJsonErr)
			}
			builder.WithDockerFields(dockerFields)
		}
	}

	createPodFields := builder.Build()
	resultChan := make(chan k8s.PodEvent, 1)

	timeout := request.Timeout
	if !common.AppInUnlimitedWhiteList(appID) {
		// 最大超时时间7天;
		if timeout <= 0 || timeout > 3600*168 {
			return response.NewGResponse(http.StatusBadRequest,
				response.ErrParams)
		}
	}
	var img model.Image
	imageDao := dao.Use(resource.OrmMain).Image
	err = imageDao.Where(imageDao.ID.Eq(tpl.ImageID)).Scan(&img)
	if err != nil {
		resource.LoggerService.Error(ctx, "query image failed",
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}

	sandboxRuntimeModel := &model.SandboxRuntime{
		AppID:       appID,
		TemplateID:  tpl.ID,
		Name:        getSandboxName(request.MetaData),
		SandboxUUID: sandboxId,
		Metadata:    string(metadataBytes),
		PortIP:      "",
		NodeIP:      "",
		CommandPort: 0,
		CodePort:    0,
		Token:       token,
		Status:      constant.StatusPending,
		CreateTime:  time.Now(),
		StartTime:   time.Now(),
		Timeout:     timeout,
		TimeoutTime: time.Time{},
		Cluster:     clusterName,
		LogUUID:     logUuid,
		CreateUser:  loginUser,
		ImageID:     img.ID,
		CPU:         tpl.CPU,
		Memory:      tpl.Memory,
		Disk:        tpl.Disk,
		CodeBean:    getCodeBean(request.MetaData),
		PostScript:  getPostScript(request.MetaData, tpl),
		Module:      getModule(request.MetaData),
		EnvirVars:   string(environVarsJSON),
		UsePvc:      usePVC,
	}
	// timeout_time 永远等于 start_time + timeout(秒)
	sandboxRuntimeModel.TimeoutTime = service.GetTimeoutTime(timeout, sandboxRuntimeModel.StartTime)
	// 创建pod之前保存一下sandbox信息
	err = sandboxRuntimeDao.Save(sandboxRuntimeModel)
	if err != nil {
		resource.LoggerService.Error(ctx, "create pod fail,save sandbox_runtime error",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}

	if usePVC {
		// 从 metadata 解析 PVC 容量大小（可选，1-100GB）。
		pvcSize, sizeErr := getPVCSize(request.MetaData)
		if sizeErr != nil {
			resource.LoggerService.Error(ctx, "parse pvc size failed",
				logit.String("sandboxID", sandboxId),
				logit.String("error", sizeErr.Error()))
			_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(map[string]interface{}{
				"status":        constant.StatusFailed,
				"end_time":      sandboxRuntimeModel.StartTime,
				"failed_reason": fmt.Sprintf("parse pvc size failed: %s", sizeErr.Error()),
			})
			return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
		}
		pvcSizeGB := 20 // 默认 20GB
		if pvcSize != nil {
			pvcSizeGB = *pvcSize
		}
		// 从 metadata 解析存储类（可选，默认 local-path）。
		storageClass, scErr := service.ValidateSandboxStorageClass(getStorageClass(request.MetaData), loginUser)
		if scErr != nil {
			resource.LoggerService.Error(ctx, "invalid storage class",
				logit.String("sandboxID", sandboxId),
				logit.String("error", scErr.Error()))
			_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(map[string]interface{}{
				"status":        constant.StatusFailed,
				"end_time":      sandboxRuntimeModel.StartTime,
				"failed_reason": fmt.Sprintf("invalid storage class: %s", scErr.Error()),
			})
			return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
		}
		binding, bindErr := service.EnsureSandboxPVCBinding(
			ctx,
			appID,
			sandboxId,
			getPVCMountPath(ctx, request.MetaData),
			clusterName,
			cluster.GetNamespace(),
			pvcSizeGB,
			storageClass,
			cluster,
			loginUser,
		)
		if bindErr != nil {
			resource.LoggerService.Error(ctx, "ensure sandbox pvc binding failed",
				logit.String("sandboxID", sandboxId),
				logit.String("error", bindErr.Error()))
			// 绑定失败时将 runtime 标记为 failed，避免卡在 pending。
			_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(map[string]interface{}{
				"status":        constant.StatusFailed,
				"end_time":      sandboxRuntimeModel.StartTime,
				"failed_reason": fmt.Sprintf("ensure pvc binding failed: %s", bindErr.Error()),
			})
			return response.NewGResponse(http.StatusInternalServerError, response.K8sOperateErr)
		}
		createPodFields.WorkspacePVCName = binding.PVCName
	}
	err = cluster.CreatePod(createPodFields, resultChan)
	if err != nil {
		service.RecordCreateFailed(appIDLabel, templateIDLabel)
		_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(map[string]interface{}{
			"status":        constant.StatusFailed,
			"end_time":      sandboxRuntimeModel.StartTime,
			"failed_reason": fmt.Sprintf("create pod failed: %s", err.Error()),
		})
		if usePVC {
			// CreatePod 失败时，runtime 已进入终态（failed），再释放 PVC，避免误释放 running/pending。
			_ = service.ReleaseSandboxPVCBinding(ctx, sandboxId, sandboxRuntimeModel.StartTime, service.DefaultSandboxPVCGracePeriod())
		}
		resource.LoggerService.Error(ctx, "create pod fail",
			logit.String("sandboxID", sandboxId),
			logit.String("cluster", clusterName),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError,
			response.CreateContainerError)
	}
	resource.LoggerService.Notice(ctx, "pod creation initiated",
		logit.String("sandboxID", sandboxId),
		logit.String("cluster", clusterName))

	if tpl.Type == "MoltBot" && len(sandboxRuntimeModel.CreateUser) > 0 && appID == constant.OpenClawAppID {
		sandboxTypeRedisKey := "sandbox_type:" + sandboxId
		_ = resource.RedisClient.Set(context.Background(), sandboxTypeRedisKey, "openclaw").Err()
	}

	// 使用 select 等待数据到达或超时
	select {
	case data := <-resultChan:
		res := CreateSandboxResponse{
			TemplateID:      tpl.Name,
			SandboxID:       sandboxId,
			ClientID:        constant.E2bClientID,
			Domain:          constant.SandboxDomain,
			EnvdVersion:     constant.E2bEnvdVersion,
			EnvdAccessToken: token,
		}
		sandboxRuntimeModel.PodName = data.PodName
		sandboxRuntimeModel.PortIP = data.PortIp
		sandboxRuntimeModel.NodeIP = data.NodeIp
		sandboxRuntimeModel.CommandPort = data.CommandPort
		sandboxRuntimeModel.CodePort = data.CodePort
		sandboxRuntimeModel.StartTime = time.Now()
		// timeout_time 必须永远等于 start_time + timeout(秒)
		sandboxRuntimeModel.TimeoutTime = service.GetTimeoutTime(sandboxRuntimeModel.Timeout, sandboxRuntimeModel.StartTime)
		sandboxRuntimeModel.EndTime = time.Now()
		sandboxRuntimeModel.Status = k8sStatusMapping[data.PodPhase]
		sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(sandboxRuntimeModel)
		if usePVC && sandboxRuntimeModel.Status == constant.StatusRunning {
			_ = service.ActivateSandboxPVCBinding(ctx, sandboxId)
		}

		service.RecordCreateSuccess(appIDLabel, templateIDLabel)
		duration := time.Since(startTime).Seconds()
		// 耗时指标不区分成功失败
		service.CreateSandboxDuration.WithLabelValues(appIDLabel, templateIDLabel).Observe(duration)

		resource.LoggerService.Notice(ctx, "create sandbox success",
			logit.String("sandboxID", sandboxId),
			logit.String("cluster", clusterName),
			logit.String("podPhase", string(data.PodPhase)))
		resource.RedisClient.Set(context.Background(), constant.BackendIPPrefix+sandboxId, data.PortIp)
		if cluster.GetNetworkType() == constant.NetworkTypeVpc {
			_ = resource.RedisClient.Set(context.Background(), constant.NginxEndpointRedisKey+sandboxId, cluster.GetNginxEndpoint()).Err()
		}
		redisKey := "sandbox:last_access:" + sandboxId
		_ = resource.RedisClient.Set(context.Background(), redisKey, time.Now().Unix(), time.Hour*24).Err()

		// 写入 IP -> Owner 缓存
		_ = common.SetSandboxOwnerCache(ctx, resource.RedisClient, resource.LoggerService, data.PortIp,
			sandboxRuntimeModel.CreateUser, sandboxId, ugateToken, sandboxRuntimeModel.AppID, app.Platform)

		postScript := getPostScript(request.MetaData, tpl)
		if len(postScript) > 0 {
			runtimeapi.GetRuntimeApi().StartCommand(sandboxId, token, postScript)
		}
		if err := service.NewSandboxRuntimeService().CheckAndAlertResourceUsage(ctx, appID); err != nil {
			resource.LoggerService.Warning(ctx, "check and alert resource usage failed after create sandbox",
				logit.Int32("appID", appID),
				logit.String("sandboxID", sandboxId),
				logit.String("error", err.Error()))
		}

		return response.NewGResponse(http.StatusCreated, res)
	case <-time.After(10 * time.Minute):
		service.RecordCreateFailed(appIDLabel, templateIDLabel)
		resource.LoggerService.Error(ctx, "create sandbox timeout",
			logit.String("sandboxID", sandboxId),
			logit.String("cluster", clusterName))
		err = service.DeleteSandbox(ctx, sandboxId, appID)
		if err != nil {
			resource.LoggerService.Error(ctx, "delete pod fail",
				logit.String("sandboxID", sandboxId),
				logit.String("error", err.Error()))
		}
		return response.NewGResponse(http.StatusInternalServerError,
			response.CreateContainerTimeoutError)
	case <-ctx.Done():
		// 只有当时间超过 2 分半（150秒）且客户端取消时，才统计失败
		elapsed := time.Since(startTime)
		if elapsed > 150*time.Second {
			service.RecordCreateFailed(appIDLabel, templateIDLabel)
		}
		resource.LoggerService.Error(ctx, "create sandbox cancel,client side closed",
			logit.String("sandboxID", sandboxId),
			logit.String("cluster", clusterName),
			logit.Duration("elapsed", elapsed))
		err = service.DeleteSandbox(ctx, sandboxId, appID)
		if err != nil {
			resource.LoggerService.Error(ctx, "delete pod fail",
				logit.String("sandboxID", sandboxId),
				logit.String("error", err.Error()))
		}
		return response.NewGResponse(http.StatusInternalServerError,
			response.CreateContainerTimeoutError)
	}
}

func handleGetSandbox(ctx context.Context, req ghttp.Request) ghttp.Response {

	sandboxId := req.Param(":sandboxID")
	resource.LoggerService.Notice(ctx, "get exist sandbox", logit.String("sandboxID", sandboxId))

	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxRuntimeModel, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxId)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "get sandbox fail, query failed",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError,
			response.QueryDbErr)
	}
	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	if appID != sandboxRuntimeModel.AppID {
		resource.LoggerService.Warning(ctx, "get sandbox fail, appID not match",
			logit.String("sandboxID", sandboxId),
			logit.Int32("requestAppID", appID),
			logit.Int32("sandboxAppID", sandboxRuntimeModel.AppID))
		return response.NewGResponse(http.StatusForbidden,
			response.AppNotMatchErr)
	}
	templateDao := dao.Use(resource.OrmMain).Template

	tpl, err := templateDao.Where(templateDao.AppID.In(appID, 0), templateDao.ID.Eq(sandboxRuntimeModel.TemplateID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "get sandbox fail, template query failed",
			logit.String("sandboxID", sandboxId),
			logit.Int32("appID", appID),
			logit.Int32("templateID", sandboxRuntimeModel.TemplateID),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusNotFound,
			response.TemplateNotFoundErr)
	}
	metadata := make(map[string]string)
	if len(sandboxRuntimeModel.Metadata) > 0 {
		err = json.Unmarshal([]byte(sandboxRuntimeModel.Metadata), &metadata)
		if err != nil {
			resource.LoggerService.Error(ctx, "get sandbox fail,metadata unmarshal error")
			return response.NewGResponse(http.StatusInternalServerError,
				response.UnmarshalJsonErr)
		}
	}
	logUrl := ""
	if len(sandboxRuntimeModel.PodName) > 0 {
		logUrl = magellanauth.GetAuthApi().GetLogURL(sandboxRuntimeModel.LogUUID, sandboxRuntimeModel.PodName)
	} else {
		logUrl = magellanauth.GetAuthApi().GetLogURL(sandboxRuntimeModel.LogUUID, sandboxRuntimeModel.SandboxUUID)
	} // 放到metadata中,方便e2b sdk获取;
	metadata["logURL"] = logUrl

	expireTime := sandboxRuntimeModel.StartTime.Add(time.Second * time.Duration(sandboxRuntimeModel.Timeout))

	res := GetSandboxResponse{
		TemplateID:      tpl.Name,
		SandboxID:       sandboxRuntimeModel.SandboxUUID,
		ClientID:        constant.E2bClientID,
		Domain:          constant.SandboxDomain,
		EnvdVersion:     constant.E2bEnvdVersion,
		EnvdAccessToken: sandboxRuntimeModel.Token,
		CpuCount:        sandboxRuntimeModel.CPU / 1000,
		MemoryMB:        sandboxRuntimeModel.Memory,
		DiskSizeMB:      sandboxRuntimeModel.Disk * 1024,
		StartedAt:       sandboxRuntimeModel.CreateTime.Format(time.RFC3339),
		EndAt:           expireTime.Format(time.RFC3339),
		State:           mappingE2bStatus(sandboxRuntimeModel.Status),
		OriginStatus:    sandboxRuntimeModel.Status,
		Metadata:        metadata,
		LogURL:          logUrl,
	}

	resource.LoggerService.Notice(ctx, "get sandbox success",
		logit.String("sandboxID", sandboxId),
		logit.String("templateID", tpl.Name),
		logit.Int32("appID", appID))
	return response.NewGResponse(http.StatusOK, res)
}

func handleRestoreSandbox(ctx context.Context, req ghttp.Request) ghttp.Response {
	sandboxId := req.Param(":sandboxID")
	request := &RestoreSandboxRequest{}
	_ = util.Bind(req, request)

	// 记录恢复操作开始时间，用于统计耗时
	startTime := time.Now()

	lockKey := constant.SandboxRestoreLockPrefix + sandboxId
	lock, err := resource.RedisClient.SetNX(ctx, lockKey, "1", 10*time.Minute).Result()
	if err != nil {
		resource.LoggerService.Error(ctx, "set restore lock failed",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError, response.ErrInternalServer)
	}
	if !lock {
		return response.NewGResponse(http.StatusConflict, response.ErrParams)
	}
	defer resource.RedisClient.Del(context.Background(), lockKey)

	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxRuntimeModel, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxId)).First()
	if err != nil {
		return response.NewGResponse(http.StatusBadRequest, response.QueryDbErr)
	}
	if sandboxRuntimeModel.AppID != appID {
		return response.NewGResponse(http.StatusForbidden, response.AppNotMatchErr)
	}

	// 检查 app 是否存在 account（尽早检查，快速失败）
	if err := service.CheckAppAccount(ctx, appID); err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.New("该应用未配置资源账号，无法恢复实例", response.ParamsErrCode))
	}
	appDao := dao.Use(resource.OrmMain).App
	app, err := appDao.Where(appDao.ID.Eq(appID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "get app info failed", logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.QueryDbErr)
	}

	// 设置 Prometheus 指标标签
	appIDLabel := fmt.Sprintf("%d", sandboxRuntimeModel.AppID)
	templateIDLabel := fmt.Sprintf("%d", sandboxRuntimeModel.TemplateID)

	if sandboxRuntimeModel.Status == constant.StatusRunning || sandboxRuntimeModel.Status == constant.StatusPending {
		service.RecordRestoreValidationFailed(appIDLabel, templateIDLabel, constant.TriggerTypeManual, constant.RestoreValidationFailureReasonStatusConflict)
		return response.NewGResponse(http.StatusConflict, response.ErrParams)
	}
	if !sandboxRuntimeModel.UsePvc {
		service.RecordRestoreValidationFailed(appIDLabel, templateIDLabel, constant.TriggerTypeManual, constant.RestoreValidationFailureReasonNoPVC)
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}

	// 跨存储类迁移中禁止恢复，避免新旧 PVC 数据不一致
	if service.IsStorageMigrationInProgress(ctx, sandboxId) {
		service.RecordRestoreValidationFailed(appIDLabel, templateIDLabel, constant.TriggerTypeManual, constant.RestoreValidationFailureReasonStorageMigrationInProgress)
		return response.NewGResponse(http.StatusConflict, fmt.Errorf("存储迁移进行中，请稍后再恢复"))
	}

	originalStatus := sandboxRuntimeModel.Status

	templateDao := dao.Use(resource.OrmMain).Template
	tpl, err := templateDao.Where(templateDao.ID.Eq(sandboxRuntimeModel.TemplateID)).First()
	if err != nil {
		return response.NewGResponse(http.StatusBadRequest, response.TemplateNotFoundErr)
	}

	imageURL := tpl.Image
	imageDao := dao.Use(resource.OrmMain).Image
	var img model.Image
	if imgErr := imageDao.Where(imageDao.ID.Eq(tpl.ImageID)).Scan(&img); imgErr == nil && img.ImageURL != "" {
		imageURL = img.ImageURL
	}

	useSmallImageByTpl, smallImageByTpl := common.GetOpenClawImageSmallByTplID(appID, tpl.Name, sandboxRuntimeModel.CreateUser, sandboxRuntimeModel.SandboxUUID)
	if useSmallImageByTpl && len(smallImageByTpl) > 0 {
		imageURL = smallImageByTpl
	}

	tplEnvVars := make(map[string]string)
	sbxEnvVars := make(map[string]string)
	envVars := make(map[string]string)
	if tpl.EnvirVars != "" {
		_ = json.Unmarshal([]byte(tpl.EnvirVars), &tplEnvVars)
	}
	if sandboxRuntimeModel.EnvirVars != "" {
		_ = json.Unmarshal([]byte(sandboxRuntimeModel.EnvirVars), &sbxEnvVars)
	}
	// 合并模版的环境变量
	for k, v := range tplEnvVars {
		envVars[k] = v
	}
	// 合并实例的环境变量
	for k, v := range sbxEnvVars {
		envVars[k] = v
	}
	// 合并请求中的环境变量，请求的优先级更高
	for k, v := range request.EnvVars {
		envVars[k] = v
	}
	startMessage := common.GetPreStartNotificationMessage(ctx, resource.RedisClient, sandboxRuntimeModel.CreateUser, sandboxRuntimeModel.StartTime)
	service.SendSandboxRestartMessage(ctx, envVars, sandboxRuntimeModel.CreateUser, sandboxRuntimeModel.SandboxUUID, startMessage, sandboxRuntimeModel.AppID)
	successMessage := common.GetPostStartNotificationMessage(ctx, resource.RedisClient, sandboxRuntimeModel.CreateUser, sandboxRuntimeModel.StartTime)

	oneApiKey := ""
	ugateToken := ""
	ugateSandboxToken := ""

	// 获取 ugate token（所有类型沙箱都需要）
	if len(sandboxRuntimeModel.CreateUser) > 0 {
		// sandboxRuntimeModel.EnvirVars 是 JSON 字符串，直接使用
		ugateSandboxToken, ugateToken = authcenter.GetAuthCenterApi().GetSandboxUgateTokenWithLoginUser(sandboxRuntimeModel.SandboxUUID, sandboxRuntimeModel.CreateUser, appID, sandboxRuntimeModel.EnvirVars, sandboxRuntimeModel.CreateUser)
	}

	// MoltBot 的启动脚本需要 oneApiKey/openClawToken；从已持久化的 COMATE_AUTH_TOKEN 重算 oneApiKey。
	if tpl.Type == "MoltBot" {
		if apiToken, ok := envVars[constant.DevopsJwtEnvKey]; ok && apiToken != "" {
			if common.IsPersonalTokenUser(sandboxRuntimeModel.CreateUser) {
				// 使用新的个人 token 接口
				if personalToken, pErr := devopsauth.GetAuthApi().GetOpenclawGWToken(sandboxRuntimeModel.CreateUser); pErr == nil {
					oneApiKey = personalToken
				}
			} else {
				if modelToken, mErr := devopsauth.GetAuthApi().GetModelInfo(apiToken); mErr == nil && modelToken != nil {
					oneApiKey = modelToken.ModelToken
				}
			}
		} else {
			if common.IsPersonalTokenUser(sandboxRuntimeModel.CreateUser) {
				// 使用新的个人 token 接口
				if personalToken, pErr := devopsauth.GetAuthApi().GetOpenclawGWToken(sandboxRuntimeModel.CreateUser); pErr == nil {
					oneApiKey = personalToken
				}
			} else {
				// 使用原有的 model token 接口
				if modelToken, mErr := devopsauth.GetAuthApi().GetModelInfo(apiToken); mErr == nil && modelToken != nil {
					oneApiKey = modelToken.ModelToken
				} else {
					comateAuthToken := authcenter.GetAuthCenterApi().GetPersonalTokenByUsername(sandboxRuntimeModel.CreateUser)
					if modelToken, mErr := devopsauth.GetAuthApi().GetModelInfo(comateAuthToken); mErr == nil && modelToken != nil {
						oneApiKey = modelToken.ModelToken
						envVars[constant.DevopsJwtEnvKey] = comateAuthToken
					}
				}
			}
		}

	}

	metadata := make(map[string]string)
	if sandboxRuntimeModel.Metadata != "" {
		_ = json.Unmarshal([]byte(sandboxRuntimeModel.Metadata), &metadata)
	}

	updatedTimeout := sandboxRuntimeModel.Timeout
	if request.Timeout > 0 {
		updatedTimeout = request.Timeout
	}
	resource.LoggerService.Notice(ctx, "restore: setting status to pending",
		logit.String("sandboxID", sandboxId),
		logit.String("originalStatus", originalStatus))
	_, err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(map[string]interface{}{
		"status":  constant.StatusPending,
		"token":   sandboxRuntimeModel.Token,
		"timeout": updatedTimeout,
		// timeout_time 语义：永远等于 start_time + timeout(秒)
		"timeout_time":  service.GetTimeoutTime(updatedTimeout, sandboxRuntimeModel.StartTime),
		"failed_reason": "",
	})
	if err != nil {
		resource.LoggerService.Error(ctx, "restore: failed to set status to pending",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest, response.QueryDbErr)
	}

	cluster := resource.ClusterManager.GetClusterByName(sandboxRuntimeModel.Cluster)
	if cluster == nil {
		service.RecordRestoreValidationFailed(appIDLabel, templateIDLabel, constant.TriggerTypeManual, constant.RestoreValidationFailureReasonClusterNotFound)
		return response.NewGResponse(http.StatusBadRequest, response.ClusterNotFoundErr)
	}

	// binding 记录缺失时做自愈：复用/重建同名 PVC 并补齐 binding 记录。
	binding, err := service.GetSandboxPVCBinding(ctx, sandboxId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			resource.LoggerService.Warning(ctx, "sandbox pvc binding record not found, trying to recreate",
				logit.String("sandboxID", sandboxId))
			// 恢复时从 metadata 解析 PVC 大小（如果提供），否则使用默认值 20GB。
			pvcSize, sizeErr := getPVCSize(metadata)
			if sizeErr != nil {
				resource.LoggerService.Error(ctx, "parse pvc size failed during restore",
					logit.String("sandboxID", sandboxId),
					logit.String("error", sizeErr.Error()))
				return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
			}
			pvcSizeGB := 20 // 默认 20GB
			if pvcSize != nil {
				pvcSizeGB = *pvcSize
			}
			// 自愈时确定存储类：
			// - PVC 未被删除（binding.deleted=0）：复用原存储类，确保 BOS 等存储类可正确重建。
			// - PVC 已被删除（binding.deleted=1）或无历史记录：传 nil 让 ValidateSandboxStorageClass 使用默认值（CFS），与新建沙箱行为一致。
			var restoreSC *string
			if latestBinding, _ := service.GetLatestSandboxPVCBinding(ctx, sandboxId); latestBinding != nil && !latestBinding.Deleted {
				restoreSC = &latestBinding.StorageClass
			}
			storageClass, scErr := service.ValidateSandboxStorageClass(restoreSC, sandboxRuntimeModel.CreateUser)
			if scErr != nil {
				resource.LoggerService.Error(ctx, "invalid storage class during restore",
					logit.String("sandboxID", sandboxId),
					logit.String("error", scErr.Error()))
				return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
			}
			binding, err = service.EnsureSandboxPVCBinding(
				ctx,
				appID,
				sandboxId,
				service.DefaultSandboxPVCMountPath(),
				sandboxRuntimeModel.Cluster,
				cluster.GetNamespace(),
				pvcSizeGB,
				storageClass,
				cluster,
				sandboxRuntimeModel.CreateUser,
			)
		}
		if err != nil {
			resource.LoggerService.Error(ctx, "get/ensure sandbox pvc binding failed",
				logit.String("sandboxID", sandboxId),
				logit.String("error", err.Error()))
			service.RecordRestoreValidationFailed(appIDLabel, templateIDLabel, constant.TriggerTypeManual, constant.RestoreValidationFailureReasonPVCBindingFailed)
			return response.NewGResponse(http.StatusBadRequest, response.QueryDbErr)
		}
	}

	pullSecrets := make([]string, 0)
	pullSecrets = append(pullSecrets, constant.RegistryPullSecret)

	appPvcDao := dao.Use(resource.OrmMain).AppPvcBinding
	pvcs, err := appPvcDao.Where(appPvcDao.AppID.Eq(appID)).Find()
	if err != nil {
		resource.LoggerService.Error(ctx, "query app pvc binding failed",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		service.RecordRestoreValidationFailed(appIDLabel, templateIDLabel, constant.TriggerTypeManual, constant.RestoreValidationFailureReasonPVCConfigQueryFailed)
		endAt := time.Now()
		_, _ = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(map[string]interface{}{
			"status":        originalStatus,
			"failed_reason": fmt.Sprintf("restore query app pvc binding failed: %s", err.Error()),
			"end_time":      endAt,
		})
		_ = service.ReleaseSandboxPVCBinding(ctx, sandboxId, endAt, service.DefaultSandboxPVCGracePeriod())
		return response.NewGResponse(http.StatusBadRequest, response.QueryDbErr)
	}

	// 从 metadata 解析 agent-sandbox/pvcs 并追加到 BindPvcs
	var metadataPvcs []*k8s.PvcBinding
	if metadata != nil {
		metadataPvcs, err = parseAndValidateMetadataPvcs(ctx, metadata, appID)
		if err != nil {
			return response.NewGResponse(http.StatusBadRequest,
				response.New(err.Error(), response.ParamsErrCode))
		}
	}
	allPvcs := make([]*k8s.PvcBinding, 0, len(pvcs)+len(metadataPvcs))
	for _, pvc := range pvcs {
		allPvcs = append(allPvcs, &k8s.PvcBinding{
			PvcName:   pvc.PvcName,
			MountPath: pvc.MountPath,
		})
	}
	allPvcs = append(allPvcs, metadataPvcs...)

	createPodFields := k8s.CreatePodFields{
		PodName: sandboxId,
		ImageFields: &k8s.ImageFields{
			ImageURL: imageURL,
			Type:     tpl.Type,
		},
		Cpu:                 sandboxRuntimeModel.CPU,
		Mem:                 sandboxRuntimeModel.Memory,
		Disk:                sandboxRuntimeModel.Disk,
		Token:               sandboxRuntimeModel.Token,
		EnvVars:             envVars,
		OneApiKey:           oneApiKey,
		OpenClawToken:       "123456",
		UgateToken:          ugateSandboxToken,
		BindPvcs:            allPvcs,
		NodeSelector:        getNodeSelector(metadata),
		LogUuid:             sandboxRuntimeModel.LogUUID,
		RegistryPullSecrets: pullSecrets,
		WorkspacePVCName:    binding.PVCName,
		Platform:            app.Platform,
		ReadLessPort:        util.GetReadProbPort(tpl.Type),
		Username:            sandboxRuntimeModel.CreateUser,
		ClusterName:         sandboxRuntimeModel.Cluster,
		AppID:               appID,
	}
	codeBeanStr := getCodeBean(metadata)
	if codeBeanStr != "" {
		codeBean := &k8s.CodeBean{}
		if err = json.Unmarshal([]byte(codeBeanStr), codeBean); err == nil {
			createPodFields.CodeBean = codeBean
		}
	}
	if dockerStr, ok := metadata[constant.MetadataDockerKey]; ok {
		dockerFields := &k8s.DockerFields{}
		if err = json.Unmarshal([]byte(dockerStr), dockerFields); err == nil {
			createPodFields.DockerFields = dockerFields
		}
	}
	if codeBeansStr, ok := metadata["codeBeans"]; ok {
		var codeBeans []*k8s.CodeBean
		if err = json.Unmarshal([]byte(codeBeansStr), &codeBeans); err == nil {
			createPodFields.CodeBeans = codeBeans
		}
	}

	resultChan := make(chan k8s.PodEvent, 1)
	// 先尝试清理同名旧 Pod，避免 Terminating/残留导致 AlreadyExists。
	resource.LoggerService.Notice(ctx, "restore: deleting old pod",
		logit.String("sandboxID", sandboxId),
		logit.String("cluster", sandboxRuntimeModel.Cluster))
	_ = cluster.DeletePod(sandboxId)
	resource.LoggerService.Notice(ctx, "restore: creating pod",
		logit.String("sandboxID", sandboxId),
		logit.String("cluster", sandboxRuntimeModel.Cluster),
		logit.String("image", imageURL))
	if err = cluster.CreatePod(&createPodFields, resultChan); err != nil {
		service.RecordRestoreFailed(appIDLabel, templateIDLabel, constant.TriggerTypeManual)
		endAt := time.Now()
		resource.LoggerService.Error(ctx, "restore: create pod failed, rolling back status",
			logit.String("sandboxID", sandboxId),
			logit.String("cluster", sandboxRuntimeModel.Cluster),
			logit.String("error", err.Error()))
		_, rollbackErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(map[string]interface{}{
			"status":        originalStatus,
			"failed_reason": fmt.Sprintf("restore create pod failed: %s", err.Error()),
			"end_time":      endAt,
		})
		if rollbackErr != nil {
			resource.LoggerService.Error(ctx, "restore: failed to rollback status after create pod error",
				logit.String("sandboxID", sandboxId),
				logit.String("error", rollbackErr.Error()))
		} else {
			resource.LoggerService.Notice(ctx, "restore: status rolled back after create pod error",
				logit.String("sandboxID", sandboxId),
				logit.String("rolledBackTo", originalStatus))
		}
		_ = service.ReleaseSandboxPVCBinding(ctx, sandboxId, endAt, service.DefaultSandboxPVCGracePeriod())
		return response.NewGResponse(http.StatusInternalServerError, response.CreateContainerError)
	}
	select {
	case data := <-resultChan:
		status := k8sStatusMapping[data.PodPhase]
		resource.LoggerService.Notice(ctx, "restore: received pod event",
			logit.String("sandboxID", sandboxId),
			logit.String("podPhase", string(data.PodPhase)),
			logit.String("mappedStatus", status),
			logit.String("portIp", data.PortIp),
			logit.String("errMsg", data.ErrMsg))
		startAt := time.Now()
		endAt := time.Now()
		updateData := map[string]interface{}{
			"pod_name":      data.PodName,
			"port_ip":       data.PortIp,
			"node_ip":       data.NodeIp,
			"command_port":  data.CommandPort,
			"code_port":     data.CodePort,
			"start_time":    startAt,
			"timeout_time":  service.GetTimeoutTime(updatedTimeout, startAt),
			"end_time":      endAt,
			"status":        status,
			"timeout":       updatedTimeout,
			"use_pvc":       true,
			"failed_reason": "",
		}
		if status == constant.StatusFailed {
			updateData["status"] = originalStatus
			updateData["failed_reason"] = data.ErrMsg
			resource.LoggerService.Warning(ctx, "restore: pod status is failed, will rollback to original status",
				logit.String("sandboxID", sandboxId),
				logit.String("originalStatus", originalStatus),
				logit.String("errMsg", data.ErrMsg))
			service.RecordRestoreFailed(appIDLabel, templateIDLabel, constant.TriggerTypeManual)
		} else if status == constant.StatusRunning {
			resource.LoggerService.Notice(ctx, "restore: pod is running, updating status",
				logit.String("sandboxID", sandboxId),
				logit.String("portIp", data.PortIp))
			service.RecordRestoreSuccess(appIDLabel, templateIDLabel, constant.TriggerTypeManual)
			_ = service.ActivateSandboxPVCBinding(ctx, sandboxId)
			resource.RedisClient.Set(context.Background(), constant.BackendIPPrefix+sandboxId, data.PortIp)
			if cluster.GetNetworkType() == constant.NetworkTypeVpc {
				_ = resource.RedisClient.Set(context.Background(), constant.NginxEndpointRedisKey+sandboxId, cluster.GetNginxEndpoint()).Err()
			}
			redisKey := "sandbox:last_access:" + sandboxRuntimeModel.SandboxUUID
			_ = resource.RedisClient.Set(context.Background(), redisKey, time.Now().Unix(), time.Hour*24).Err()
			// 写入 IP -> Owner 缓存（恢复时IP会变化）
			_ = common.SetSandboxOwnerCache(ctx, resource.RedisClient, resource.LoggerService, data.PortIp,
				sandboxRuntimeModel.CreateUser, sandboxId, ugateToken, sandboxRuntimeModel.AppID, app.Platform)
			go func() {
				// 延迟30秒发送，确保容器内服务完全就绪后再通知用户
				time.Sleep(5 * time.Second)
				// 获取通知话术（根据用户、sandbox启动时间和redis配置判断）
				service.SendSandboxRestartMessage(context.Background(), envVars, sandboxRuntimeModel.CreateUser, sandboxRuntimeModel.SandboxUUID, successMessage, sandboxRuntimeModel.AppID)
			}()
		}
		duration := time.Since(startTime).Seconds()
		// 耗时指标不区分成功失败
		service.RestoreSandboxDuration.WithLabelValues(appIDLabel, templateIDLabel, constant.TriggerTypeManual).Observe(duration)

		resource.LoggerService.Notice(ctx, "sandbox runtime saved, restore pod finished",
			logit.String("sandboxID", sandboxId),
			logit.String("status", status))
		result, updateErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(updateData)
		if updateErr != nil {
			resource.LoggerService.Error(ctx, "restore: failed to update db after pod event",
				logit.String("sandboxID", sandboxId),
				logit.String("status", status),
				logit.String("error", updateErr.Error()))
		} else {
			resource.LoggerService.Notice(ctx, "restore: db updated after pod event",
				logit.String("sandboxID", sandboxId),
				logit.String("status", status),
				logit.Int64("rowsAffected", result.RowsAffected))
		}
		if status == constant.StatusFailed {
			_ = service.ReleaseSandboxPVCBinding(ctx, sandboxId, endAt, service.DefaultSandboxPVCGracePeriod())
		}
		postScript := sandboxRuntimeModel.PostScript
		if postScript != "" && status == constant.StatusRunning {
			runtimeapi.GetRuntimeApi().StartCommand(sandboxId, sandboxRuntimeModel.Token, postScript)
		}
		if status == constant.StatusRunning {
			if err := service.NewSandboxRuntimeService().CheckAndAlertResourceUsage(ctx, appID); err != nil {
				resource.LoggerService.Warning(ctx, "check and alert resource usage failed after restore sandbox",
					logit.Int32("appID", appID),
					logit.String("sandboxID", sandboxId),
					logit.String("error", err.Error()))
			}
		}
		res := CreateSandboxResponse{
			TemplateID:      tpl.Name,
			SandboxID:       sandboxId,
			ClientID:        constant.E2bClientID,
			Domain:          constant.SandboxDomain,
			EnvdVersion:     constant.E2bEnvdVersion,
			EnvdAccessToken: sandboxRuntimeModel.Token,
		}
		return response.NewGResponse(http.StatusOK, res)
	case <-time.After(10 * time.Minute):
		service.RecordRestoreFailed(appIDLabel, templateIDLabel, constant.TriggerTypeManual)
		resource.LoggerService.Warning(ctx, "restore: pod event timeout after 10 minutes",
			logit.String("sandboxID", sandboxId),
			logit.String("cluster", sandboxRuntimeModel.Cluster))
		// 清理已创建但未就绪的 Pod，避免 K8s 侧残留
		_ = cluster.DeletePod(sandboxId)
		endAt := time.Now()
		_, updateErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(map[string]interface{}{
			"status":        originalStatus,
			"failed_reason": "restore timeout",
			"end_time":      endAt,
		})
		if updateErr != nil {
			resource.LoggerService.Error(ctx, "restore: failed to rollback status after timeout",
				logit.String("sandboxID", sandboxId),
				logit.String("error", updateErr.Error()))
		} else {
			resource.LoggerService.Notice(ctx, "restore: status rolled back after timeout",
				logit.String("sandboxID", sandboxId),
				logit.String("rolledBackTo", originalStatus))
		}
		_ = service.ReleaseSandboxPVCBinding(context.Background(), sandboxId, endAt, service.DefaultSandboxPVCGracePeriod())
		return response.NewGResponse(http.StatusInternalServerError, response.CreateContainerTimeoutError)
	case <-ctx.Done():
		resource.LoggerService.Warning(ctx, "restore: context canceled (client disconnected)",
			logit.String("sandboxID", sandboxId),
			logit.String("cluster", sandboxRuntimeModel.Cluster))
		// 清理已创建但未就绪的 Pod，避免 K8s 侧残留
		_ = cluster.DeletePod(sandboxId)
		endAt := time.Now()
		_, updateErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(map[string]interface{}{
			"status":        originalStatus,
			"failed_reason": "restore canceled",
			"end_time":      endAt,
		})
		if updateErr != nil {
			resource.LoggerService.Error(ctx, "restore: failed to rollback status after cancel",
				logit.String("sandboxID", sandboxId),
				logit.String("error", updateErr.Error()))
		} else {
			resource.LoggerService.Notice(ctx, "restore: status rolled back after cancel",
				logit.String("sandboxID", sandboxId),
				logit.String("rolledBackTo", originalStatus))
		}
		_ = service.ReleaseSandboxPVCBinding(context.Background(), sandboxId, endAt, service.DefaultSandboxPVCGracePeriod())
		return response.NewGResponse(http.StatusInternalServerError, response.CreateContainerTimeoutError)
	}
}

func handleDeleteSandbox(ctx context.Context, req ghttp.Request) ghttp.Response {
	sandboxId := req.Param(":sandboxID")
	appID := ctx.Value(constant.CtxAppIDKey).(int32)

	resource.LoggerService.Notice(ctx, "delete sandbox", logit.String("sandboxID", sandboxId))

	// 在删除前查询沙箱信息，用于后续记录历史
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxRuntimeModel, queryErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxId)).First()
	if queryErr != nil {
		resource.LoggerService.Error(ctx, "delete sandbox failed, query failed",
			logit.String("sandboxID", sandboxId),
			logit.String("error", queryErr.Error()))
		return response.NewGResponse(http.StatusBadRequest, queryErr)
	}

	// 保存原始状态，判断是否需要记录历史
	originalStatus := sandboxRuntimeModel.Status
	needRecordHistory := (originalStatus == constant.StatusPending || originalStatus == constant.StatusRunning)

	err := service.DeleteSandbox(ctx, sandboxId, appID)
	if err != nil {
		resource.LoggerService.Error(ctx, "delete sandbox failed",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest, err)
	}

	// 只有从 pending/running 状态删除时才记录历史
	if needRecordHistory {
		// 重新查询获取更新后的 endTime
		sandboxRuntimeModel, queryErr = sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxId)).First()
		if queryErr != nil {
			resource.LoggerService.Warning(ctx, "failed to query sandbox for history record",
				logit.String("sandboxID", sandboxId),
				logit.String("error", queryErr.Error()))
		} else {
			// 查询 app 信息获取 workspace_id 和 type
			appDao := dao.Use(resource.OrmMain).App
			app, appErr := appDao.WithContext(ctx).Where(appDao.ID.Eq(sandboxRuntimeModel.AppID)).First()
			workspaceID := int32(0)
			appType := ""
			if appErr == nil && app != nil {
				workspaceID = app.WorkspaceID
				appType = app.Type
			} else if appErr != nil {
				resource.LoggerService.Warning(ctx, "failed to get app info for history record",
					logit.Int32("appID", sandboxRuntimeModel.AppID),
					logit.String("error", fmt.Sprintf("%v", appErr)))
			}

			// 从 ctx 获取 loginUser
			loginUser := ""
			if user, ok := ctx.Value(constant.CtxLoginUserKey).(string); ok {
				loginUser = user
			}

			service.RecordDestroyHistory(
				sandboxRuntimeModel.SandboxUUID,
				sandboxRuntimeModel.AppID,
				sandboxRuntimeModel.TemplateID,
				sandboxRuntimeModel.Name,
				sandboxRuntimeModel.Cluster,
				sandboxRuntimeModel.CreateUser,
				loginUser,
				sandboxRuntimeModel.StartTime,
				sandboxRuntimeModel.EndTime,
				"user_kill",
				sandboxRuntimeModel.NodeIP,
				sandboxRuntimeModel.PortIP,
				"",
				sandboxRuntimeModel.CPU,
				sandboxRuntimeModel.Memory,
				sandboxRuntimeModel.Disk,
				workspaceID,
				appType,
			)
		}
	}

	resource.LoggerService.Notice(ctx, "delete sandbox success",
		logit.String("sandboxID", sandboxId))
	return response.NewGResponse(http.StatusNoContent, nil)
}

func handleReleaseSandboxPVC(ctx context.Context, req ghttp.Request) ghttp.Response {
	sandboxID := req.Param(":sandboxID")
	appID := ctx.Value(constant.CtxAppIDKey).(int32)

	resource.LoggerService.Notice(ctx, "release sandbox pvc", logit.String("sandboxID", sandboxID))

	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "release sandbox pvc failed, query failed",
			logit.String("sandboxID", sandboxID),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusNotFound, response.ResourceNotFoundErr)
	}
	if appID != runtime.AppID {
		resource.LoggerService.Warning(ctx, "release sandbox pvc failed, appID not match",
			logit.String("sandboxID", sandboxID),
			logit.Int32("requestAppID", appID),
			logit.Int32("sandboxAppID", runtime.AppID))
		return response.NewGResponse(http.StatusForbidden, response.AppNotMatchErr)
	}

	svc := service.NewSandboxRuntimeService()
	if err := svc.ReleaseSandboxPVC(ctx, sandboxID); err != nil {
		resource.LoggerService.Error(ctx, "release sandbox pvc failed",
			logit.String("sandboxID", sandboxID),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError, err)
	}

	resource.LoggerService.Notice(ctx, "release sandbox pvc success", logit.String("sandboxID", sandboxID))
	return response.NewGResponse(http.StatusNoContent, nil)
}

func handleSetSandboxTimeout(ctx context.Context, req ghttp.Request) ghttp.Response {

	sandboxId := req.Param(":sandboxID")
	request := &SetSandboxTimeoutRequest{}

	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}
	resource.LoggerService.Notice(ctx, "set sandbox timeout", logit.String("sandboxID", sandboxId),
		logit.Int32("timeout", request.Timeout))

	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxRuntimeModel, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxId)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "set sandbox timeout fail, query failed",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError,
			response.QueryDbErr)
	}
	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	if appID != sandboxRuntimeModel.AppID {
		resource.LoggerService.Warning(ctx, "set sandbox timeout fail, appID not match",
			logit.String("sandboxID", sandboxId),
			logit.Int32("requestAppID", appID),
			logit.Int32("sandboxAppID", sandboxRuntimeModel.AppID))
		return response.NewGResponse(http.StatusInternalServerError,
			response.AppNotMatchErr)
	}

	if constant.StatusRunning != sandboxRuntimeModel.Status {
		resource.LoggerService.Warning(ctx, "set sandbox timeout fail, sandbox not running",
			logit.String("sandboxID", sandboxId),
			logit.String("currentStatus", sandboxRuntimeModel.Status))
		return response.NewGResponse(http.StatusInternalServerError,
			response.ErrParams)
	}

	// 设置过期时间;
	if request.Timeout > 0 {
		sandboxRuntimeModel.Timeout = request.Timeout
		// timeout_time 永远等于 start_time + timeout(秒)
		startAt := sandboxRuntimeModel.StartTime
		if startAt.IsZero() {
			startAt = time.Now()
		}
		sandboxRuntimeModel.TimeoutTime = service.GetTimeoutTime(request.Timeout, startAt)
		sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(sandboxRuntimeModel)
		resource.LoggerService.Notice(ctx, "set sandbox timeout success",
			logit.String("sandboxID", sandboxId),
			logit.Int32("timeout", request.Timeout))
	}

	res := GetSandboxTimeoutResponse{
		Timeout: request.Timeout,
	}
	resource.LoggerService.Notice(ctx, "set sandbox timeout completed",
		logit.String("sandboxID", sandboxId),
		logit.Int32("timeout", request.Timeout))
	return response.NewGResponse(http.StatusOK, res)
}

func handleCreatePreheatJob(ctx context.Context, req ghttp.Request) ghttp.Response {

	adminToken, ok := req.Header("token")
	if !ok || adminToken != constant.AdminToken {
		return response.NewGResponse(http.StatusUnauthorized, response.ErrParams)
	}

	request := &service.CreatePreheatRequest{}
	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}

	resource.LoggerService.Notice(ctx, "create preheat job",
		logit.String("image", request.ImageURL),
		logit.String("clusters", strings.Join(request.ClusterList, ",")))

	// 创建预热服务
	preheatService := &service.PreheatService{}
	preheatResponse, err := preheatService.CreatePreheatJob(ctx, request)
	if err != nil {
		resource.LoggerService.Error(ctx, "create preheat job failed",
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest, err)
	}

	return response.NewGResponse(http.StatusCreated, preheatResponse)
}

func handlePvBinding(ctx context.Context, req ghttp.Request) ghttp.Response {
	request := &service.BindPvRequest{}
	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}

	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	if request.PvcPrefix == "" {
		request.PvcPrefix = fmt.Sprintf("app-%d", appID)
	}
	if request.MountPath == "" {
		request.MountPath = "/shared"
	}
	if request.SubDir == "" {
		request.SubDir = fmt.Sprintf("/app-%d", appID)
	}

	bindPvService := &service.BindPvService{}

	clusters := request.Clusters
	if len(clusters) == 0 {
		clusterName := service.NewAppService().GetAppCluster(appID, "")
		clusters = []string{clusterName}
	}

	pvName := request.PvcPrefix + "-pv"
	pvcName := request.PvcPrefix + "-pvc"
	bindResponse := &service.BindPvResponse{}

	appPvcDao := dao.Use(resource.OrmMain).AppPvcBinding
	if request.SaveDb {
		existCnt, err := appPvcDao.Where(
			appPvcDao.PvcName.Eq(pvcName),
		).Count()
		if err != nil {
			return response.NewGResponse(http.StatusBadRequest, err)
		}
		if existCnt > 0 {
			return response.NewGResponse(http.StatusBadRequest, errors.New("pvc already exist"))
		}
		existCnt, err = appPvcDao.Where(
			appPvcDao.AppID.Eq(appID),
			appPvcDao.MountPath.Eq(request.MountPath),
		).Count()
		if err != nil {
			return response.NewGResponse(http.StatusBadRequest, err)
		}
		if existCnt > 0 {
			return response.NewGResponse(http.StatusBadRequest, errors.New("mount path already exist"))
		}
		appPvBnd := &model.AppPvcBinding{
			AppID:      appID,
			PvcName:    pvcName,
			MountPath:  request.MountPath,
			CreateTime: time.Now(),
			UpdateTime: time.Now(),
		}
		saveErr := appPvcDao.Save(appPvBnd)
		if saveErr != nil {
			return response.NewGResponse(http.StatusBadRequest, saveErr)
		}
		bindResponse.BindID = appPvBnd.ID
	} else {
		appPvBnd, err := appPvcDao.Where(
			appPvcDao.AppID.Eq(appID),
			appPvcDao.PvcName.Eq(pvcName),
			appPvcDao.MountPath.Eq(request.MountPath),
		).First()
		if err != nil || appPvBnd == nil {
			return response.NewGResponse(http.StatusBadRequest, errors.New("record is not exist but saveDb is false"))
		}
		bindResponse.BindID = appPvBnd.ID
	}
	for _, clusterName := range clusters {
		cluster := resource.ClusterManager.GetClusterByName(clusterName)
		resource.LoggerService.Notice(ctx, "bind pvc",
			logit.Int32("appId", appID),
			logit.String("cluster", clusterName),
			logit.String("pvcName", request.PvcPrefix),
			logit.String("mountPath", request.MountPath))

		err := bindPvService.BindPv(ctx, cluster, pvcName, pvName, request.SubDir)
		if err != nil {
			resource.LoggerService.Error(ctx, "bind pvc failed",
				logit.String("cluster", clusterName),
				logit.String("error", err.Error()))
			return response.NewGResponse(http.StatusBadRequest, err)
		}
	}

	return response.NewGResponse(http.StatusCreated, bindResponse)
}

func handleCardCallback(ctx context.Context, req ghttp.Request) ghttp.Response {
	// Check if this is a verification request (echostr parameter)
	echoStr, _ := req.Query("echostr")
	if echoStr != "" {
		resource.LoggerService.Notice(ctx, "card callback verification request",
			logit.String("echostr", echoStr))
		return ghttp.NewTextResponse(http.StatusOK, []byte(echoStr))
	}

	// Extract callback parameters from form
	appKey := req.PostFormDefault("app_key", "")
	eventID := req.PostFormDefault("event_id", "")
	userID := req.PostFormDefault("user_id", "")
	encryptedMsgJSON := req.PostFormDefault("msg_json", "")
	// Extract logid from header
	logID, _ := req.Header("logid")
	if logID == "" {
		logID, _ = req.Header("LOGID")
	}
	ctx = context.WithValue(ctx, constant.CtxLoginUserKey, userID)
	resource.LoggerService.Notice(ctx, "handle card callback request",
		logit.String("logID", logID),
		logit.String("appKey", appKey),
		logit.String("eventID", eventID),
		logit.String("userID", userID))

	// Get AES key from config
	aesKey := eacauth.GetEacAuthConfig().CardCallbackAESKey
	if aesKey == "" {
		resource.LoggerService.Error(ctx, "card callback AES key not configured")
		return response.NewGResponse(http.StatusInternalServerError,
			response.New("AES key not configured", http.StatusInternalServerError))
	}

	// Create sandbox runtime service
	sandboxRuntimeService := service.NewSandboxRuntimeService()

	// Create callback handler
	handler := card.NewCallbackHandler(aesKey, sandboxRuntimeService)

	// Handle the callback
	callbackResp, err := handler.HandleCallback(ctx, logID, appKey, eventID, userID, encryptedMsgJSON)
	if err != nil {
		resource.LoggerService.Error(ctx, "handle card callback failed",
			logit.String("error", err.Error()),
			logit.String("logID", logID))
		return response.NewGResponse(http.StatusBadRequest,
			response.New(err.Error(), http.StatusBadRequest))
	}

	resource.LoggerService.Notice(ctx, "handle card callback success",
		logit.String("logID", logID))

	return response.NewGResponse(http.StatusOK, callbackResp)
}

// SendCardRequest 发送卡片请求结构
type SendCardMsgRequest struct {
	Receiver      string `json:"receiver"`
	AppKey        string `json:"appKey"`
	AppSecret     string `json:"appSecret"`
	SandboxID     string `json:"sandboxID"`
	SandboxAppKey string `json:"sandboxAppKey"`
}

// handleSendCard 处理发送卡片请求
func handleSendCard(ctx context.Context, req ghttp.Request) ghttp.Response {
	request := &SendCardMsgRequest{}

	if err := util.Bind(req, request); err != nil {
		resource.LoggerService.Error(ctx, "send card bind request failed",
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	// Validate required parameters
	if request.Receiver == "" || request.AppKey == "" || request.AppSecret == "" {
		resource.LoggerService.Error(ctx, "send card missing required parameters")
		return response.NewGResponse(http.StatusBadRequest,
			response.New("receiver, appKey and appSecret are required", http.StatusBadRequest))
	}

	resource.LoggerService.Notice(ctx, "send card request",
		logit.String("receiver", request.Receiver),
		logit.String("appKey", request.AppKey),
		logit.String("sandboxID", request.SandboxID))

	// Create card client and send message
	client := &card.SendCardClient{}
	err := client.SendCardMsg(request.Receiver, request.AppKey, request.AppSecret,
		request.SandboxID)
	if err != nil {
		resource.LoggerService.Error(ctx, "send card failed",
			logit.String("error", err.Error()),
			logit.String("receiver", request.Receiver))
		return response.NewGResponse(http.StatusInternalServerError,
			response.New(err.Error(), http.StatusInternalServerError))
	}

	resource.LoggerService.Notice(ctx, "send card success",
		logit.String("receiver", request.Receiver))

	return response.NewGResponse(http.StatusOK, map[string]interface{}{
		"message":  "card sent successfully",
		"receiver": request.Receiver,
	})
}

// SendMessageRequest 发送消息请求结构
type SendMessageRequest struct {
	SandboxUUID string `json:"sandboxUuid"` // 沙箱UUID，用于获取对应的AppKey和AppSecret
}

// handleSendMessage 处理发送消息请求
func handleSendMessage(ctx context.Context, req ghttp.Request) ghttp.Response {
	request := &SendMessageRequest{}

	if err := util.Bind(req, request); err != nil {
		resource.LoggerService.Error(ctx, "send message bind request failed",
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	// Validate required parameters
	if request.SandboxUUID == "" {
		resource.LoggerService.Error(ctx, "send message missing required parameters")
		return response.NewGResponse(http.StatusBadRequest,
			response.New("sandboxUuid is required", http.StatusBadRequest))
	}

	resource.LoggerService.Notice(ctx, "send message request",
		logit.String("sandboxUUID", request.SandboxUUID))

	// 根据 sandboxUUID 查询沙箱信息
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxRuntime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(request.SandboxUUID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to query sandbox runtime",
			logit.String("sandboxUUID", request.SandboxUUID),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusNotFound,
			response.New(fmt.Sprintf("sandbox not found: %s", err.Error()), http.StatusNotFound))
	}

	if sandboxRuntime.ID == 0 {
		resource.LoggerService.Error(ctx, "sandbox runtime not found",
			logit.String("sandboxUUID", request.SandboxUUID))
		return response.NewGResponse(http.StatusNotFound,
			response.New("sandbox runtime not found", http.StatusNotFound))
	}

	// 解析环境变量
	envVars := make(map[string]string)
	if sandboxRuntime.EnvirVars != "" {
		if err := json.Unmarshal([]byte(sandboxRuntime.EnvirVars), &envVars); err != nil {
			resource.LoggerService.Error(ctx, "failed to parse envVars",
				logit.String("sandboxUUID", request.SandboxUUID),
				logit.String("error", err.Error()))
			return response.NewGResponse(http.StatusInternalServerError,
				response.New(fmt.Sprintf("failed to parse envVars: %s", err.Error()), http.StatusInternalServerError))
		}
	}

	// 从环境变量中获取 appKey 和 appSecret
	appKey, ok := envVars["INFOFLOW_APP_KEY"]
	if !ok || appKey == "" {
		resource.LoggerService.Error(ctx, "INFOFLOW_APP_KEY not found in envVars",
			logit.String("sandboxUUID", request.SandboxUUID))
		return response.NewGResponse(http.StatusInternalServerError,
			response.New("INFOFLOW_APP_KEY not found in sandbox environment variables", http.StatusInternalServerError))
	}

	appSecret, ok := envVars["INFOFLOW_APP_SECRET"]
	if !ok || appSecret == "" {
		resource.LoggerService.Error(ctx, "INFOFLOW_APP_SECRET not found in envVars",
			logit.String("sandboxUUID", request.SandboxUUID))
		return response.NewGResponse(http.StatusInternalServerError,
			response.New("INFOFLOW_APP_SECRET not found in sandbox environment variables", http.StatusInternalServerError))
	}

	// 使用沙箱创建用户作为消息接收者
	toUser := sandboxRuntime.CreateUser

	// 获取通知消息内容
	successMessage := common.GetPostStartNotificationMessage(ctx, resource.RedisClient, sandboxRuntime.CreateUser, sandboxRuntime.StartTime)

	resource.LoggerService.Notice(ctx, "sending message to sandbox creator",
		logit.String("sandboxUUID", request.SandboxUUID),
		logit.String("toUser", toUser),
		logit.String("appKey", appKey),
		logit.String("message", successMessage))

	// Create message client and send message
	client, err := card.NewSendMessageClient(appKey, appSecret)
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to create send message client",
			logit.String("sandboxUUID", request.SandboxUUID),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError,
			response.New(err.Error(), http.StatusInternalServerError))
	}
	err = client.SendTextMessage(ctx, toUser, successMessage, appKey, appSecret)
	if err != nil {
		resource.LoggerService.Error(ctx, "send message failed",
			logit.String("error", err.Error()),
			logit.String("sandboxUUID", request.SandboxUUID),
			logit.String("toUser", toUser))
		return response.NewGResponse(http.StatusInternalServerError,
			response.New(err.Error(), http.StatusInternalServerError))
	}

	resource.LoggerService.Notice(ctx, "send message success",
		logit.String("sandboxUUID", request.SandboxUUID),
		logit.String("toUser", toUser))

	return response.NewGResponse(http.StatusOK, map[string]interface{}{
		"message": "message sent successfully",
		"toUser":  toUser,
	})
}

// ============== PVC 迁移相关 Handler ==============

type MigrateSandboxRequest struct {
	TargetNode string `json:"targetNode"`
}

// handleMigrateSandbox E2B 侧触发 PVC 迁移
func handleMigrateSandbox(ctx context.Context, req ghttp.Request) ghttp.Response {
	sandboxID := req.Param(":sandboxID")
	request := &MigrateSandboxRequest{}
	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}
	if request.TargetNode == "" {
		return response.NewGResponse(http.StatusBadRequest, response.MigrationTargetNodeErr)
	}

	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxID)).First()
	if err != nil {
		return response.NewGResponse(http.StatusNotFound, response.ResourceNotFoundErr)
	}
	if appID != runtime.AppID {
		return response.NewGResponse(http.StatusForbidden, response.AppNotMatchErr)
	}

	migration, err := service.MigrateSandboxPVC(ctx, sandboxID, request.TargetNode)
	if err != nil {
		resource.LoggerService.Error(ctx, "migrate sandbox pvc failed",
			logit.String("sandboxID", sandboxID),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError, response.New(err.Error(), response.InternalErrCode))
	}

	return response.NewGResponse(http.StatusAccepted, migration)
}

// handleMigrateStatus E2B 侧查询迁移状态
func handleMigrateStatus(ctx context.Context, req ghttp.Request) ghttp.Response {
	sandboxID := req.Param(":sandboxID")

	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxID)).First()
	if err != nil {
		return response.NewGResponse(http.StatusNotFound, response.ResourceNotFoundErr)
	}
	if appID != runtime.AppID {
		return response.NewGResponse(http.StatusForbidden, response.AppNotMatchErr)
	}

	migration, err := service.GetMigrationStatus(ctx, sandboxID)
	if err != nil {
		return response.NewGResponse(http.StatusNotFound, response.MigrationNotFoundErr)
	}

	return response.NewGResponse(http.StatusOK, migration)
}

// ============== 定时任务相关 Handler (API Key 认证) ==============

type CreateScheduledTaskRequest struct {
	ScheduledTimes []string `json:"scheduledTimes" binding:"required,dive,required"` // ["08:00", "14:00", "18:00"]
	RepeatMode     string   `json:"repeatMode" binding:"required"`                   // "daily"
}

// handleCreateScheduledTask 创建定时任务 (API Key 认证版本)
// POST /sandboxes/:sandboxUuid/scheduled-tasks
func handleCreateScheduledTask(ctx context.Context, req ghttp.Request) ghttp.Response {
	sandboxUuid := req.Param(":sandboxUuid")
	request := &CreateScheduledTaskRequest{}

	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}

	if sandboxUuid == "" {
		resource.LoggerService.Warning(ctx, "sandboxUuid is required")
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}

	appID := ctx.Value(constant.CtxAppIDKey).(int32)
	loginUser := ctx.Value(constant.CtxLoginUserKey).(string)

	resource.LoggerService.Notice(ctx, "create scheduled task",
		logit.String("sandboxUuid", sandboxUuid),
		logit.String("scheduledTimes", strings.Join(request.ScheduledTimes, ",")),
		logit.String("repeatMode", request.RepeatMode),
		logit.Int32("appID", appID),
		logit.String("loginUser", loginUser))

	// 查询沙箱信息
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxUuid)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "sandbox not found",
			logit.String("sandboxUuid", sandboxUuid),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusNotFound, response.ResourceNotFoundErr)
	}

	// 验证 appID 是否匹配
	if appID != runtime.AppID {
		resource.LoggerService.Warning(ctx, "create scheduled task failed, appID not match",
			logit.String("sandboxUuid", sandboxUuid),
			logit.Int32("requestAppID", appID),
			logit.Int32("sandboxAppID", runtime.AppID))
		return response.NewGResponse(http.StatusForbidden, response.AppNotMatchErr)
	}

	// 调用 service 创建定时任务
	scheduledTaskSvc := service.NewScheduledTaskService()
	createReq := service.CreateScheduledTasksRequest{
		SandboxID:      runtime.ID,
		ScheduledTimes: request.ScheduledTimes,
		RepeatMode:     request.RepeatMode,
	}

	err = scheduledTaskSvc.CreateScheduledTasks(ctx, createReq)
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to create scheduled task",
			logit.String("sandboxUuid", sandboxUuid),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError, response.New(err.Error(), response.InternalErrCode))
	}

	resource.LoggerService.Notice(ctx, "create scheduled task success",
		logit.String("sandboxUuid", sandboxUuid),
		logit.String("scheduledTimes", strings.Join(request.ScheduledTimes, ",")))

	return response.NewGResponse(http.StatusOK, map[string]interface{}{
		"message": "scheduled task created successfully",
	})
}

// handleGetScheduledTasks 获取沙箱的定时任务列表 (API Key 认证版本)
// GET /sandboxes/:sandboxUuid/scheduled-tasks
func handleGetScheduledTasks(ctx context.Context, req ghttp.Request) ghttp.Response {
	sandboxUuid := req.Param(":sandboxUuid")

	if sandboxUuid == "" {
		resource.LoggerService.Warning(ctx, "sandboxUuid is required")
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}

	appID := ctx.Value(constant.CtxAppIDKey).(int32)

	resource.LoggerService.Notice(ctx, "get scheduled tasks",
		logit.String("sandboxUuid", sandboxUuid),
		logit.Int32("appID", appID))

	// 查询沙箱信息
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxUuid)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "sandbox not found",
			logit.String("sandboxUuid", sandboxUuid),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusNotFound, response.ResourceNotFoundErr)
	}

	// 验证 appID 是否匹配
	if appID != runtime.AppID {
		resource.LoggerService.Warning(ctx, "get scheduled tasks failed, appID not match",
			logit.String("sandboxUuid", sandboxUuid),
			logit.Int32("requestAppID", appID),
			logit.Int32("sandboxAppID", runtime.AppID))
		return response.NewGResponse(http.StatusForbidden, response.AppNotMatchErr)
	}

	// 调用 service 查询定时任务
	scheduledTaskSvc := service.NewScheduledTaskService()
	tasks, err := scheduledTaskSvc.GetScheduledTasks(ctx, runtime.ID)
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to get scheduled tasks",
			logit.String("sandboxUuid", sandboxUuid),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError, response.New(err.Error(), response.InternalErrCode))
	}

	resource.LoggerService.Notice(ctx, "get scheduled tasks success",
		logit.String("sandboxUuid", sandboxUuid),
		logit.Int("count", len(tasks)))

	return response.NewGResponse(http.StatusOK, tasks)
}

// handleDeleteScheduledTask 删除单个定时任务 (API Key 认证版本)
// DELETE /sandboxes/scheduled-tasks/:taskID
func handleDeleteScheduledTask(ctx context.Context, req ghttp.Request) ghttp.Response {
	taskIDStr := req.Param(":taskID")
	appID := ctx.Value(constant.CtxAppIDKey).(int32)

	taskID, err := strconv.ParseInt(taskIDStr, 10, 64)
	if err != nil {
		resource.LoggerService.Warning(ctx, "invalid taskID",
			logit.String("taskID", taskIDStr))
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}

	resource.LoggerService.Notice(ctx, "delete scheduled task",
		logit.Int64("taskID", taskID),
		logit.Int32("appID", appID))

	// 查询定时任务信息，验证权限
	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask
	task, err := scheduledTaskDao.Where(scheduledTaskDao.ID.Eq(taskID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "scheduled task not found",
			logit.Int64("taskID", taskID),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusNotFound, response.ResourceNotFoundErr)
	}

	// 查询对应的沙箱，验证 appID
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(task.SandboxID)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "sandbox not found for task",
			logit.Int64("taskID", taskID),
			logit.Int64("sandboxID", task.SandboxID),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusNotFound, response.ResourceNotFoundErr)
	}

	// 验证 appID 是否匹配
	if appID != runtime.AppID {
		resource.LoggerService.Warning(ctx, "delete scheduled task failed, appID not match",
			logit.Int64("taskID", taskID),
			logit.Int32("requestAppID", appID),
			logit.Int32("sandboxAppID", runtime.AppID))
		return response.NewGResponse(http.StatusForbidden, response.AppNotMatchErr)
	}

	// 调用 service 删除定时任务
	scheduledTaskSvc := service.NewScheduledTaskService()
	err = scheduledTaskSvc.DeleteScheduledTask(ctx, taskID)
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to delete scheduled task",
			logit.Int64("taskID", taskID),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError, response.New(err.Error(), response.InternalErrCode))
	}

	resource.LoggerService.Notice(ctx, "delete scheduled task success",
		logit.Int64("taskID", taskID))

	return response.NewGResponse(http.StatusNoContent, nil)
}

// handleDeleteScheduledTasksBySandbox 删除沙箱的所有定时任务 (API Key 认证版本)
// DELETE /sandboxes/:sandboxUuid/scheduled-tasks
func handleDeleteScheduledTasksBySandbox(ctx context.Context, req ghttp.Request) ghttp.Response {
	sandboxUuid := req.Param(":sandboxUuid")

	if sandboxUuid == "" {
		resource.LoggerService.Warning(ctx, "sandboxUuid is required")
		return response.NewGResponse(http.StatusBadRequest, response.ErrParams)
	}

	appID := ctx.Value(constant.CtxAppIDKey).(int32)

	resource.LoggerService.Notice(ctx, "delete all scheduled tasks for sandbox",
		logit.String("sandboxUuid", sandboxUuid),
		logit.Int32("appID", appID))

	// 查询沙箱信息
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	runtime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxUuid)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "sandbox not found",
			logit.String("sandboxUuid", sandboxUuid),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusNotFound, response.ResourceNotFoundErr)
	}

	// 验证 appID 是否匹配
	if appID != runtime.AppID {
		resource.LoggerService.Warning(ctx, "delete scheduled tasks failed, appID not match",
			logit.String("sandboxUuid", sandboxUuid),
			logit.Int32("requestAppID", appID),
			logit.Int32("sandboxAppID", runtime.AppID))
		return response.NewGResponse(http.StatusForbidden, response.AppNotMatchErr)
	}

	// 调用 service 删除沙箱的所有定时任务
	scheduledTaskSvc := service.NewScheduledTaskService()
	err = scheduledTaskSvc.DeleteScheduledTasksBySandbox(ctx, runtime.ID)
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to delete scheduled tasks",
			logit.String("sandboxUuid", sandboxUuid),
			logit.String("error", err.Error()))
		return response.NewGResponse(http.StatusInternalServerError, response.New(err.Error(), response.InternalErrCode))
	}

	resource.LoggerService.Notice(ctx, "delete all scheduled tasks success",
		logit.String("sandboxUuid", sandboxUuid))

	return response.NewGResponse(http.StatusNoContent, nil)
}
