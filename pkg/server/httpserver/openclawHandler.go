package httpserver

import (
	"regexp"
)

// isValidDockerImage 校验 Docker 镜像地址格式
// 格式示例: iregistry.baidu-int.com/magellan-public/openclaw:20260325_1774431942037
func isValidDockerImage(image string) bool {
	if image == "" {
		return false
	}

	// Docker 镜像格式: [registry/][namespace/]repository[:tag]
	// 基本校验:
	// 1. 必须至少包含一个 / (registry/repository 或 namespace/repository)
	// 2. 不能以 / 或 : 开头或结尾
	// 3. :tag 部分可选，如果存在则 tag 不能为空
	// 4. 支持域名、端口、路径等

	if image[0] == '/' || image[0] == ':' || image[len(image)-1] == '/' || image[len(image)-1] == ':' {
		return false
	}

	// 校验至少包含一个 /
	if !regexp.MustCompile(`/`).MatchString(image) {
		return false
	}

	// 校验整体格式 (简化版，避免过度限制)
	// 允许字母、数字、点、横线、下划线、斜杠、冒号、数字、端口号
	pattern := `^[a-zA-Z0-9\.\-\_:/]+$`
	matched, err := regexp.MatchString(pattern, image)
	if err != nil || !matched {
		return false
	}

	// 校验 tag 部分不能为空 (如果存在 :tag)
	lastColon := -1
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == '/' {
			break
		}
		if image[i] == ':' {
			lastColon = i
			break
		}
	}
	if lastColon != -1 && lastColon == len(image)-1 {
		return false
	}

	return true
}
