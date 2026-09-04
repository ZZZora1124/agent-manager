package service

import (
	"context"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"time"

	"icode.baidu.com/baidu/gdp/logit"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/instance"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
)

func DeleteSandbox(ctx context.Context, sandboxId string, appID int32) error {
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	sandboxRuntimeModel, err := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxId)).First()
	if err != nil {
		resource.LoggerService.Error(ctx, "delete sandbox fail, query failed",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		return response.QueryDbErr
	}
	if appID != sandboxRuntimeModel.AppID {
		resource.LoggerService.Warning(ctx, "delete sandbox fail, appID not match",
			logit.String("sandboxID", sandboxId),
			logit.Int32("requestAppID", appID),
			logit.Int32("sandboxAppID", sandboxRuntimeModel.AppID))
		return response.AppNotMatchErr
	}

	clusterName := sandboxRuntimeModel.Cluster
	resource.LoggerService.Notice(ctx, "deleting sandbox",
		logit.String("sandboxID", sandboxId),
		logit.String("cluster", clusterName),
		logit.String("status", sandboxRuntimeModel.Status))

	resource.ClusterManager.GetClusterByName(clusterName).DeletePod(sandboxId)

	// 删除 IP -> Owner 缓存
	_ = common.DeleteSandboxOwnerCache(ctx, resource.RedisClient, resource.LoggerService, sandboxRuntimeModel.PortIP)

	switch sandboxRuntimeModel.Status {
	case constant.StatusPending:
		// Pending 状态杀死时不计费：end_time=StartTime
		sandboxRuntimeModel.EndTime = sandboxRuntimeModel.StartTime
	case constant.StatusRunning:
		// Running 状态杀死时计费：end_time=now
		sandboxRuntimeModel.EndTime = time.Now()
	case constant.StatusKill, constant.StatusExpiredKill, constant.StatusReleased:
		// 已关闭/已释放，删除接口保持幂等，不重复记录历史
		return nil
	default:
		resource.LoggerService.Warning(ctx, "delete sandbox fail, status not match",
			logit.String("sandboxID", sandboxId),
			logit.String("currentStatus", sandboxRuntimeModel.Status))
		return response.ErrParams
	}

	if sandboxRuntimeModel.UsePvc {
		// usePVC=true：关闭后 sandbox 进入 kill（PVC 保留到期后再释放）
		sandboxRuntimeModel.Status = constant.StatusKill

		// 处理关闭前的定时任务
		ProcessScheduledTasksBeforeShutdown(ctx, sandboxRuntimeModel.ID, sandboxId, sandboxRuntimeModel.CreateUser)

	} else {
		// usePVC=false：关闭后进入 kill（与 usePVC=true 保持一致）
		sandboxRuntimeModel.Status = constant.StatusKill
	}
	_, err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeModel.ID)).Updates(sandboxRuntimeModel)
	if err != nil {
		resource.LoggerService.Error(ctx, "update sandbox status fail",
			logit.String("sandboxID", sandboxId),
			logit.String("error", err.Error()))
		return response.QueryDbErr
	}

	if sandboxRuntimeModel.UsePvc {
		if releaseErr := ReleaseSandboxPVCBinding(ctx, sandboxRuntimeModel.SandboxUUID, sandboxRuntimeModel.EndTime, DefaultSandboxPVCGracePeriod()); releaseErr != nil {
			resource.LoggerService.Error(ctx, "release sandbox pvc binding failed",
				logit.String("sandboxID", sandboxRuntimeModel.SandboxUUID),
				logit.String("error", releaseErr.Error()))
		}

		// 懒迁移触发：将 local-path PVC 数据异步拷贝到 CFS
		binding, bindErr := GetSandboxPVCBinding(ctx, sandboxRuntimeModel.SandboxUUID)
		// 跨集群 PVC 迁移：将 app_id=2071 且 storage_class=sharedcfs 的 PVC 从老集群迁移到 agent-sandbox2-prod
		if bindErr == nil && binding != nil && common.IsCfsMigrateUser(sandboxRuntimeModel.CreateUser) {
			if err := MigrateSbxPVCToNewCluster(ctx, sandboxRuntimeModel.SandboxUUID, binding, sandboxRuntimeModel.Cluster,
				sandboxRuntimeModel.AppID); err != nil {
				resource.LoggerService.Warning(ctx, "migrate PVC to new cluster failed",
					logit.String("sandboxUUID", sandboxRuntimeModel.SandboxUUID),
					logit.String("error", err.Error()))
			}
		}

		if bindErr == nil && (shouldLazyMigrate(sandboxRuntimeModel, binding) || shouldMigrateToNewCFS(sandboxRuntimeModel, binding)) {
			// 重新获取最新的 cluster 信息（可能已被跨集群迁移更新）
			sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
			latestRuntime, latestErr := sandboxRuntimeDao.Where(sandboxRuntimeDao.SandboxUUID.Eq(sandboxRuntimeModel.SandboxUUID)).First()
			migrateClusterName := clusterName
			if latestErr == nil && latestRuntime != nil {
				migrateClusterName = latestRuntime.Cluster
			}
			cluster := resource.ClusterManager.GetClusterByName(migrateClusterName)
			if cluster != nil {
				go TriggerLazyStorageMigration(ctx, sandboxRuntimeModel.SandboxUUID, binding, cluster, sandboxRuntimeModel.CreateUser)
			}
		}

	}
	redisKey := constant.BackendIPPrefix + sandboxId
	resource.LoggerService.Notice(ctx, "deleting redis key for sandbox",
		logit.String("sandboxID", sandboxId),
		logit.String("cluster", clusterName),
		logit.String("redisKey", redisKey))
	_, err = resource.RedisClient.Del(ctx, redisKey).Result()
	if err != nil {
		resource.LoggerService.Error(ctx, "delete redis key failed",
			logit.String("sandboxID", sandboxId),
			logit.String("redisKey", redisKey),
			logit.String("error", err.Error()))
	}

	resource.LoggerService.Notice(ctx, "delete sandbox success",
		logit.String("sandboxID", sandboxId),
		logit.String("cluster", clusterName),
		logit.String("finalStatus", sandboxRuntimeModel.Status))
	return nil
}

// ProcessScheduledTasksBeforeShutdown 处理沙箱关闭前的定时任务
// sandboxRuntimeID: 沙箱运行时数据库ID
// sandboxUUID: 沙箱UUID
// createUser: 创建用户
func ProcessScheduledTasksBeforeShutdown(ctx context.Context, sandboxRuntimeID int64, sandboxUUID, createUser string) {
	resource.LoggerService.Notice(ctx, "fetching cron jobs before pod deletion",
		logit.String("sandboxID", sandboxUUID))

	cronJobHandler := instance.NewCronJobHandler(*dao.Use(resource.OrmMain))
	cronJobParams := instance.FetchAndProcessCronJobsParams{
		SandboxUUID:    sandboxUUID,
		CronFilePath:   "/home/gem/.openclaw/cron/jobs.json",
		AdvanceMinutes: 10,
	}

	nextRunTimeInfos, err := cronJobHandler.FetchAndProcessCronJobs(ctx, cronJobParams)
	if err != nil {
		resource.LoggerService.Warning(ctx, "failed to fetch cron jobs",
			logit.String("sandboxID", sandboxUUID),
			logit.String("error", err.Error()))
		return
	}

	resource.LoggerService.Notice(ctx, "successfully fetched cron jobs",
		logit.String("sandboxID", sandboxUUID),
		logit.Int("count", len(nextRunTimeInfos)))

	// 提取定时任务信息并存储到数据库
	scheduledTaskDao := dao.Use(resource.OrmMain).ScheduledTask

	// 获取数据库中当前 sandbox 的所有定时任务（包括 deleted=true 和 deleted=false）
	allTasks, queryErr := scheduledTaskDao.Where(
		scheduledTaskDao.SandboxID.Eq(sandboxRuntimeID),
	).Find()

	if queryErr != nil {
		resource.LoggerService.Error(ctx, "failed to query existing scheduled tasks",
			logit.String("sandboxID", sandboxUUID),
			logit.String("error", queryErr.Error()))
		return
	}

	// 统计当前有效任务数量（deleted=false）
	existingCount := int64(0)
	for _, task := range allTasks {
		if !task.Deleted {
			existingCount++
		}
	}

	resource.LoggerService.Notice(ctx, "existing scheduled tasks count",
		logit.String("sandboxID", sandboxUUID),
		logit.Int64("activeCount", existingCount),
		logit.Int("totalCount", len(allTasks)))

	// 构建 nextRunTimeInfos 的 JobID 映射，用于快速查找
	nextRunTimeInfosMap := make(map[string]*instance.NextRunTimeInfo)
	for i := range nextRunTimeInfos {
		nextRunTimeInfosMap[nextRunTimeInfos[i].JobID] = &nextRunTimeInfos[i]
	}

	// 第一步：处理数据库中 deleted=false 但 nextRunTimeInfos 中不存在的任务 -> 标记为 deleted
	for _, task := range allTasks {
		if !task.Deleted {
			if _, exists := nextRunTimeInfosMap[task.JobID]; !exists {
				// nextRunTimeInfos 中不存在，标记为删除
				_, updateErr := scheduledTaskDao.Where(
					scheduledTaskDao.ID.Eq(task.ID),
				).Update(scheduledTaskDao.Deleted, true)

				if updateErr != nil {
					resource.LoggerService.Error(ctx, "failed to mark task as deleted",
						logit.String("sandboxID", sandboxUUID),
						logit.String("jobId", task.JobID),
						logit.Int64("taskId", task.ID),
						logit.String("error", updateErr.Error()))
				} else {
					existingCount-- // 删除成功后计数减1
					resource.LoggerService.Notice(ctx, "successfully marked task as deleted",
						logit.String("sandboxID", sandboxUUID),
						logit.String("jobId", task.JobID),
						logit.Int64("taskId", task.ID),
						logit.Int64("remainingCount", existingCount))
				}
			}
		}
	}

	// 构建数据库任务的 JobID 映射，用于快速查找（包括所有任务，不管 deleted 状态）
	allTasksMap := make(map[string]*model.ScheduledTask)
	for i := range allTasks {
		allTasksMap[allTasks[i].JobID] = allTasks[i]
	}

	// 第二步：处理 nextRunTimeInfos 中的任务
	for _, info := range nextRunTimeInfos {
		resource.LoggerService.Notice(ctx, "processing cron job",
			logit.String("jobName", info.JobName),
			logit.String("jobId", info.JobID),
			logit.String("nextRunTime", info.AdjustedNextRunStr),
			logit.String("scheduleKind", info.ScheduleKind),
			logit.Bool("enabled", info.Enabled))

		if existingTask, exists := allTasksMap[info.JobID]; exists {
			// 情况2：数据库中存在这个任务（不管 deleted 状态）
			updateMap := make(map[string]interface{})
			updateMap["scheduled_time"] = info.AdjustedNextRunStr
			updateMap["repeat_mode"] = info.ScheduleKind
			updateMap["description"] = info.JobName

			// 根据 enabled 决定 deleted 状态
			if info.Enabled {
				// 需要激活任务
				if existingTask.Deleted {
					// 之前是删除状态，现在要激活，需要检查数量限制
					if existingCount >= 10 {
						resource.LoggerService.Warning(ctx, "scheduled task limit reached, cannot activate job",
							logit.String("sandboxID", sandboxUUID),
							logit.Int64("currentCount", existingCount),
							logit.String("jobId", info.JobID),
							logit.String("jobName", info.JobName))
						continue
					}
				}
				updateMap["deleted"] = false
			} else {
				// 需要禁用任务
				updateMap["deleted"] = true
			}

			wasDeleted := existingTask.Deleted
			_, updateErr := scheduledTaskDao.Where(
				scheduledTaskDao.ID.Eq(existingTask.ID),
			).Updates(updateMap)

			if updateErr != nil {
				resource.LoggerService.Error(ctx, "failed to update scheduled task",
					logit.String("sandboxID", sandboxUUID),
					logit.String("jobId", info.JobID),
					logit.String("jobName", info.JobName),
					logit.Int64("taskId", existingTask.ID),
					logit.String("error", updateErr.Error()))
			} else {
				// 更新 existingCount
				if wasDeleted && info.Enabled {
					existingCount++ // 从删除状态激活
				} else if !wasDeleted && !info.Enabled {
					existingCount-- // 从激活状态禁用
				}
				resource.LoggerService.Notice(ctx, "successfully updated scheduled task",
					logit.String("sandboxID", sandboxUUID),
					logit.String("jobId", info.JobID),
					logit.String("jobName", info.JobName),
					logit.Int64("taskId", existingTask.ID),
					logit.Int64("currentCount", existingCount))
			}
		} else {
			// 情况3：数据库中不存在这个任务
			if !info.Enabled {
				resource.LoggerService.Notice(ctx, "skipping disabled job not in database",
					logit.String("sandboxID", sandboxUUID),
					logit.String("jobId", info.JobID),
					logit.String("jobName", info.JobName))
				continue
			}

			// 检查是否超过限制（10个）
			if existingCount >= 10 {
				resource.LoggerService.Warning(ctx, "scheduled task limit reached, cannot create job",
					logit.String("sandboxID", sandboxUUID),
					logit.Int64("currentCount", existingCount),
					logit.String("jobId", info.JobID),
					logit.String("jobName", info.JobName))
				continue
			}

			scheduledTask := model.ScheduledTask{
				SandboxID:     sandboxRuntimeID,
				SandboxUUID:   sandboxUUID,
				JobID:         info.JobID,
				ScheduledTime: info.AdjustedNextRunStr,
				RepeatMode:    info.ScheduleKind,
				Description:   info.JobName,
				CreateUser:    createUser,
				CreateTime:    time.Now(),
				Deleted:       false,
			}

			if err := scheduledTaskDao.Create(&scheduledTask); err != nil {
				resource.LoggerService.Error(ctx, "failed to create scheduled task",
					logit.String("sandboxID", sandboxUUID),
					logit.String("jobId", info.JobID),
					logit.String("jobName", info.JobName),
					logit.String("error", err.Error()))
			} else {
				existingCount++ // 创建成功后计数加1
				resource.LoggerService.Notice(ctx, "successfully created scheduled task",
					logit.String("sandboxID", sandboxUUID),
					logit.Int64("taskId", scheduledTask.ID),
					logit.String("jobId", info.JobID),
					logit.String("jobName", info.JobName),
					logit.Int64("currentCount", existingCount))
			}
		}
	}

	resource.LoggerService.Notice(ctx, "final active scheduled tasks count",
		logit.String("sandboxID", sandboxUUID),
		logit.Int64("count", existingCount))
}
