package instance

import (
	"context"
	"fmt"
)

// ExampleUsage 展示如何使用 SkillInstaller
// 这是一个示例函数，实际使用时需要根据业务场景调整
func ExampleUsage() {
	// 注意：实际使用时需要引入正确的包并初始化数据库连接
	// import (
	//     "icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	//     "icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	// )

	// 1. 初始化 SkillInstaller（实际使用时需要传入真实的 dao.Query）
	// installer := NewSkillInstaller(dao.Use(resource.OrmMain))

	// 2. 准备安装参数
	params := InstallSkillParams{
		SandboxUUID: "y1d79v9s",
		DownloadURL: "https://bj.bcebos.com/baidu-cc/skills/iapi-manager/1.0.0/iapi-manager.zip",
		SkillName:   "iapi-manager",
	}

	// 3. 执行安装
	// ctx := context.Background()
	// err := installer.InstallSkill(ctx, params)
	// if err != nil {
	//     fmt.Printf("安装 skill 失败: %v\n", err)
	//     return
	// }

	fmt.Printf("将安装 skill: %s 到沙箱: %s\n", params.SkillName, params.SandboxUUID)
	fmt.Println("安装成功！")
}

// BatchInstallSkills 批量安装多个 skills 的示例
func BatchInstallSkills(installer *SkillInstaller, sandboxUUID string, skills []struct {
	Name string
	URL  string
}) error {
	ctx := context.Background()

	for _, skill := range skills {
		params := InstallSkillParams{
			SandboxUUID: sandboxUUID,
			DownloadURL: skill.URL,
			SkillName:   skill.Name,
		}

		fmt.Printf("正在安装 skill: %s...\n", skill.Name)
		if err := installer.InstallSkill(ctx, params); err != nil {
			return fmt.Errorf("安装 skill %s 失败: %w", skill.Name, err)
		}
		fmt.Printf("skill %s 安装成功\n", skill.Name)
	}

	return nil
}

// ExampleGatewayChecker 展示如何使用 GatewayChecker 检测 openclaw-gateway
func ExampleGatewayChecker() {
	// 注意：实际使用时需要传入真实的 dao.Query
	// checker := NewGatewayChecker(dao.Use(resource.OrmMain))

	// 准备参数
	params := CheckGatewayParams{
		SandboxUUID: "y1d79v9s",
	}

	// 方式一：仅检测进程（ps -aux | grep openclaw-gateway | grep -v grep）
	// processResult, err := checker.CheckGatewayByProcess(context.Background(), params)
	// if err != nil {
	// 	fmt.Printf("进程检测失败: %v\n", err)
	// 	return
	// }
	// fmt.Printf("进程检测: 运行中=%v\n详情: %s\n", processResult.IsRunning, processResult.Details)

	// 方式二：仅检测端口（检测 8081 端口是否监听）
	// portResult, err := checker.CheckGatewayByPort(context.Background(), params)
	// if err != nil {
	// 	fmt.Printf("端口检测失败: %v\n", err)
	// 	return
	// }
	// fmt.Printf("端口检测: 运行中=%v\n详情: %s\n", portResult.IsRunning, portResult.Details)

	// 方式三：综合检测（推荐）- 同时检测进程和端口
	// result, err := checker.CheckGateway(context.Background(), params)
	// if err != nil {
	// 	fmt.Printf("综合检测失败: %v\n", err)
	// 	return
	// }
	//
	// if result.IsRunning {
	// 	fmt.Println("✅ openclaw-gateway 正常运行")
	// 	fmt.Printf("   进程存在: %v\n", result.ProcessExists)
	// 	fmt.Printf("   端口监听: %v\n", result.PortListening)
	// } else {
	// 	fmt.Println("❌ openclaw-gateway 未正常运行")
	// 	if !result.ProcessExists {
	// 		fmt.Println("   进程不存在")
	// 	}
	// 	if !result.PortListening {
	// 		fmt.Println("   端口 8081 未监听")
	// 	}
	// }

	fmt.Printf("将检测沙箱 %s 中的 openclaw-gateway 状态\n", params.SandboxUUID)
}
