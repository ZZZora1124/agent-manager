package constant

const CtxTraceIDKey = "traceID"

const CtxAppIDKey = "ctx.appKeyId"

const CtxLoginUserKey = "ctx.loginUsername"

const CtxScheduledTaskKey = "ctx.scheduledTask"

const StatusPending = "pending"

const StatusRunning = "running"

const StatusSucceeded = "succeeded"

const StatusFailed = "failed"

const StatusKill = "kill"

const StatusExpiredKill = "expire_kill"

// StatusReleased 表示实例已释放。
// - usePVC=false：实例关闭/过期后直接进入 released
// - usePVC=true：实例关闭/过期后先进入 kill/expire_kill；待 PVC 到期（binding.kill -> binding.released 并清理）后再迁移到 released
const StatusReleased = "released"

const E2bStatusPaused = "paused"

const E2bStatusRunning = "running"

// DefaultCluster 确保沙盒环境和线上环境均有这个集群
const DefaultCluster = "agent-cce-prod"

const AgentSandbox3Cluster = "agent-sandbox3-prod"

const PreHotImage = "iregistry.baidu-int.com/magellan-public/image-pull:1.0"

const IRegistryUsername = "ipipe01"

const IRegistryPassword = "*Ipipe123"

const BackendIPPrefix = "backend_ip:"

// SandboxIPToOwnerPrefix IP到创建人的映射缓存前缀（支持沙箱IP变化场景）
const SandboxIPToOwnerPrefix = "sandbox:ip_to_owner:"

// UgateTokenCachePrefix sandboxUUID到ugate token的映射缓存前缀
const UgateTokenCachePrefix = "sandbox:ugate_token:"

// FuseOwnerCachePrefix sandboxUUID到fuseOwner的映射缓存前缀
const FuseOwnerCachePrefix = "sandbox:fuse_owner:"

const SandboxCleanLockKey = "sandbox:clean:lock"
const SandboxPVCCleanLockKey = "sandbox:pvc:clean:lock"
const SandboxRestoreLockPrefix = "sandbox:restore:lock:"

const SandboxDomain = "agent-sandbox.baidu-int.com"

const E2bEnvdVersion = "1.0.0"

const E2bClientID = "e2b"

const TestRunMode = "test"

const TestUuidSuffix = "-test"

const RoleAdmin = "admin"

const RoleMember = "member"

const RoleSuperAdmin = "superadmin"

const DevopsJwtEnvKey = "COMATE_AUTH_TOKEN"

const OpenClawEnvKey = "OPEN_CLAW_TOKEN"

const MetadataImageKey = "agent-sandbox/image"

const MetadataModuleKey = "module"

const MetadataDockerKey = "agent-sandbox/docker"

const MetadataPostScriptKey = "agent-sandbox/postScript"

const MetadataNodeSelectorKey = "agent-sandbox/nodeSelector"

const MetadataNameKey = "agent-sandbox/name"

const MetadataCreatorKey = "agent-sandbox/creator"

const MetadataAccessToken = "agent-sandbox/access-token"

const MetadataCodeBeanKey = "codeBean"

const MetadataPvcsKey = "agent-sandbox/pvcs"

const NetWhiteListRedisKey = "sandbox:net:whitelist"

const RegistryWhiteListRedisKey = "sandbox:registry:whitelist"

const UnlimitedWhiteListRedisKey = "sandbox:unlimited:whitelist"

const TokenWhiteListRedisKey = "sandbox:token:whitelist"

const RegistryPullSecret = "iregistry.baidu-int.com"
const PoolStatusPending = "pending"
const PoolStatusReady = "ready"
const PoolStatusFailed = "failed"
const PoolStatusUsed = "used"

const SandboxPoolPreheatLockKey = "sandbox:pool:preheat:lock"
const SandboxPoolCleanLockKey = "sandbox:pool:clean:lock"
const SandboxPoolAcquireLockKeyPrefix = "sandbox:pool:acquire:lock:"
const SandboxIdleCheckLockKey = "sandbox:idle:check:lock"
const SandboxPVCMigrateLockPrefix = "sandbox:pvc:migrate:"
const SandboxStorageMigrateLockPrefix = "sandbox:storage:migrate:"

const OpenClawImageSmallRedisKey = "openclaw:image:small"

// ImageSmallByTplIDRedisKeyPrefix 按模版ID存储小流量配置的Redis Key前缀，完整key格式: image:small:{tplID}
const ImageSmallByTplIDRedisKeyPrefix = "image:small:"

const SmallTrafficUserWhiteListRedisKey = "sandbox:small:traffic:user:whitelist"

// DefaultOpenClawSkillsRedisKey OpenClaw Skills 配置的 Redis Key
const DefaultOpenClawSkillsRedisKey = "openclaw:default:skills"

// DefaultOpenClawSkills MoltBot 类型默认的 OpenClaw Skills 列表（作为兜底值）
const DefaultOpenClawSkills = "ducc-skill,wechat-article-to-markdown,baidu-neisou-deepresearch,app-builder,so-send-message," +
	"baidu-exchange-skill,ku-doc-manage,baidu-baike,baidu-map-api,baidu-scholar-search,baidu-search," +
	"icafe_card_assistant,xiaohongshu,clawdbot-logs,planning-with-files,frontend-design,skill-creator,daily-hot-news"
const CfsMigrateWhiteListRedisKey = "sandbox:small:traffic:user:cfs-migrate-whitelist"
const CfsMigratePercentageKey = "sandbox:cfs:migrate:percentage"
const CfsNewMigrateWhiteListRedisKey = "sandbox:cfs:new:migrate:user:whitelist"
const CfsNewMigratePercentageKey = "sandbox:cfs:new:migrate:percentage"
const OpenClawGWTokenWhiteListRedisKey = "sandbox:openclawgw:token:user:whitelist"
const OpenClawGWTokenAllUsersRedisKey = "sandbox:openclawgw:token:all-users"

const ScheduledTaskExecutorLockKey = "sandbox:scheduled:task:executor:lock"

const NetworkTypeVpc = "vpc"
const NginxEndpointRedisKey = "nginx:endpoint:"

// NoIdleReleaseWhiteListRedisKey 非休眠白名单：在此名单中的用户创建的 sandbox 不会被空闲释放
const NoIdleReleaseWhiteListRedisKey = "sandbox:no-idle-release:whitelist"

// RedirectCheckRedisKey 重定向和小流量检查的 Redis Key
// 格式: {"123": ["user1", "user2"], "456": ["user3"], "all": ["all"]}
// key 为 appId (字符串格式)，特殊值 "all" 表示全流量
// value 为 username 列表，特殊值 "all" 表示该 appId 下所有用户都是小流量
const RedirectCheckRedisKey = "sandbox:redirect:check"

// OpenClawAppID openClaw助手所用的空间
const OpenClawAppID = 2071

// NotificationMessagesRedisKey 沙箱启动通知消息的
const NotificationMessagesRedisKey = "sandbox:notification:messages"

// TriggerTypeManual 手动触发类型
const TriggerTypeManual = "manual"

// TriggerTypeScheduled 定时任务触发类型
const TriggerTypeScheduled = "scheduled"

// 沙箱恢复校验失败原因常量
const (
	// RestoreValidationFailureReasonStatusConflict 状态冲突：沙箱处于 running 或 pending 状态
	RestoreValidationFailureReasonStatusConflict = "status_conflict"

	// RestoreValidationFailureReasonNoPVC 未开启PVC持久化
	RestoreValidationFailureReasonNoPVC = "no_pvc"

	// RestoreValidationFailureReasonClusterNotFound 集群不存在
	RestoreValidationFailureReasonClusterNotFound = "cluster_not_found"

	// RestoreValidationFailureReasonStorageMigrationInProgress 存储迁移进行中
	RestoreValidationFailureReasonStorageMigrationInProgress = "storage_migration_in_progress"

	// RestoreValidationFailureReasonPVCBindingFailed PVC绑定失败
	RestoreValidationFailureReasonPVCBindingFailed = "pvc_binding_failed"

	// RestoreValidationFailureReasonPVCConfigQueryFailed 应用PVC配置查询失败
	RestoreValidationFailureReasonPVCConfigQueryFailed = "pvc_config_query_failed"
)

// DispatchAPIKey ClawGuard-Dispatch 服务的 API 密钥（用于查询 auto_wake 状态）
const DispatchAPIKey = "75i1o27472uf3i1o2745ie63eqs9"

// DispatchServiceURL ClawGuard-Dispatch 服务地址
const DispatchServiceURL = "https://sandbox-clawguard-api.baidu-int.com"

const AdminToken = "b9f5d3e7-2a1c-4b8e-9c3f-d7a6e1b2c5f8"

// RedirectUUAPClusters 支持多个集群名称判断
var RedirectUUAPClusters = []string{
	"agent-cce-prod",
	"agent-sandbox2-prod",
	"agent-sandbox3-prod",
	"agent-sandbox2",
}

// IsRedirectUUAPCluster 辅助函数：判断集群是否在列表中
func IsRedirectUUAPCluster(clusterName string) bool {
	for _, c := range RedirectUUAPClusters {
		if c == clusterName {
			return true
		}
	}
	return false
}

const CommonOpenclawBusinessID = "59b7ba3eded604ff"
