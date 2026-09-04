package model

import "time"

const TableNameSandboxPVCMigration = "sandbox_pvc_migration"

const (
	PVCMigrationStatusPending = "pending"
	PVCMigrationStatusRunning = "running"
	PVCMigrationStatusDone    = "done"
	PVCMigrationStatusFailed  = "failed"
	// PVCMigrationStatusCleaned 迁移完成且旧 PVC 已清理
	PVCMigrationStatusCleaned = "cleaned"
)

const (
	PVCMigrationTypeCrossNode    = "cross_node"
	PVCMigrationTypeCrossStorage = "cross_storage"
)

type SandboxPVCMigration struct {
	ID                 int64      `gorm:"column:id;primaryKey;autoIncrement:true" json:"id"`
	SandboxUUID        string     `gorm:"column:sandbox_uuid;not null" json:"sandbox_uuid"`
	BindingID          int64      `gorm:"column:binding_id;not null" json:"binding_id"`
	SourceNode         string     `gorm:"column:source_node;not null" json:"source_node"`
	TargetNode         string     `gorm:"column:target_node;not null" json:"target_node"`
	SourcePVCName      string     `gorm:"column:source_pvc_name;not null" json:"source_pvc_name"`
	SourcePVName       string     `gorm:"column:source_pv_name;not null" json:"source_pv_name"`
	SourcePVPath       string     `gorm:"column:source_pv_path;not null" json:"source_pv_path"`
	Cluster            string     `gorm:"column:cluster;not null" json:"cluster"`
	Namespace          string     `gorm:"column:namespace;not null" json:"namespace"`
	Status             string     `gorm:"column:status;not null" json:"status"`
	ErrorMsg           string     `gorm:"column:error_msg" json:"error_msg"`
	MigrationType      string     `gorm:"column:migration_type;not null;default:cross_node" json:"migration_type"`
	TargetStorageClass string     `gorm:"column:target_storage_class;not null;default:''" json:"target_storage_class"`
	TargetPVCName      string     `gorm:"column:target_pvc_name;not null;default:''" json:"target_pvc_name"`
	CreateTime         time.Time  `gorm:"column:create_time;not null" json:"create_time"`
	StartTime          *time.Time `gorm:"column:start_time" json:"start_time"`
	EndTime            *time.Time `gorm:"column:end_time" json:"end_time"`
}

func (*SandboxPVCMigration) TableName() string {
	return TableNameSandboxPVCMigration
}
