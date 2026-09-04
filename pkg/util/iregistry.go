package util

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// iregistry API 地址
	iregistryBaseURL = "https://iregistry.baidu-int.com"
	// 镜像大小限制 10GB
	maxImageSize = int64(10 * 1024 * 1024 * 1024)
	// iregistry privatekey
	iregistryPrivateKey = "64fe9315-4ce4-4c8d-b570-2a9ca21635e9"
)

// ImageInfo 镜像解析信息
type ImageInfo struct {
	ProjectName    string // iregistry空间的名字
	RepositoryName string // 仓库名字（包含project）
	Tag            string // 镜像tag
}

// ParseImageURL 解析镜像URL，提取projectName、repositoryName和tag
// 镜像URL格式: iregistry.baidu-int.com/{projectName}/{repositoryName}:{tag}
// 例如: iregistry.baidu-int.com/acb-release/builder-base-arm64:1.0
func ParseImageURL(imageURL string) (*ImageInfo, error) {
	// 去掉域名
	prefix := "iregistry.baidu-int.com/"
	if !strings.HasPrefix(imageURL, prefix) {
		return nil, fmt.Errorf("镜像地址格式不正确，应以 %s 开头", prefix)
	}

	rest := strings.TrimPrefix(imageURL, prefix)

	// 分离 tag
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("镜像地址格式不正确，需要包含tag")
	}

	path := parts[0]
	tag := parts[1]

	// 分离 projectName 和 repositoryName
	// 格式: {projectName}/{repositoryName}
	pathParts := strings.SplitN(path, "/", 2)
	if len(pathParts) != 2 {
		return nil, fmt.Errorf("镜像地址格式不正确，路径需要包含project和repository")
	}

	return &ImageInfo{
		ProjectName:    pathParts[0],
		RepositoryName: path,
		Tag:            tag,
	}, nil
}

// GenerateAuthToken 生成鉴权Token
// 计算规则: MD5(username:privatekey:timestamp)
func GenerateAuthToken(username string) string {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	raw := fmt.Sprintf("%s:%s:%s", username, iregistryPrivateKey, timestamp)
	hash := md5.Sum([]byte(raw))
	return hex.EncodeToString(hash[:])
}

// RoleResponse 权限查询API返回结构
type RoleResponse struct {
	ProjectName   string `json:"projectName"`
	ProjectID     int    `json:"projectId"`
	Username      string `json:"username"`
	IsPublic      bool   `json:"isPublic"`
	HasPullAccess bool   `json:"hasPullAccess"` // 是否有pull权限
	IsSysAdmin    bool   `json:"isSysAdmin"`
}

// ImageSizeResponse 镜像大小查询API返回结构
type ImageSizeResponse struct {
	Digest        string `json:"digest"`
	Name          string `json:"name"`
	Size          int64  `json:"size"` // 镜像大小（字节）
	Created       string `json:"created"`
	DockerVersion string `json:"docker_version"`
	PushTime      string `json:"push_time"`
	PullTime      string `json:"pull_time"`
}

// CheckPullAccess 检查用户是否有指定仓库的pull权限
// API: /rest/v1/openapi/project/role?username={username}&projectName={projectName}
// I-User-Name header 使用系统账号 ipipe01
func CheckPullAccess(username, projectName string) (bool, error) {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	// I-User-Name 使用系统账号 ipipe01
	systemUsername := "ipipe01"
	token := GenerateAuthToken(systemUsername)

	// 构建URL
	apiURL := fmt.Sprintf("%s/rest/v1/openapi/project/role?username=%s&projectName=%s",
		iregistryBaseURL, url.QueryEscape(username), url.QueryEscape(projectName))

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return false, fmt.Errorf("创建请求失败: %w", err)
	}

	// 添加鉴权Header - 使用系统账号
	req.Header.Set("I-User-Name", systemUsername)
	req.Header.Set("I-User-Token", token)
	req.Header.Set("I-User-Timestamp", timestamp)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("请求iregistry API失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("读取响应失败: %w", err)
	}

	// 403 表示没有权限
	if resp.StatusCode == http.StatusForbidden {
		return false, fmt.Errorf("没有仓库 %s 的访问权限", projectName)
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("iregistry API返回错误状态码: %d, body: %s", resp.StatusCode, string(body))
	}

	var result RoleResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("解析响应JSON失败: %w, body: %s", err, string(body))
	}

	return result.HasPullAccess, nil
}

// GetImageSize 获取镜像大小
// API: /rest/v1/openapi/images/tags/{tagId}?repositoryName={repositoryName}
// I-User-Name header 使用系统账号 ipipe01
func GetImageSize(repositoryName, tag string) (int64, error) {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	// I-User-Name 使用系统账号 ipipe01
	systemUsername := "ipipe01"
	token := GenerateAuthToken(systemUsername)

	// 构建URL
	apiURL := fmt.Sprintf("%s/rest/v1/openapi/images/tags/%s?repositoryName=%s",
		iregistryBaseURL, url.QueryEscape(tag), url.QueryEscape(repositoryName))

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return 0, fmt.Errorf("创建请求失败: %w", err)
	}

	// 添加鉴权Header - 使用系统账号
	req.Header.Set("I-User-Name", systemUsername)
	req.Header.Set("I-User-Token", token)
	req.Header.Set("I-User-Timestamp", timestamp)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("请求iregistry API失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("iregistry API返回错误状态码: %d, body: %s", resp.StatusCode, string(body))
	}

	var result ImageSizeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("解析响应JSON失败: %w, body: %s", err, string(body))
	}

	return result.Size, nil
}

// CheckImageAccess 校验自定义镜像的权限和大小
// 返回: hasPullAccess(是否有权限), imageSize(镜像大小), error
func CheckImageAccess(username, imageURL string) (bool, int64, error) {
	// 解析镜像URL
	imageInfo, err := ParseImageURL(imageURL)
	if err != nil {
		return false, 0, fmt.Errorf("解析镜像地址失败: %w", err)
	}

	// 1. 校验用户是否有pull权限
	hasPull, err := CheckPullAccess(username, imageInfo.ProjectName)
	if err != nil {
		return false, 0, fmt.Errorf("校验pull权限失败: %w", err)
	}
	if !hasPull {
		return false, 0, fmt.Errorf("没有该镜像仓库(%s)的pull权限", imageInfo.ProjectName)
	}

	// 2. 校验镜像大小是否超过10GB
	imageSize, err := GetImageSize(imageInfo.RepositoryName, imageInfo.Tag)
	if err != nil {
		return false, 0, fmt.Errorf("获取镜像大小失败: %w", err)
	}
	if imageSize > maxImageSize {
		return false, imageSize, fmt.Errorf("镜像大小%.2fGB超过10GB限制，不允许添加",
			float64(imageSize)/1024/1024/1024)
	}

	return true, imageSize, nil
}
