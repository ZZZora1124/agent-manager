#!/usr/bin/env python3
"""
OpenClaw/ClawdBot 配置文件管理器

功能：
- 创建/更新 JSON 配置文件
- 支持 merge/override/smart 三种更新模式
- 精确更新/删除特定字段

使用方法：
  1. 更新配置文件:
     python3 config_manager.py update <config_path> <base64_json> [mode]

  2. 更新特定字段:
     python3 config_manager.py set-field <config_path> <json_path> <value>

  3. 删除特定字段:
     python3 config_manager.py delete-field <config_path> <json_path>

模式说明：
  - merge: 合并模式（默认），递归合并配置，保留现有字段
  - override: 覆盖模式，完全替换配置
  - smart: 智能模式，只更新关键字段（models, auth, gateway）
"""

import json
import sys
import base64
from pathlib import Path


def merge_dict(base, update):
    """
    递归合并字典

    Args:
        base: 基础字典
        update: 更新字典

    Returns:
        合并后的字典（update 的值会覆盖 base）
    """
    for key, value in update.items():
        if key in base and isinstance(base[key], dict) and isinstance(value, dict):
            merge_dict(base[key], value)
        else:
            base[key] = value
    return base


def update_config(config_path, new_config_json, mode='merge'):
    """
    更新配置文件

    Args:
        config_path: 配置文件路径
        new_config_json: 新配置的 JSON 字符串
        mode: 更新模式 (merge/override/smart)

    Returns:
        bool: 是否成功
    """
    config_path = Path(config_path)
    config_path.parent.mkdir(parents=True, exist_ok=True)

    try:
        new_config = json.loads(new_config_json)
    except json.JSONDecodeError as e:
        print(f"[ERROR] 无效的JSON: {e}", file=sys.stderr)
        return False

    # 文件不存在，直接创建
    if not config_path.exists():
        print(f"[INFO] 创建新配置: {config_path}")
        with open(config_path, 'w', encoding='utf-8') as f:
            json.dump(new_config, f, indent=2, ensure_ascii=False)
        return True

    # 读取现有配置
    try:
        with open(config_path, 'r', encoding='utf-8') as f:
            content = f.read()
            existing_config = json.loads(content)
            # 检查是否有重复 key（简单检测）
            if content.count('"http"') > 1:
                print(f"[WARN] 检测到重复的 key，Python 会自动保留最后一个", file=sys.stderr)
    except Exception as e:
        print(f"[WARN] 读取失败，覆盖: {e}", file=sys.stderr)
        with open(config_path, 'w', encoding='utf-8') as f:
            json.dump(new_config, f, indent=2, ensure_ascii=False)
        return True

    # 根据模式处理
    if mode == 'override':
        final_config = new_config
    elif mode == 'merge':
        final_config = merge_dict(existing_config.copy(), new_config)
    elif mode == 'smart':
        # 智能模式：关键字段更新，其他保留
        final_config = existing_config.copy()
        for key in ['models', 'auth', 'gateway']:
            if key in new_config:
                if key in final_config and isinstance(final_config[key], dict):
                    merge_dict(final_config[key], new_config[key])
                else:
                    final_config[key] = new_config[key]
    else:
        print(f"[ERROR] 未知模式: {mode}", file=sys.stderr)
        return False

    with open(config_path, 'w', encoding='utf-8') as f:
        json.dump(final_config, f, indent=2, ensure_ascii=False)
    print(f"[SUCCESS] 配置已更新: {config_path}")
    return True


def update_field(config_path, json_path, value):
    """
    更新特定字段

    Args:
        config_path: 配置文件路径
        json_path: JSON 路径，如 'gateway.remote.url'
        value: 新值

    Returns:
        bool: 是否成功
    """
    config_path = Path(config_path)
    if not config_path.exists():
        print(f"[ERROR] 文件不存在: {config_path}", file=sys.stderr)
        return False

    try:
        with open(config_path, 'r', encoding='utf-8') as f:
            config = json.load(f)
    except Exception as e:
        print(f"[ERROR] 读取失败: {e}", file=sys.stderr)
        return False

    keys = json_path.split('.')
    current = config
    for key in keys[:-1]:
        if key not in current:
            current[key] = {}
        current = current[key]
    current[keys[-1]] = value

    with open(config_path, 'w', encoding='utf-8') as f:
        json.dump(config, f, indent=2, ensure_ascii=False)
    print(f"[SUCCESS] 字段已更新: {json_path} = {value}")
    return True


def delete_field(config_path, json_path):
    """
    删除特定字段

    Args:
        config_path: 配置文件路径
        json_path: JSON 路径，如 'gateway.http.endpoints.chatCompletions'

    Returns:
        bool: 是否成功
    """
    config_path = Path(config_path)
    if not config_path.exists():
        print(f"[ERROR] 文件不存在: {config_path}", file=sys.stderr)
        return False

    try:
        with open(config_path, 'r', encoding='utf-8') as f:
            config = json.load(f)
    except Exception as e:
        print(f"[ERROR] 读取失败: {e}", file=sys.stderr)
        return False

    keys = json_path.split('.')
    current = config
    for key in keys[:-1]:
        if key not in current:
            print(f"[WARN] 路径不存在: {json_path}")
            return True
        current = current[key]

    if keys[-1] in current:
        del current[keys[-1]]
        print(f"[SUCCESS] 字段已删除: {json_path}")
    else:
        print(f"[WARN] 字段不存在: {json_path}")

    with open(config_path, 'w', encoding='utf-8') as f:
        json.dump(config, f, indent=2, ensure_ascii=False)
    return True


def main():
    """主函数：处理命令行参数"""
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)

    command = sys.argv[1]

    if command == 'update':
        if len(sys.argv) < 4:
            print("[ERROR] 参数不足", file=sys.stderr)
            print("用法: python3 config_manager.py update <config_path> <base64_json> [mode]")
            sys.exit(1)

        config_path = sys.argv[2]
        base64_json = sys.argv[3]
        mode = sys.argv[4] if len(sys.argv) > 4 else 'merge'

        try:
            json_str = base64.b64decode(base64_json).decode('utf-8')
        except Exception as e:
            print(f"[ERROR] Base64 解码失败: {e}", file=sys.stderr)
            sys.exit(1)

        success = update_config(config_path, json_str, mode)
        sys.exit(0 if success else 1)

    elif command == 'set-field':
        if len(sys.argv) < 5:
            print("[ERROR] 参数不足", file=sys.stderr)
            print("用法: python3 config_manager.py set-field <config_path> <json_path> <value>")
            sys.exit(1)

        config_path = sys.argv[2]
        json_path = sys.argv[3]
        value = sys.argv[4]

        # 尝试解析值的类型
        try:
            value = json.loads(value)
        except json.JSONDecodeError:
            # 解析失败就当字符串
            pass

        success = update_field(config_path, json_path, value)
        sys.exit(0 if success else 1)

    elif command == 'delete-field':
        if len(sys.argv) < 4:
            print("[ERROR] 参数不足", file=sys.stderr)
            print("用法: python3 config_manager.py delete-field <config_path> <json_path>")
            sys.exit(1)

        config_path = sys.argv[2]
        json_path = sys.argv[3]

        success = delete_field(config_path, json_path)
        sys.exit(0 if success else 1)

    else:
        print(f"[ERROR] 未知命令: {command}", file=sys.stderr)
        print(__doc__)
        sys.exit(1)


if __name__ == '__main__':
    main()
