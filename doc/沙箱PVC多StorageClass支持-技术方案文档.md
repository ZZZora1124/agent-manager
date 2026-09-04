# 沙箱 PVC 多 StorageClass 支持 -- 技术方案文档

> **文档版本**: v3.0 (2026-02-28)
> **状态**: 已实现
> **适用范围**: Agent Runtime Manager — 支持 `local-path`、`hp1`、`sharedcfs`、`csi-bos2` 四种存储类

---

## 1. 背景与目标

### 1.1 问题背景

现有沙箱 PVC 持久化方案需要支持多种存储后端以满足不同场景需求：

| 存储类 | 简称 | 供给方式 | 场景 |
|--------|------|----------|------|
| `local-path` | 本地盘 | 动态 | 开发/测试（默认） |
| `hp1` | CDS 云磁盘 | 动态 | 生产、高数据可靠性 |
| `sharedcfs` | CFS 共享文件存储 | 动态 | 跨节点共享、openClaw 持久化 |
| `csi-bos2` | BOS 对象存储 | **静态**（PV + PVC） | 大容量、数据保留 |

### 1.2 目标

1. 用户创建沙箱时可选择存储类：`local-path`（默认）、`hp1`、`sharedcfs` 或 `csi-bos2`
2. 存储类选择持久化到数据库，恢复操作自动复用原始存储类
3. 新 API 和 Legacy API 均支持该参数
4. 完全向后兼容，不传参数时行为与改动前一致
5. CFS/BOS 跨节点天然可达，不需要 SSH 数据迁移

---

## 2. 存储类对比

| 特性 | `local-path` | `hp1` (CDS) | `sharedcfs` (CFS) | `csi-bos2` (BOS) |
|------|-------------|-------------|-------------------|------------------|
| 存储类型 | 本地磁盘 | 云磁盘 | 共享文件存储 | 对象存储 |
| 供给方式 | 动态 | 动态 | 动态 | **静态**（PV + PVC） |
| 访问模式 | ReadWriteOnce | ReadWriteOnce | **ReadWriteMany** | ReadWriteOnce |
| 跨节点访问 | 不支持 | 支持 | **支持** | **支持** |
| 数据可靠性 | 取决于本地磁盘 | 三副本，高可靠 | 高可靠 | 极高可靠 |
| 是否需要迁移 | 需要 | 需要 | **不需要** | **不需要** |
| PV 回收策略 | Delete | Delete | Delete | **Retain**（数据保留） |
| 适用场景 | 开发/测试 | 生产 | 跨节点共享 | 大容量持久化 |

---

## 3. 设计方案

### 3.1 核心思路

- **动态供给**（local-path / hp1 / sharedcfs）：`CreateDynamicPVC` 或 `CreateDynamicPVCWithAccessMode` 创建 PVC，K8s 自动创建 PV
- **静态供给**（csi-bos2）：`CreateBOSPVAndPVC` 手动创建 PV + PVC
- **CFS 区别**：使用 `ReadWriteMany` 访问模式（其他动态供给使用 `ReadWriteOnce`）
- **用户入口**：请求模型的 `storageClass` 参数
- **持久化**：`sandbox_pvc_binding` 表的 `storage_class` 列
- **透传**：`EnsureSandboxPVCBinding()` 内部根据 storageClass switch 分支
- **校验**：白名单验证，仅允许四种合法值
- **迁移防护**：CFS 和 BOS 都是跨节点可达，拒绝迁移请求

### 3.2 local-path / hp1 PVC 规格（动态供给，ReadWriteOnce）

```yaml
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: sandbox-{uuid}-pvc
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: hp1
  resources:
    requests:
      storage: 20Gi
```

### 3.3 sharedcfs PVC 规格（动态供给，ReadWriteMany）

```yaml
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: sandbox-{uuid}-pvc
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: sharedcfs
  resources:
    requests:
      storage: 20Gi
```

### 3.4 csi-bos2 PV + PVC 规格（静态供给）

**PV**:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: sandbox-{uuid}-pv
spec:
  accessModes:
    - ReadWriteOnce
  capacity:
    storage: 20Gi
  persistentVolumeReclaimPolicy: Retain
  storageClassName: csi-bos2
  mountOptions:
    - "-o bos.fs.meta.expires=60"
    - "-o allow_other"
  csi:
    driver: csi-bos2plugin
    volumeHandle: agentsandbox/sandbox-{uuid}
    nodePublishSecretRef:
      name: csi-bos-secret
      namespace: default
```

**PVC**:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: sandbox-{uuid}-pvc
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: csi-bos2
  volumeName: sandbox-{uuid}-pv
  resources:
    requests:
      storage: 20Gi
```

**关键设计**：

- **共享 Bucket + 子目录隔离**：所有沙箱共享 `agentsandbox` bucket，每个沙箱挂载 `sandbox-{uuid}` 子目录
- **ReclaimPolicy = Retain**：PV 删除后 BOS 数据保留，支持后续恢复
- **Secret 前置**：`csi-bos-secret`（含 AK/SK）需提前在集群中创建

---

## 4. 改动范围

### 4.1 数据库迁移

**文件**: `scripts/sql/init.sql`

```sql
ALTER TABLE sandbox_pvc_binding
    ADD COLUMN `storage_class` VARCHAR(64) NOT NULL DEFAULT 'local-path' COMMENT '存储类: local-path, hp1, sharedcfs, csi-bos2';
```

### 4.2 数据模型

**文件**: `pkg/model/sandbox_pvc_binding.go`

```go
StorageClass string `gorm:"column:storage_class;not null;default:local-path" json:"storage_class"`
```

### 4.3 K8s 层

**文件**: `pkg/library/k8s/createpod.go`

| 改动项 | 说明 |
|--------|------|
| `CreateDynamicPVCWithAccessMode` | 新增方法，支持自定义访问模式（CFS 用 ReadWriteMany） |
| `CreateDynamicPVC` | 内部调用 `CreateDynamicPVCWithAccessMode`，默认 `ReadWriteOnce` |
| BOS 静态供给代码 | `CreateBOSPVAndPVC`、`BuildBOSPVName`、`DefaultBOSMountOptions`、BOS 常量（保留） |

### 4.4 服务层

**文件**: `pkg/service/sandbox_pvc_binding.go`

| 改动项 | 说明 |
|--------|------|
| 四种常量 | `StorageClassLocalPath` / `StorageClassHP1` / `StorageClassCFS` / `StorageClassBOS` |
| `allowedStorageClasses` 白名单 | 四种存储类 |
| `IsCFSStorageClass` / `IsBOSStorageClass` / `IsSharedStorageClass` | 类型判断函数 |
| `ValidateSandboxStorageClass` | 白名单校验 |
| `EnsureSandboxPVCBinding` | switch 分支：BOS→静态供给，CFS→动态(RWX)，其他→动态(RWO) |

**分支逻辑**:

```go
switch {
case IsBOSStorageClass(storageClass):
    // BOS 静态供给：创建 PV + PVC
    pvName := k8s.BuildBOSPVName(sandboxUUID)
    subDir := fmt.Sprintf("sandbox-%s", sandboxUUID)
    err = cluster.CreateBOSPVAndPVC(ctx, pvName, pvcName, k8s.BOSSandboxBucket, subDir, pvcSize)
case IsCFSStorageClass(storageClass):
    // CFS 动态供给：ReadWriteMany
    err = cluster.CreateDynamicPVCWithAccessMode(ctx, pvcName, storageClass, pvcSize, corev1.ReadWriteMany)
default:
    // local-path / hp1 动态供给：ReadWriteOnce
    err = cluster.CreateDynamicPVC(ctx, pvcName, storageClass, pvcSize)
}
```

### 4.5 迁移防护

**文件**: `pkg/service/sandbox_pvc_migration.go`

CFS 和 BOS 跨节点天然可达，不需要 SSH 数据迁移：

```go
if IsSharedStorageClass(binding.StorageClass) {
    return nil, fmt.Errorf("shared storage (%s) does not require cross-node migration", binding.StorageClass)
}
```

### 4.6 无需改动

| 文件 | 原因 |
|------|------|
| `pkg/model/sandbox_pvc_binding.go` | `StorageClass` 字段类型不变 |
| `pkg/server/httpserver/handler.go` | 透传 storageClass 值 |
| `pkg/library/background/pvc_cleaner.go` | 通过 `GetPVNameByPVC` → `DeletePV` 兼容所有类型 |
| `pkg/library/bos/` | Artifact BOS 不受影响 |

---

## 5. API 使用方式

### 5.1 新 API

```http
POST /rest/v1/sandbox/instances
Content-Type: application/json

{
  "appId": 123,
  "templateId": 456,
  "usePVC": true,
  "pvcSize": 50,
  "storageClass": "sharedcfs"
}
```

### 5.2 Legacy API

```http
POST /sandboxes
Content-Type: application/json

{
  "sandboxID": "my-sandbox",
  "templateID": "python-agent",
  "metadata": {
    "agent-sandbox/usePVC": "true",
    "agent-sandbox/pvcSize": "50",
    "agent-sandbox/storageClass": "sharedcfs"
  }
}
```

### 5.3 参数说明

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `storageClass` | string | 否 | `"local-path"` | 可选 `local-path`、`hp1`、`sharedcfs`、`csi-bos2` |

### 5.4 响应示例（节选）

```json
{
  "usePVC": true,
  "pvcReclaimTime": null,
  "storageClass": "sharedcfs"
}
```

---

## 6. 释放与恢复生命周期

### 6.1 完整时间轴（以 CFS 为例）

```
T0  创建沙箱（storageClass=sharedcfs）
    ├── CreateDynamicPVCWithAccessMode: 创建 PVC（ReadWriteMany）
    ├── binding.status = active
    └── K8s 自动创建 PV 并绑定

T1  释放沙箱（ReleaseSandboxRuntime）
    ├── sandbox_runtime.status → kill
    ├── ReleaseSandboxPVCBinding: binding.status → kill
    ├── binding.reclaim_after = T1 + 15 天
    └── PV/PVC 仍在 K8s 中，数据完整

T2  恢复沙箱（T1 后 15 天内）
    ├── GetSandboxPVCBinding 找到 binding（deleted=0）
    ├── EnsureSandboxPVCBinding 直接复用
    └── Pod 挂载原 PVC → 数据完整恢复

T3  15 天后 PVC Cleaner 触发（若未恢复）
    ├── DeletePVC + DeletePV
    └── binding.deleted = true
```

### 6.2 BOS 特殊行为

BOS 使用 `ReclaimPolicy=Retain`，PV 删除后 BOS bucket 数据保留。超期恢复时可重建 PV/PVC 指向同一子目录恢复数据。

### 6.3 恢复行为汇总

| 场景 | 行为 | 数据 |
|------|------|------|
| binding 存在（15 天内） | 直接复用现有 binding 和 PVC | 完整 |
| binding 已清理（15 天后，CFS/local-path/hp1） | 从历史 binding 读取 storageClass，重建 PVC | 新建 |
| binding 已清理（15 天后，BOS） | 重建 PV/PVC 指向同一子目录 | **完整**（BOS Retain） |

---

## 7. 验证方式

1. `go build ./...` — 编译通过
2. 创建沙箱 `storageClass: "sharedcfs"` → PVC(ReadWriteMany) 自动创建
3. 创建沙箱 `storageClass: "csi-bos2"` → PV + PVC 创建成功
4. 创建沙箱 `storageClass: "hp1"` → PVC(ReadWriteOnce) 自动创建
5. 不传 storageClass → 默认 `local-path`（向后兼容）
6. 传无效值 → 返回 400
7. 迁移接口对 CFS/BOS 类型返回错误
8. Pod 挂载 `/workspace` 可读写

---

## 8. 风险与注意事项

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 集群未安装对应 StorageClass | PVC 停留 Pending | K8s 原生行为 |
| DDL 未先于代码部署 | GORM Create 失败 | 部署流程保证 |
| BOS CSI 插件 v0.0.4 不自动创建子目录 | Pod CrashLoop | 建议优先使用 CFS；BOS 保留给需要数据永久保留的场景 |
| BOS bucket 数据不自动清理 | 存储成本累积 | BOS 成本低，后续可扩展 Cleaner |
| BOS Secret `csi-bos-secret` 不存在 | Pod 挂载失败 | 需提前在集群中创建 |

---

## 附录：修改文件索引

| 文件路径 | 改动类型 |
|----------|----------|
| `pkg/service/sandbox_pvc_binding.go` | 四种常量 + `IsCFSStorageClass`/`IsBOSStorageClass`/`IsSharedStorageClass` + switch 分支 |
| `pkg/library/k8s/createpod.go` | `CreateDynamicPVCWithAccessMode` + BOS 静态供给代码（保留） |
| `pkg/service/sandbox_pvc_migration.go` | `IsSharedStorageClass` 拒绝迁移 |
| `pkg/service/sandboxRuntime.go` | `StorageClass` 字段反映实际值 |
| `scripts/sql/init.sql` | DDL COMMENT 含四种存储类 |



