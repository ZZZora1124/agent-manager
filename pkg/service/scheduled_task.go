package service

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/logit"
)

type scheduledTaskService struct{}

func NewScheduledTaskService() ScheduledTaskService {
	return &scheduledTaskService{}
}

type ScheduledTaskService interface {
	CreateScheduledTasks(ctx context.Context, req CreateScheduledTasksRequest) error
	GetScheduledTasks(ctx context.Context, sandboxId int64) ([]*ScheduledTaskResponse, error)
	DeleteScheduledTask(ctx context.Context, taskID int64) error
	DeleteScheduledTasksBySandbox(ctx context.Context, sandboxId int64) error
}

type CreateScheduledTasksRequest struct {
	SandboxID      int64    `json:"-"`                                               // 从路径参数获取
	ScheduledTimes []string `json:"scheduledTimes" binding:"required,dive,required"` // ["08:00", "14:00", "18:00"]
	RepeatMode     string   `json:"repeatMode" binding:"required"`                   // "daily"
	Description    string   `json:"description"`                                     // 定时任务描述
}

type ScheduledTaskResponse struct {
	ID            int64  `json:"id"`
	SandboxUUID   string `json:"sandboxUuid"`
	JobID         string `json:"jobId"`
	ScheduledTime string `json:"scheduledTime"`
	RepeatMode    string `json:"repeatMode"`
	Description   string `json:"description"`
	CreateUser    string `json:"createUser"`
	CreateTime    string `json:"createTime"`
	Deleted       bool   `json:"deleted"`
}

// CreateScheduledTasks 创建多个定时任务
func (s *scheduledTaskService) CreateScheduledTasks(ctx context.Context, req CreateScheduledTasksRequest) error {
	logger := resource.LoggerService

	// 通过 sandboxId 查询沙箱
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxRuntime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(req.SandboxID)).First()
	if err != nil {
		logger.Error(ctx, "sandbox not found",
			logit.Int64("sandboxId", req.SandboxID),
			logit.String("error", err.Error()))
		return fmt.Errorf("沙箱不存在: %d", req.SandboxID)
	}

	// 校验沙箱是否开启了 PVC 持久化
	if !sandboxRuntime.UsePvc {
		logger.Warning(ctx, "sandbox does not have PVC enabled",
			logit.Int64("sandboxId", req.SandboxID),
			logit.String("sandboxUUID", sandboxRuntime.SandboxUUID))
		return fmt.Errorf("定时任务需要开启 PVC 持久化,请先开启 PVC")
	}

	// 校验沙箱状态，只有 running, kill, expire_kill, succeeded 状态才能创建定时任务
	allowedStatuses := []string{constant.StatusRunning, constant.StatusKill, constant.StatusSucceeded, constant.StatusExpiredKill}
	statusAllowed := false
	for _, status := range allowedStatuses {
		if sandboxRuntime.Status == status {
			statusAllowed = true
			break
		}
	}
	if !statusAllowed {
		logger.Warning(ctx, "sandbox status not allowed for scheduled task",
			logit.Int64("sandboxId", req.SandboxID),
			logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
			logit.String("status", sandboxRuntime.Status))
		return fmt.Errorf("沙箱当前状态 '%s' 不支持创建定时任务，仅支持 running、kill、expire_kill、succeeded 状态", sandboxRuntime.Status)
	}

	// 校验时间格式
	timePattern := regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)
	for _, scheduledTime := range req.ScheduledTimes {
		if !timePattern.MatchString(scheduledTime) {
			return fmt.Errorf("无效的时间格式: %s, 期望格式为 HH:MM (00:00-23:59)", scheduledTime)
		}
	}

	// 获取当前用户
	createUser := ctx.Value(constant.CtxLoginUserKey).(string)

	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask

	// 检查同一个sandboxId的有效定时任务数量
	existingTaskCount, err := scheduledTaskDao.Where(
		scheduledTaskDao.SandboxID.Eq(req.SandboxID),
		scheduledTaskDao.Deleted.Is(false),
	).Count()
	if err != nil {
		logger.Error(ctx, "failed to count existing scheduled tasks",
			logit.Int64("sandboxId", req.SandboxID),
			logit.String("error", err.Error()))
		return fmt.Errorf("查询定时任务数量失败: %w", err)
	}

	// 计算本次需要创建的任务数量（排除重复的）
	newTaskCount := int64(0)
	for _, scheduledTime := range req.ScheduledTimes {
		existingTask, _ := scheduledTaskDao.Where(
			scheduledTaskDao.SandboxID.Eq(req.SandboxID),
			scheduledTaskDao.ScheduledTime.Eq(scheduledTime),
			scheduledTaskDao.Deleted.Is(false),
		).First()
		if existingTask == nil {
			newTaskCount++
		}
	}

	// 检查是否超过限制
	if existingTaskCount+newTaskCount > 5 {
		logger.Warning(ctx, "exceed maximum scheduled tasks limit",
			logit.Int64("sandboxId", req.SandboxID),
			logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
			logit.Int64("existingCount", existingTaskCount),
			logit.Int64("newCount", newTaskCount))
		return fmt.Errorf("定时任务数量超过限制，同一个沙箱最多只能创建5个有效的定时任务，当前已有%d个，本次尝试新增%d个", existingTaskCount, newTaskCount)
	}

	// 批量创建定时任务
	for _, scheduledTime := range req.ScheduledTimes {
		// 检查该沙箱是否已存在相同时间点的定时任务
		existingTask, err := scheduledTaskDao.Where(
			scheduledTaskDao.SandboxID.Eq(req.SandboxID),
			scheduledTaskDao.ScheduledTime.Eq(scheduledTime),
			scheduledTaskDao.Deleted.Is(false),
		).First()

		if err == nil && existingTask != nil {
			// 已存在相同时间点的定时任务，跳过
			logger.Warning(ctx, "scheduled task already exists, skipping",
				logit.Int64("sandboxId", req.SandboxID),
				logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
				logit.String("scheduledTime", scheduledTime))
			continue
		}

		// 创建新的定时任务
		task := &model.ScheduledTask{
			SandboxID:     req.SandboxID,
			SandboxUUID:   sandboxRuntime.SandboxUUID,
			ScheduledTime: scheduledTime,
			RepeatMode:    req.RepeatMode,
			Description:   req.Description,
			CreateUser:    createUser,
			CreateTime:    time.Now(),
			Deleted:       false,
		}

		err = scheduledTaskDao.Create(task)
		if err != nil {
			logger.Error(ctx, "failed to create scheduled task",
				logit.Int64("sandboxId", req.SandboxID),
				logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
				logit.String("scheduledTime", scheduledTime),
				logit.String("error", err.Error()))
			return fmt.Errorf("创建定时任务失败: %w", err)
		}

		logger.Notice(ctx, "scheduled task created successfully",
			logit.Int64("sandboxId", req.SandboxID),
			logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
			logit.String("sandboxName", sandboxRuntime.Name),
			logit.String("scheduledTime", scheduledTime),
			logit.String("repeatMode", req.RepeatMode),
			logit.String("createUser", createUser))
	}

	return nil
}

// GetScheduledTasks 查询沙箱的定时任务列表
func (s *scheduledTaskService) GetScheduledTasks(ctx context.Context, sandboxId int64) ([]*ScheduledTaskResponse, error) {
	logger := resource.LoggerService

	// 通过 sandboxId 查询沙箱获取 UUID
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxRuntime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxId)).First()
	if err != nil {
		logger.Error(ctx, "sandbox not found",
			logit.Int64("sandboxId", sandboxId),
			logit.String("error", err.Error()))
		return nil, fmt.Errorf("沙箱不存在: %d", sandboxId)
	}

	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask
	tasks, err := scheduledTaskDao.Where(
		scheduledTaskDao.SandboxID.Eq(sandboxId),
	).Order(scheduledTaskDao.ScheduledTime).Find()

	if err != nil {
		logger.Error(ctx, "failed to get scheduled tasks",
			logit.Int64("sandboxId", sandboxId),
			logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
			logit.String("error", err.Error()))
		return nil, fmt.Errorf("获取定时任务列表失败: %w", err)
	}

	// 转换为响应结构体
	var response []*ScheduledTaskResponse
	for _, task := range tasks {
		response = append(response, &ScheduledTaskResponse{
			ID:            task.ID,
			SandboxUUID:   task.SandboxUUID,
			JobID:         task.JobID,
			ScheduledTime: task.ScheduledTime,
			RepeatMode:    task.RepeatMode,
			Description:   task.Description,
			CreateUser:    task.CreateUser,
			CreateTime:    task.CreateTime.Format("2006-01-02T15:04:05"),
			Deleted:       task.Deleted,
		})
	}

	return response, nil
}

// DeleteScheduledTask 删除单个定时任务
func (s *scheduledTaskService) DeleteScheduledTask(ctx context.Context, taskID int64) error {
	logger := resource.LoggerService

	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask
	_, err := scheduledTaskDao.Where(scheduledTaskDao.ID.Eq(taskID)).Updates(map[string]interface{}{
		"deleted": true,
	})

	if err != nil {
		logger.Error(ctx, "failed to delete scheduled task",
			logit.Int64("taskID", taskID),
			logit.String("error", err.Error()))
		return fmt.Errorf("删除定时任务失败: %w", err)
	}

	logger.Notice(ctx, "scheduled task deleted",
		logit.Int64("taskID", taskID))

	return nil
}

// DeleteScheduledTasksBySandbox 删除沙箱的所有定时任务
func (s *scheduledTaskService) DeleteScheduledTasksBySandbox(ctx context.Context, sandboxId int64) error {
	logger := resource.LoggerService

	// 通过 sandboxId 查询沙箱获取 UUID
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxRuntime, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxId)).First()
	if err != nil {
		logger.Error(ctx, "sandbox not found",
			logit.Int64("sandboxId", sandboxId),
			logit.String("error", err.Error()))
		return fmt.Errorf("沙箱不存在: %d", sandboxId)
	}

	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask
	_, err = scheduledTaskDao.Where(
		scheduledTaskDao.SandboxID.Eq(sandboxId),
		scheduledTaskDao.Deleted.Is(false),
	).Updates(map[string]interface{}{
		"deleted": true,
	})

	if err != nil {
		logger.Error(ctx, "failed to delete scheduled tasks by sandbox",
			logit.Int64("sandboxId", sandboxId),
			logit.String("sandboxUUID", sandboxRuntime.SandboxUUID),
			logit.String("error", err.Error()))
		return fmt.Errorf("删除定时任务失败: %w", err)
	}

	logger.Notice(ctx, "scheduled tasks deleted by sandbox",
		logit.Int64("sandboxId", sandboxId),
		logit.String("sandboxUUID", sandboxRuntime.SandboxUUID))

	return nil
}
