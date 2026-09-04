package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"gorm.io/gorm"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/mcpcenter"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/logit"
)

type appService struct{}

func NewAppService() AppService {
	return &appService{}
}

type AppService interface {
	GetAppId(ctx context.Context, workspaceID int) (map[string]interface{}, error)
	GetApiKey(workspaceID int) (map[string]interface{}, error)
	UpdateAccount(ctx context.Context, workspaceID int, cloudAccountIds []string) error
	GetAppCluster(appID int32, username string) string
}

// 查询平台和空间绑定关系
func (s *appService) GetAppId(ctx context.Context, workspaceID int) (map[string]interface{}, error) {
	logger := resource.LoggerService
	logger.Notice(ctx, "get app id", logit.Int("workspaceID", workspaceID))
	var app model.App
	// 1. 先查
	appDao := dao.Use(resource.OrmMain).App
	err := appDao.Where(appDao.WorkspaceID.Eq(int32(workspaceID)), appDao.Type.Eq("mcp")).Scan(&app)

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Error(ctx, "app found failed", logit.String("error", err.Error()))
		return map[string]interface{}{"appId": 0}, err // 其他数据库错误直接抛
	}
	// 2. 找到了直接返回
	if err == nil && app != (model.App{}) {
		return map[string]interface{}{"appId": app.ID, "limitCnt": app.LimitCnt}, nil
	}
	// 3. 没查到，手动插入
	// 查询workspaceInfo
	flag, workspaceInfo := mcpcenter.GetMcpCenterApi().GetWorkspaceInfo(workspaceID, ctx.Value(constant.CtxLoginUserKey).(string))
	workspaceName := workspaceInfo.Name
	workAccountID := ""
	if len(workspaceInfo.CloudAccountIds) > 0 {
		workAccountID = workspaceInfo.CloudAccountIds[len(workspaceInfo.CloudAccountIds)-1]
	}
	if !flag {
		return map[string]interface{}{"appId": 0}, fmt.Errorf("空间信息获取失败,请重试")
	}
	apiKey := util.GenerateRandomString(32)
	app = model.App{
		WorkspaceID:     int32(workspaceID),
		Type:            "mcp",
		CreateTime:      time.Now(),
		Owner:           ctx.Value(constant.CtxLoginUserKey).(string),
		WorkspaceName:   workspaceName,
		Account:         workAccountID,
		AccountReported: false, // 初始化为未上报
		APIKey:          apiKey,
	}
	if err := appDao.Create(&app); err != nil {
		logger.Error(ctx, "app create failed", logit.String("error", err.Error()))
		return map[string]interface{}{"appId": 0}, err
	}
	// 4. 返回新插入的 ID
	return map[string]interface{}{"appId": app.ID, "limitCnt": app.LimitCnt}, nil
}

func (s *appService) GetApiKey(workspaceID int) (map[string]interface{}, error) {
	var app model.App
	appDao := dao.Use(resource.OrmMain).App
	err := appDao.Where(appDao.WorkspaceID.Eq(int32(workspaceID)), appDao.Type.Eq("mcp")).Scan(&app)
	if err != nil {
		return map[string]interface{}{"apiKey": ""}, err
	}
	return map[string]interface{}{"apiKey": app.APIKey}, nil
}

// UpdateAccount 更新app表中的account字段
func (s *appService) UpdateAccount(ctx context.Context, workspaceID int, cloudAccountIds []string) error {
	logger := resource.LoggerService
	logger.Notice(ctx, "update account",
		logit.Int("workspaceID", workspaceID),
		logit.Int("cloudAccountIdsCount", len(cloudAccountIds)))

	if len(cloudAccountIds) == 0 {
		return fmt.Errorf("cloudAccountIds不能为空")
	}

	appDao := dao.Use(resource.OrmMain).App

	// 先查询当前的app记录，检查是否已经存在account
	var app model.App
	err := appDao.Where(appDao.WorkspaceID.Eq(int32(workspaceID)), appDao.Type.Eq("mcp")).Scan(&app)
	if err != nil {
		logger.Error(ctx, "query app failed",
			logit.Int("workspaceID", workspaceID),
			logit.String("error", err.Error()))
		return fmt.Errorf("查询应用信息失败: %w", err)
	}

	// 检查记录是否存在（ID为0表示未找到记录）
	if app.ID == 0 {
		logger.Warning(ctx, "app not found",
			logit.Int("workspaceID", workspaceID))
		return fmt.Errorf("未找到对应的workspace记录")
	}

	// 如果已经存在资源账号，则不允许更新
	if app.Account != "" {
		logger.Warning(ctx, "app already has account, cannot update",
			logit.Int("workspaceID", workspaceID),
			logit.String("existingAccount", app.Account))
		return fmt.Errorf("该应用已配置资源账号，不允许更新")
	}

	// 取最后一个cloudAccountId
	account := cloudAccountIds[len(cloudAccountIds)-1]

	// 更新account字段和上报状态
	result, err := appDao.Where(appDao.WorkspaceID.Eq(int32(workspaceID)), appDao.Type.Eq("mcp")).
		Updates(map[string]interface{}{
			"account":          account,
			"account_reported": false, // 账号更新后重置上报状态
		})
	if err != nil {
		logger.Error(ctx, "update account failed",
			logit.Int("workspaceID", workspaceID),
			logit.String("account", account),
			logit.String("error", err.Error()))
		return err
	}

	if result.RowsAffected == 0 {
		logger.Warning(ctx, "update account no rows affected",
			logit.Int("workspaceID", workspaceID))
		return fmt.Errorf("未找到对应的workspace记录")
	}

	logger.Notice(ctx, "update account success",
		logit.Int("workspaceID", workspaceID),
		logit.String("account", account))

	return nil
}

// ClusterWeight 集群及其权重配置
type ClusterWeight struct {
	Cluster string `json:"cluster"`
	Weight  int    `json:"weight"`
}

func (s *appService) GetAppCluster(appID int32, username string) string {
	appDao := dao.Use(resource.OrmMain).App
	app, err := appDao.Where(appDao.ID.Eq(appID)).First()
	if err != nil || app.Cluster == "" {
		return constant.DefaultCluster
	}

	// 尝试解析为多集群配置
	var clusters []ClusterWeight
	if err := json.Unmarshal([]byte(app.Cluster), &clusters); err == nil {
		// JSON 格式：多集群配置
		if len(clusters) == 0 {
			return constant.DefaultCluster
		}
		return selectClusterByWeight(clusters)
	}

	// 不是 JSON 格式，保持向后兼容：直接返回单个集群名
	return app.Cluster
}

// selectClusterByWeight 根据权重选择集群
// 使用加权随机算法：将权重映射到区间 [0, totalWeight)，随机生成一个值，落在哪个区间就选择对应的集群
func selectClusterByWeight(clusters []ClusterWeight) string {
	totalWeight := 0
	for _, c := range clusters {
		if c.Weight <= 0 {
			continue // 跳过权重小于等于 0 的配置
		}
		totalWeight += c.Weight
	}

	if totalWeight == 0 {
		return clusters[0].Cluster // 如果所有权重都 <= 0，返回第一个集群
	}

	// 生成随机数 [0, totalWeight)
	r := rand.New(rand.NewSource(time.Now().UnixNano())).Intn(totalWeight)

	// 累加权重，判断落在哪个区间
	accumulated := 0
	for _, c := range clusters {
		if c.Weight <= 0 {
			continue
		}
		accumulated += c.Weight
		if r < accumulated {
			return c.Cluster
		}
	}

	// 兜底：返回最后一个有效集群
	for i := len(clusters) - 1; i >= 0; i-- {
		if clusters[i].Weight > 0 {
			return clusters[i].Cluster
		}
	}

	return constant.DefaultCluster
}
