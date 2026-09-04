package service

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus 指标定义
var (

	// CreateSandboxSuccessCounter 记录创建沙箱成功的总数
	// 标签: app_id(应用ID), template_id(模板ID)
	CreateSandboxSuccessCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "sandbox",
			Subsystem: "runtime",
			Name:      "create_success_total",
			Help:      "Total number of successful sandbox runtime create operations",
		},
		[]string{"app_id", "template_id"},
	)

	// CreateSandboxFailedCounter 记录创建沙箱失败的总数
	// 标签: app_id(应用ID), template_id(模板ID)
	CreateSandboxFailedCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "sandbox",
			Subsystem: "runtime",
			Name:      "create_failed_total",
			Help:      "Total number of failed sandbox runtime create operations",
		},
		[]string{"app_id", "template_id"},
	)

	// RestoreSandboxSuccessCounter 记录恢复沙箱成功的总数
	// 标签: app_id(应用ID), template_id(模板ID), trigger_type(触发类型: manual-普通唤醒, scheduled-定时任务唤醒)
	RestoreSandboxSuccessCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "sandbox",
			Subsystem: "runtime",
			Name:      "restore_success_total",
			Help:      "Total number of successful sandbox runtime restore operations",
		},
		[]string{"app_id", "template_id", "trigger_type"},
	)

	// RestoreSandboxFailedCounter 记录恢复沙箱失败的总数
	// 标签: app_id(应用ID), template_id(模板ID), trigger_type(触发类型: manual-普通唤醒, scheduled-定时任务唤醒)
	RestoreSandboxFailedCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "sandbox",
			Subsystem: "runtime",
			Name:      "restore_failed_total",
			Help:      "Total number of failed sandbox runtime restore operations",
		},
		[]string{"app_id", "template_id", "trigger_type"},
	)

	// RestoreSandboxDuration 记录恢复沙箱操作的耗时分布(单位:秒)，不区分成功失败
	// 标签: app_id(应用ID), template_id(模板ID), trigger_type(触发类型: manual-普通唤醒, scheduled-定时任务唤醒)
	RestoreSandboxDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "sandbox",
			Subsystem: "runtime",
			Name:      "restore_duration_seconds",
			Help:      "Duration of sandbox runtime restore operations in seconds",
			Buckets:   []float64{1, 2, 5, 10, 30, 60, 120, 240, 480}, // 自定义分桶
		},
		[]string{"app_id", "template_id", "trigger_type"},
	)

	// RestoreSandboxValidationFailedCounter 记录恢复沙箱校验失败的总数
	// 标签: app_id(应用ID), template_id(模板ID), trigger_type(触发类型: manual-普通唤醒, scheduled-定时任务唤醒), reason(失败原因)
	RestoreSandboxValidationFailedCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "sandbox",
			Subsystem: "runtime",
			Name:      "restore_validation_failed_total",
			Help:      "Total number of failed sandbox runtime restore validation checks",
		},
		[]string{"app_id", "template_id", "trigger_type", "reason"},
	)

	// CreateSandboxDuration 创建恢复沙箱操作的耗时分布(单位:秒)，不区分成功失败
	// 标签: app_id(应用ID), template_id(模板ID)
	CreateSandboxDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "sandbox",
			Subsystem: "runtime",
			Name:      "create_duration_seconds",
			Help:      "Duration of sandbox runtime create operations in seconds",
			Buckets:   []float64{1, 2, 5, 10, 30, 60, 120, 240, 480}, // 自定义分桶
		},
		[]string{"app_id", "template_id"},
	)
)

// RecordRestoreSuccess 记录恢复成功
// triggerType: constant.TriggerTypeManual 或 constant.TriggerTypeScheduled
func RecordRestoreSuccess(appID, templateID, triggerType string) {
	RestoreSandboxSuccessCounter.WithLabelValues(appID, templateID, triggerType).Inc()
}

// RecordRestoreFailed 记录恢复失败
// triggerType: constant.TriggerTypeManual 或 constant.TriggerTypeScheduled
func RecordRestoreFailed(appID, templateID, triggerType string) {
	RestoreSandboxFailedCounter.WithLabelValues(appID, templateID, triggerType).Inc()
}

// RecordRestoreValidationFailed 记录恢复校验失败
// triggerType: constant.TriggerTypeManual 或 constant.TriggerTypeScheduled
// reason: 校验失败原因，如 constant.RestoreValidationFailureReasonStatusConflict 等
func RecordRestoreValidationFailed(appID, templateID, triggerType, reason string) {
	RestoreSandboxValidationFailedCounter.WithLabelValues(appID, templateID, triggerType, reason).Inc()
}

// RecordCreateSuccess 记录创建成功
func RecordCreateSuccess(appID, templateID string) {
	CreateSandboxSuccessCounter.WithLabelValues(appID, templateID).Inc()
}

// RecordCreateFailed 记录创建失败
func RecordCreateFailed(appID, templateID string) {
	CreateSandboxFailedCounter.WithLabelValues(appID, templateID).Inc()
}
