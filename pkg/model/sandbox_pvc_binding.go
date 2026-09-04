package model

import "time"

const TableNameSandboxPVCBinding = "sandbox_pvc_binding"

const (
	SandboxPVCStatusActive   = "active"
	SandboxPVCStatusKill     = "kill"
	SandboxPVCStatusReleased = "released"
	SandboxPVCStatusFailed   = "failed"
)

// SandboxPVCBinding 维护 sandbox_uuid 与 PVC 的绑定关系。
type SandboxPVCBinding struct {
	ID           int64      `gorm:"column:id;primaryKey;autoIncrement:true" json:"id"`
	SandboxUUID  string     `gorm:"column:sandbox_uuid;not null" json:"sandbox_uuid"`
	AppID        int32      `gorm:"column:app_id;not null" json:"app_id"`
	PVCName      string     `gorm:"column:pvc_name;not null" json:"pvc_name"`
	MountPath    string     `gorm:"column:mount_path;not null" json:"mount_path"`
	Cluster      string     `gorm:"column:cluster;not null" json:"cluster"`
	Namespace    string     `gorm:"column:namespace;not null" json:"namespace"`
	StorageClass string     `gorm:"column:storage_class;not null;default:local-path" json:"storage_class"`
	Status       string     `gorm:"column:status;not null" json:"status"`
	ReclaimAfter *time.Time `gorm:"column:reclaim_after" json:"reclaim_after"`
	Deleted      bool       `gorm:"column:deleted;not null" json:"deleted"`
	CreateTime   time.Time  `gorm:"column:create_time;not null" json:"create_time"`
	UpdateTime   time.Time  `gorm:"column:update_time;not null" json:"update_time"`
}

func (*SandboxPVCBinding) TableName() string {
	return TableNameSandboxPVCBinding
}
