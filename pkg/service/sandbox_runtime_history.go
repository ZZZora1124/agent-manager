package service

import (
	"context"
	"time"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"

	"icode.baidu.com/baidu/gdp/logit"
)

// RecordDestroyHistory 记录沙箱销毁历史
// 参数：
//   - sandboxUUID: 沙箱唯一标识
//   - appID: 所属应用ID
//   - templateID: 所属模板ID
//   - name: 实例名称
//   - cluster: 所属集群
//   - createUser: 创建用户
//   - deleteUser: 删除用户
//   - startTime: 开始时间
//   - endTime: 结束时间
//   - status: 销毁后状态 (user_kill/idle_kill/expire_kill)
//   - nodeIP: 节点IP
//   - podIP: Pod IP
//   - errorMsg: 错误信息（如有）
//   - cpu: CPU毫核数
//   - memory: 内存大小（MB）
//   - disk: 磁盘大小（GB）
//   - workspaceID: 所属工作空间ID
//   - appType: 应用类型（plat/mcp）
func RecordDestroyHistory(sandboxUUID string, appID, templateID int32, name, cluster, createUser, deleteUser string, startTime, endTime time.Time, status, nodeIP, podIP, errorMsg string, cpu, memory, disk, workspaceID int32, appType string) {
	history := model.SandboxRuntimeHistory{
		SandboxUUID: sandboxUUID,
		AppID:       appID,
		TemplateID:  templateID,
		Name:        name,
		Cluster:     cluster,
		CreateUser:  createUser,
		DeleteUser:  deleteUser,
		StartTime:   startTime,
		EndTime:     endTime,
		Status:      status,
		NodeIP:      nodeIP,
		PodIP:       podIP,
		ErrorMsg:    errorMsg,
		// 新增字段
		CPU:         cpu,
		Memory:      memory,
		Disk:        disk,
		WorkspaceID: workspaceID,
		Type:        appType,
	}

	// 直接使用 GORM 创建记录
	if err := resource.OrmMain.Create(&history).Error; err != nil {
		// 只记录警告日志，不阻塞主流程
		resource.LoggerService.Warning(context.Background(), "record sandbox runtime history failed",
			logit.String("sandboxUUID", sandboxUUID),
			logit.String("error", err.Error()))
	}
}
