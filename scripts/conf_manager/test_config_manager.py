#!/usr/bin/env python3
"""
config_manager.py 测试脚本

用法：
    # 测试替换操作（会备份原文件）
    python3 test_config_manager.py

    # 使用自定义配置文件
    python3 test_config_manager.py custom_config.json

    # 恢复备份文件
    python3 test_config_manager.py --restore
"""

import os
import sys
import json
import shutil
import subprocess
from pathlib import Path
from datetime import datetime


class Colors:
    """终端颜色"""
    HEADER = '\033[95m'
    BLUE = '\033[94m'
    CYAN = '\033[96m'
    GREEN = '\033[92m'
    YELLOW = '\033[93m'
    RED = '\033[91m'
    ENDC = '\033[0m'
    BOLD = '\033[1m'


def print_header(text):
    """打印标题"""
    print(f"\n{Colors.BOLD}{Colors.HEADER}{'=' * 60}{Colors.ENDC}")
    print(f"{Colors.BOLD}{Colors.HEADER}{text.center(60)}{Colors.ENDC}")
    print(f"{Colors.BOLD}{Colors.HEADER}{'=' * 60}{Colors.ENDC}\n")


def print_step(step_num, text):
    """打印步骤"""
    print(f"{Colors.BOLD}{Colors.CYAN}[步骤 {step_num}]{Colors.ENDC} {text}")


def print_success(text):
    """打印成功信息"""
    print(f"{Colors.GREEN}✅ {text}{Colors.ENDC}")


def print_warning(text):
    """打印警告信息"""
    print(f"{Colors.YELLOW}⚠️  {text}{Colors.ENDC}")


def print_error(text):
    """打印错误信息"""
    print(f"{Colors.RED}❌ {text}{Colors.ENDC}")


def print_info(text):
    """打印信息"""
    print(f"{Colors.BLUE}ℹ️  {text}{Colors.ENDC}")


def run_command(cmd):
    """执行命令"""
    result = subprocess.run(
        cmd,
        shell=True,
        capture_output=True,
        text=True
    )
    return result.returncode, result.stdout, result.stderr


def show_json_diff(label, json_data):
    """显示 JSON 内容"""
    print(f"\n{Colors.BOLD}{label}:{Colors.ENDC}")
    print(json.dumps(json_data, indent=2, ensure_ascii=False))


def backup_file(file_path):
    """备份文件"""
    if not file_path.exists():
        print_error(f"文件不存在: {file_path}")
        return None

    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    backup_path = file_path.parent / f"{file_path.name}.backup_{timestamp}"
    shutil.copy2(file_path, backup_path)
    print_success(f"已备份到: {backup_path}")
    return backup_path


def restore_backup(config_path):
    """恢复备份"""
    backup_files = sorted(config_path.parent.glob(f"{config_path.name}.backup_*"))
    if not backup_files:
        print_error("未找到备份文件")
        return False

    latest_backup = backup_files[-1]
    print_info(f"找到备份文件: {latest_backup}")

    confirm = input(f"确认恢复备份到 {config_path}？ (yes/no): ")
    if confirm.lower() == 'yes':
        shutil.copy2(latest_backup, config_path)
        print_success(f"已恢复: {config_path}")
        return True
    else:
        print_warning("取消恢复")
        return False


def test_config_manager(config_path):
    """测试配置管理器"""

    print_header("配置管理器测试")

    # 检查脚本是否存在
    script_path = Path(__file__).parent / "config_manager.py"
    if not script_path.exists():
        print_error(f"未找到 config_manager.py: {script_path}")
        return False

    # 检查配置文件是否存在
    if not config_path.exists():
        print_error(f"配置文件不存在: {config_path}")
        print_info("请将 PVC 中的配置文件放到当前目录，命名为 openclaw.json")
        return False

    print_info(f"配置文件: {config_path}")
    print_info(f"测试脚本: {script_path}")

    # 步骤 1: 读取原始配置
    print_step(1, "读取原始配置")
    try:
        with open(config_path, 'r', encoding='utf-8') as f:
            original_config = json.load(f)
        show_json_diff("原始配置", original_config)
        print_success("读取成功")
    except Exception as e:
        print_error(f"读取失败: {e}")
        return False

    # 备份文件
    backup_path = backup_file(config_path)
    if not backup_path:
        return False

    # 步骤 2: 检查是否有重复的 http 节点
    print_step(2, "检查重复的 key")
    with open(config_path, 'r') as f:
        content = f.read()
        http_count = content.count('"http"')
        if http_count > 1:
            print_warning(f"检测到 {http_count} 个 'http' key")
            print_info("Python json.load() 会自动只保留最后一个")
        else:
            print_success("未检测到重复的 key")

    # 步骤 3: 删除 chatCompletions 节点
    print_step(3, "删除 gateway.http.endpoints.chatCompletions")
    cmd = f"python3 {script_path} delete-field {config_path} 'gateway.http.endpoints.chatCompletions'"
    returncode, stdout, stderr = run_command(cmd)

    if returncode == 0:
        print_success("删除成功")
        if stdout:
            print(f"  输出: {stdout.strip()}")
    else:
        print_warning("删除失败（可能字段不存在）")
        if stderr:
            print(f"  错误: {stderr.strip()}")

    # 步骤 4: 添加 responses 节点
    print_step(4, "添加 gateway.http.endpoints.responses.enabled = true")
    cmd = f"python3 {script_path} set-field {config_path} 'gateway.http.endpoints.responses.enabled' 'true'"
    returncode, stdout, stderr = run_command(cmd)

    if returncode == 0:
        print_success("添加成功")
        if stdout:
            print(f"  输出: {stdout.strip()}")
    else:
        print_error("添加失败")
        if stderr:
            print(f"  错误: {stderr.strip()}")
        return False

    # 步骤 5: 更新 remote.url
    print_step(5, "更新 gateway.remote.url")
    sandbox_uuid = "test-abc123def456"
    new_url = f"https://openclaw-8081-{sandbox_uuid}.n.baidu-int.com"
    cmd = f"python3 {script_path} set-field {config_path} 'gateway.remote.url' '{new_url}'"
    returncode, stdout, stderr = run_command(cmd)

    if returncode == 0:
        print_success("更新成功")
        if stdout:
            print(f"  输出: {stdout.strip()}")
    else:
        print_warning("更新失败（可能字段不存在）")
        if stderr:
            print(f"  错误: {stderr.strip()}")

    # 步骤 6: 读取更新后的配置
    print_step(6, "读取更新后的配置")
    try:
        with open(config_path, 'r', encoding='utf-8') as f:
            updated_config = json.load(f)
        show_json_diff("更新后配置", updated_config)
        print_success("读取成功")
    except Exception as e:
        print_error(f"读取失败: {e}")
        return False

    # 步骤 7: 对比变化
    print_step(7, "对比配置变化")

    # 检查 chatCompletions 是否被删除
    has_chat_completions = False
    if 'gateway' in updated_config and 'http' in updated_config['gateway']:
        if 'endpoints' in updated_config['gateway']['http']:
            has_chat_completions = 'chatCompletions' in updated_config['gateway']['http']['endpoints']

    if has_chat_completions:
        print_error("❌ chatCompletions 未被删除")
    else:
        print_success("✅ chatCompletions 已删除")

    # 检查 responses 是否被添加
    has_responses = False
    if 'gateway' in updated_config and 'http' in updated_config['gateway']:
        if 'endpoints' in updated_config['gateway']['http']:
            if 'responses' in updated_config['gateway']['http']['endpoints']:
                has_responses = updated_config['gateway']['http']['endpoints']['responses'].get('enabled') is True

    if has_responses:
        print_success("✅ responses 已添加")
    else:
        print_error("❌ responses 未添加")

    # 检查 remote.url 是否被更新
    remote_url_updated = False
    if 'gateway' in updated_config and 'remote' in updated_config['gateway']:
        actual_url = updated_config['gateway']['remote'].get('url', '')
        remote_url_updated = actual_url == new_url

    if remote_url_updated:
        print_success(f"✅ remote.url 已更新为: {new_url}")
    else:
        print_warning(f"⚠️  remote.url 未更新或不匹配")
        if 'gateway' in updated_config and 'remote' in updated_config['gateway']:
            actual_url = updated_config['gateway']['remote'].get('url', 'N/A')
            print_info(f"   当前值: {actual_url}")

    # 检查其他字段是否保留
    print_step(8, "检查其他字段是否保留")

    # 检查自定义字段
    preserved_fields = []
    for key in original_config:
        if key in updated_config and key not in ['gateway']:
            preserved_fields.append(key)

    if preserved_fields:
        print_success(f"✅ 保留的顶级字段: {', '.join(preserved_fields)}")
    else:
        print_info("ℹ️  未检测到其他顶级字段")

    # 总结
    print_header("测试总结")

    all_passed = not has_chat_completions and has_responses and remote_url_updated

    if all_passed:
        print_success("🎉 所有测试通过！")
        print_info(f"原始配置已备份到: {backup_path}")
        print_info(f"更新后的配置: {config_path}")
    else:
        print_error("⚠️  部分测试未通过，请检查上述错误")

    # 询问是否恢复备份
    print("\n" + "=" * 60)
    restore = input("是否恢复原始配置？ (yes/no): ")
    if restore.lower() == 'yes':
        shutil.copy2(backup_path, config_path)
        print_success(f"已恢复原始配置: {config_path}")
    else:
        print_info("保留更新后的配置")
        print_info(f"如需恢复，请运行: python3 {Path(__file__).name} --restore")

    return all_passed


def main():
    """主函数"""

    # 检查参数
    if len(sys.argv) > 1:
        if sys.argv[1] == '--restore':
            # 恢复备份
            script_dir = Path(__file__).parent
            config_path = script_dir / "openclaw.json"
            restore_backup(config_path)
            return
        else:
            # 使用指定的配置文件
            config_path = Path(sys.argv[1])
    else:
        # 默认配置文件
        script_dir = Path(__file__).parent
        config_path = script_dir / "openclaw.json"

    # 运行测试
    success = test_config_manager(config_path)

    sys.exit(0 if success else 1)


if __name__ == '__main__':
    main()
