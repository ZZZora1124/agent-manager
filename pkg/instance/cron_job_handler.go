package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/gdp/logit"
)

// CronJobHandler 处理定时任务相关操作
type CronJobHandler struct {
	fileFetcher *FileFetcher
}

// NewCronJobHandler 创建一个新的 CronJobHandler 实例
func NewCronJobHandler(db dao.Query) *CronJobHandler {
	return &CronJobHandler{
		fileFetcher: NewFileFetcher(db),
	}
}

// CronJobsFile 定时任务文件结构
type CronJobsFile struct {
	Version int       `json:"version"`
	Jobs    []CronJob `json:"jobs"`
}

// CronJob 定时任务结构
type CronJob struct {
	ID             string          `json:"id"`
	AgentID        string          `json:"agentId"`
	SessionKey     string          `json:"sessionKey"`
	Name           string          `json:"name"`
	Enabled        bool            `json:"enabled"`
	DeleteAfterRun bool            `json:"deleteAfterRun"`
	CreatedAtMs    int64           `json:"createdAtMs"`
	UpdatedAtMs    int64           `json:"updatedAtMs"`
	Schedule       *Schedule       `json:"schedule"`
	SessionTarget  string          `json:"sessionTarget"`
	WakeMode       string          `json:"wakeMode"`
	Payload        json.RawMessage `json:"payload"`
	Delivery       json.RawMessage `json:"delivery,omitempty"`
	State          *CronJobState   `json:"state,omitempty"`
}

// Schedule 定时任务调度配置
type Schedule struct {
	Kind string `json:"kind"`
}

// CronJobState 定时任务状态
type CronJobState struct {
	NextRunAtMs        *int64 `json:"nextRunAtMs,omitempty"`
	LastRunAtMs        *int64 `json:"lastRunAtMs,omitempty"`
	LastRunStatus      string `json:"lastRunStatus,omitempty"`
	LastStatus         string `json:"lastStatus,omitempty"`
	LastDurationMs     *int64 `json:"lastDurationMs,omitempty"`
	LastError          string `json:"lastError,omitempty"`
	LastDeliveryStatus string `json:"lastDeliveryStatus,omitempty"`
	ConsecutiveErrors  int    `json:"consecutiveErrors,omitempty"`
}

// NextRunTimeInfo 下次运行时间信息
type NextRunTimeInfo struct {
	JobID              string `json:"jobId"`
	JobName            string `json:"jobName"`
	ScheduleKind       string `json:"scheduleKind"`
	Enabled            bool   `json:"enabled"`
	OriginalNextRunMs  int64  `json:"originalNextRunMs"`
	OriginalNextRunStr string `json:"originalNextRunStr"`
	AdjustedNextRunMs  int64  `json:"adjustedNextRunMs"`
	AdjustedNextRunStr string `json:"adjustedNextRunStr"`
	AdvanceMinutes     int    `json:"advanceMinutes"`
}

// FetchAndProcessCronJobsParams 拉取并处理定时任务的参数
type FetchAndProcessCronJobsParams struct {
	SandboxUUID    string // 沙箱唯一标识
	CronFilePath   string // cron 文件路径，默认为 /home/gem/.openclaw/cron/jobs.json
	AdvanceMinutes int    // 提前多少分钟，默认为 10 分钟
}

// FetchAndProcessCronJobs 拉取并处理定时任务文件
// 提取所有的 nextRunAtMs，减去指定分钟数，并输出到日志
func (h *CronJobHandler) FetchAndProcessCronJobs(ctx context.Context, params FetchAndProcessCronJobsParams) ([]NextRunTimeInfo, error) {
	logger := resource.LoggerService

	// 设置默认值
	if params.CronFilePath == "" {
		params.CronFilePath = "/home/gem/.openclaw/cron/jobs.json"
	}
	if params.AdvanceMinutes == 0 {
		params.AdvanceMinutes = 10
	}

	logger.Notice(ctx, "start processing cron jobs file",
		logit.String("sandboxUUID", params.SandboxUUID),
		logit.String("cronFilePath", params.CronFilePath),
		logit.Int("advanceMinutes", params.AdvanceMinutes))

	// 拉取文件内容
	content, err := h.fileFetcher.FetchFile(ctx, FetchFileParams{
		SandboxUUID: params.SandboxUUID,
		FilePath:    params.CronFilePath,
	})
	if err != nil {
		logger.Error(ctx, "failed to fetch cron jobs file", logit.String("error", err.Error()))
		return nil, fmt.Errorf("failed to fetch cron jobs file: %w", err)
	}

	// 解析 JSON
	var cronJobsFile CronJobsFile
	if err := json.Unmarshal([]byte(content), &cronJobsFile); err != nil {
		logger.Error(ctx, "failed to parse cron jobs JSON", logit.String("error", err.Error()))
		return nil, fmt.Errorf("failed to parse cron jobs JSON: %w", err)
	}

	logger.Notice(ctx, "successfully parsed cron jobs file",
		logit.Int("version", cronJobsFile.Version),
		logit.Int("totalJobs", len(cronJobsFile.Jobs)))

	// 获取当前时间的毫秒时间戳
	currentTimeMs := time.Now().UnixMilli()

	// 提取并处理 nextRunAtMs
	var nextRunTimeInfos []NextRunTimeInfo
	var filteredCount int                                 // 被过滤掉的任务数量
	advanceMs := int64(params.AdvanceMinutes * 60 * 1000) // 转换为毫秒

	for _, job := range cronJobsFile.Jobs {
		if job.State != nil && job.State.NextRunAtMs != nil {
			originalNextRunMs := *job.State.NextRunAtMs

			// 获取 scheduleKind
			scheduleKind := ""
			if job.Schedule != nil {
				scheduleKind = job.Schedule.Kind
			}

			// 过滤条件 1: enabled 必须为 true (但 scheduleKind 为 cron 时不过滤)
			if !job.Enabled && scheduleKind != "cron" {
				logger.Debug(ctx, "skip disabled cron job",
					logit.String("jobId", job.ID),
					logit.String("jobName", job.Name),
					logit.Bool("enabled", job.Enabled),
					logit.String("scheduleKind", scheduleKind))
				filteredCount++
				continue
			}

			// 过滤条件 2: nextRunAtMs 必须大于当前时间
			if originalNextRunMs <= currentTimeMs {
				logger.Debug(ctx, "skip expired cron job",
					logit.String("jobId", job.ID),
					logit.String("jobName", job.Name),
					logit.Int64("nextRunAtMs", originalNextRunMs),
					logit.Int64("currentTimeMs", currentTimeMs))
				filteredCount++
				continue
			}

			// 计算调整后的时间（减去指定分钟数）
			adjustedNextRunMs := originalNextRunMs - advanceMs

			// 转换为可读时间格式
			adjustedTime := time.UnixMilli(adjustedNextRunMs)

			info := NextRunTimeInfo{
				JobID:              job.ID,
				JobName:            job.Name,
				ScheduleKind:       scheduleKind,
				Enabled:            job.Enabled,
				OriginalNextRunMs:  originalNextRunMs,
				OriginalNextRunStr: time.UnixMilli(originalNextRunMs).Format("2006-01-02 15:04"),
				AdjustedNextRunMs:  adjustedNextRunMs,
				AdjustedNextRunStr: adjustedTime.Format("2006-01-02 15:04"),
				AdvanceMinutes:     params.AdvanceMinutes,
			}

			nextRunTimeInfos = append(nextRunTimeInfos, info)

			// 输出到日志
			logger.Notice(ctx, "process cron job",
				logit.String("jobId", info.JobID),
				logit.String("jobName", info.JobName),
				logit.Bool("enabled", job.Enabled),
				logit.Int64("originalNextRunMs", info.OriginalNextRunMs),
				logit.String("originalNextRunTime", info.OriginalNextRunStr),
				logit.Int64("adjustedNextRunMs", info.AdjustedNextRunMs),
				logit.String("adjustedNextRunTime", info.AdjustedNextRunStr),
				logit.Int("advanceMinutes", info.AdvanceMinutes))
		}
	}

	if len(nextRunTimeInfos) == 0 {
		logger.Warning(ctx, "no qualified cron jobs found",
			logit.Int("filteredCount", filteredCount))
	} else {
		logger.Notice(ctx, "cron jobs processing completed",
			logit.Int("processedJobsCount", len(nextRunTimeInfos)),
			logit.Int("filteredCount", filteredCount))
	}

	return nextRunTimeInfos, nil
}

// GetAllCronJobs 获取所有定时任务（不做时间处理）
func (h *CronJobHandler) GetAllCronJobs(ctx context.Context, sandboxUUID string, cronFilePath string) (*CronJobsFile, error) {
	logger := resource.LoggerService

	if cronFilePath == "" {
		cronFilePath = "/home/gem/.openclaw/cron/jobs.json"
	}

	// 拉取文件内容
	content, err := h.fileFetcher.FetchFile(ctx, FetchFileParams{
		SandboxUUID: sandboxUUID,
		FilePath:    cronFilePath,
	})
	if err != nil {
		logger.Error(ctx, "failed to fetch cron jobs file", logit.String("error", err.Error()))
		return nil, fmt.Errorf("failed to fetch cron jobs file: %w", err)
	}

	// 解析 JSON
	var cronJobsFile CronJobsFile
	if err := json.Unmarshal([]byte(content), &cronJobsFile); err != nil {
		logger.Error(ctx, "failed to parse cron jobs JSON", logit.String("error", err.Error()))
		return nil, fmt.Errorf("failed to parse cron jobs JSON: %w", err)
	}

	return &cronJobsFile, nil
}

// GetEnabledCronJobs 获取所有启用的定时任务
func (h *CronJobHandler) GetEnabledCronJobs(ctx context.Context, sandboxUUID string, cronFilePath string) ([]CronJob, error) {
	cronJobsFile, err := h.GetAllCronJobs(ctx, sandboxUUID, cronFilePath)
	if err != nil {
		return nil, err
	}

	var enabledJobs []CronJob
	for _, job := range cronJobsFile.Jobs {
		if job.Enabled {
			enabledJobs = append(enabledJobs, job)
		}
	}

	return enabledJobs, nil
}
