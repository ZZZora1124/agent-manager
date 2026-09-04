# Python 脚本嵌入说明

## 改进概述

将 Python 配置管理脚本从硬编码改为使用 `go:embed` 从文件读取，便于维护和更新。

## 实现方式

### 1. 使用 Go 1.16+ 的 embed 指令

```go
import (
    _ "embed"
)

//go:embed config_manager.py
var configManagerPythonScript string

func getConfigManagerPythonScript() string {
    return configManagerPythonScript
}
```

### 2. 文件位置

由于 `go:embed` 的限制（不能嵌入上级目录的文件），我们采用以下方案：

```
project/
├── scripts/conf_manager/
│   └── config_manager.py          # 原始脚本（主版本）
└── pkg/library/k8s/
    └── config_manager.py          # 副本（用于 embed）
```

**重要：** `pkg/library/k8s/config_manager.py` 是从 `scripts/conf_manager/config_manager.py` 复制而来。

## 文件同步

修改 Python 脚本时，需要同步两个文件：

### 方式 1：手动复制

```bash
cp scripts/conf_manager/config_manager.py pkg/library/k8s/config_manager.py
```

### 方式 2：使用脚本（推荐）

创建 `scripts/sync_config_manager.sh`：

```bash
#!/bin/bash
# 同步 config_manager.py 到 pkg/library/k8s/

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE="${SCRIPT_DIR}/conf_manager/config_manager.py"
TARGET="${SCRIPT_DIR}/../pkg/library/k8s/config_manager.py"

if [ ! -f "$SOURCE" ]; then
    echo "❌ 源文件不存在: $SOURCE"
    exit 1
fi

cp "$SOURCE" "$TARGET"
echo "✅ 已同步: config_manager.py"
echo "   源: $SOURCE"
    echo "   目标: $TARGET"
```

使用：
```bash
./scripts/sync_config_manager.sh
```

### 方式 3：Git Hook（自动化）

创建 `.git/hooks/pre-commit`：

```bash
#!/bin/bash
# 自动同步 config_manager.py

SOURCE="scripts/conf_manager/config_manager.py"
TARGET="pkg/library/k8s/config_manager.py"

if [ -f "$SOURCE" ]; then
    if ! cmp -s "$SOURCE" "$TARGET"; then
        echo "🔄 同步 config_manager.py..."
        cp "$SOURCE" "$TARGET"
        git add "$TARGET"
        echo "✅ 已同步并添加到暂存区"
    fi
fi
```

## 为什么不使用符号链接？

尝试过使用符号链接：
```bash
ln -s ../../../scripts/conf_manager/config_manager.py pkg/library/k8s/config_manager.py
```

但 `go:embed` 不支持嵌入符号链接文件，会报错：
```
pattern config_manager.py: cannot embed irregular file config_manager.py
```

## 为什么不使用相对路径？

`go:embed` 有路径限制：
- ✅ 支持：当前目录及子目录
- ❌ 不支持：上级目录（`..`）

尝试使用相对路径：
```go
//go:embed ../../../scripts/conf_manager/config_manager.py  // ❌ 失败
```

报错：
```
pattern ../../../scripts/conf_manager/config_manager.py: invalid pattern syntax
```

## 优势

### 改进前：硬编码

```go
func getConfigManagerPythonScript() string {
    return `#!/usr/bin/env python3
"""OpenClaw/ClawdBot 配置文件管理器"""
import json
import sys
...
    # 150+ 行硬编码的 Python 代码
`
}
```

**问题：**
- ❌ Python 代码难以维护
- ❌ 没有语法高亮
- ❌ IDE 无法提供 Python 支持
- ❌ 需要转义特殊字符
- ❌ 修改 Python 代码需要重新编译 Go

### 改进后：go:embed

```go
//go:embed config_manager.py
var configManagerPythonScript string

func getConfigManagerPythonScript() string {
    return configManagerPythonScript
}
```

**优势：**
- ✅ Python 代码独立存在，易于维护
- ✅ IDE 提供完整的 Python 支持
- ✅ 可以单独测试 Python 脚本
- ✅ 编译时嵌入，运行时无文件依赖
- ✅ 性能好，无运行时 I/O

## 开发流程

### 修改 Python 脚本

1. 编辑 `scripts/conf_manager/config_manager.py`
2. 测试脚本功能：
   ```bash
   cd scripts/conf_manager/
   python3 test_config_manager.py
   ```
3. 同步到 Go 嵌入位置：
   ```bash
   cp scripts/conf_manager/config_manager.py pkg/library/k8s/
   ```
4. 重新编译 Go 程序：
   ```bash
   go build
   ```

### 验证嵌入内容

```bash
# 编译后，Python 脚本已经嵌入到 Go 二进制文件中
# 运行时不需要外部的 .py 文件
./your-program
```

## 部署注意事项

### 源码部署

提交代码时，两个文件都需要提交：
```bash
git add scripts/conf_manager/config_manager.py
git add pkg/library/k8s/config_manager.py
git commit -m "Update config manager script"
```

### 二进制部署

编译后的二进制文件已经包含 Python 脚本，无需额外文件：
```bash
go build -o agent-runtime-manager
# agent-runtime-manager 已经包含 Python 脚本
```

## 文件版本一致性检查

创建检查脚本 `scripts/check_sync.sh`：

```bash
#!/bin/bash
SOURCE="scripts/conf_manager/config_manager.py"
TARGET="pkg/library/k8s/config_manager.py"

if ! cmp -s "$SOURCE" "$TARGET"; then
    echo "❌ 文件不同步！"
    echo "   运行: cp $SOURCE $TARGET"
    exit 1
else
    echo "✅ 文件已同步"
    exit 0
fi
```

在 CI/CD 中使用：
```yaml
# .github/workflows/ci.yml
- name: Check config_manager.py sync
  run: ./scripts/check_sync.sh
```

## 替代方案对比

| 方案 | 优点 | 缺点 | 推荐 |
|------|------|------|------|
| **硬编码** | 简单 | 难维护 | ❌ |
| **go:embed** | 编译时嵌入，性能好 | 需要文件同步 | ✅ |
| **os.ReadFile** | 运行时读取，灵活 | 依赖外部文件，性能差 | ⭐⭐⭐ |
| **http 下载** | 可动态更新 | 网络依赖，不可靠 | ⭐⭐ |

## 总结

使用 `go:embed` 嵌入 Python 脚本是最佳方案：
- ✅ 保持 Python 代码的可维护性
- ✅ 编译时嵌入，运行时无依赖
- ✅ 性能好，无运行时 I/O
- ⚠️ 需要注意文件同步（可自动化）

