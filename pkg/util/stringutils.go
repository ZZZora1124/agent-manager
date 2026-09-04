package util

import (
	"encoding/json"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/env"
	"math/rand"
	"regexp"
	"strings"
	"time"
)

const letters = "abcdefghijklmnopqrstuvwxyz"
const digits = "0123456789"

func GenerateRandomString(length int) string {
	if length < 1 {
		return ""
	}

	rand.Seed(time.Now().UnixNano()) // 使用当前时间作为种子
	result := make([]byte, length)

	// 确保第一个字符是字母
	result[0] = letters[rand.Intn(len(letters))]

	// 为剩余的字符随机选择字母或数字
	for i := 1; i < length; i++ {
		if rand.Intn(2) == 0 {
			result[i] = letters[rand.Intn(len(letters))]
		} else {
			result[i] = digits[rand.Intn(len(digits))]
		}
	}
	return string(result)
}

// GenerateSandboxID 生成随机字符串
func GenerateSandboxID(length int) string {
	result := GenerateRandomString(length)
	if env.RunMode() == constant.TestRunMode {
		return string(result) + constant.TestUuidSuffix
	}
	return result
}

// GenerateUniqueSandboxID 生成不与已有记录冲突的 sandbox ID。
// exists 回调返回 true 表示该 ID 已存在，最多重试 3 次。
// 数据库唯一索引是最终兜底，即使极端情况下重试耗尽，插入失败也会被上层捕获。
func GenerateUniqueSandboxID(length int, exists func(string) bool) string {
	for i := 0; i < 3; i++ {
		id := GenerateSandboxID(length)
		if !exists(id) {
			return id
		}
	}
	return GenerateSandboxID(length)
}

func ValidImageAddr(addr string) bool {
	var (
		domainRe = regexp.MustCompile(`^iregistry\.baidu-int\.com$`)
		segRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)
		tagRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	)
	// 1. 切成域名，路径和tag
	domain, rest, found := strings.Cut(addr, "/")
	if !found {
		return false
	}
	if !domainRe.MatchString(domain) {
		return false
	}

	// 2. 切成路径和 tag
	path, tag, ok := strings.Cut(rest, ":")
	if !ok || path == "" || tag == "" {
		return false
	}
	if !tagRe.MatchString(tag) {
		return false
	}

	// 3. 校验
	for _, s := range strings.Split(path, "/") {
		if !segRe.MatchString(s) {
			return false
		}
	}
	return true
}

func GetReadProbPort(tplType string) []int32 {
	if tplType == "MoltBot" {
		return []int32{8080}
	} else if tplType == "All-in-one" {
		return []int32{8080}
	} else {
		return []int32{}
	}
}

// UnmarshalJSON 解析JSON字符串到指定类型
func UnmarshalJSON(jsonStr string, v interface{}) error {
	if jsonStr == "" {
		return nil
	}
	return json.Unmarshal([]byte(jsonStr), v)
}
