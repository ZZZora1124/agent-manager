package bos

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"icode.baidu.com/baidu/gdp/conf"
	"net/url"
	"slices"
	"strings"
	"time"
)

type Api struct {
	Config *ApiConfig
}

var bosApi *Api = nil
var bosConfig *ApiConfig = nil

type UploadCtx struct {
	Headers       map[string]string
	Bucket        string
	ObjectKey     string
	Host          string
	Authorization string
}

type DownloadCtx struct {
	Headers       map[string]string
	Bucket        string
	ObjectKey     string
	Host          string
	Authorization string
}

func GetBosApi() *Api {
	if bosConfig == nil {
		config := &ApiConfig{}
		err := conf.Parse("bos.toml", config)
		if err != nil {
			panic("parse magellan auth config error")
		}
		bosConfig = config
	}
	if bosApi == nil {
		bosApi = &Api{
			Config: bosConfig,
		}
	}
	return bosApi
}

func generateHMAC(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
func (api *Api) GetUploadCtx(artifactID string, contentLen int64) *UploadCtx {
	var ctx UploadCtx

	// 格式化时间，类似于Python的'%Y-%m-%dT%H:%M:%SZ'
	// Go的格式化使用特定的时间作为参考：2006-01-02 15:04:05
	formattedTime := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	formattedDay := time.Now().Format("2006010215")
	host := fmt.Sprintf("%s.%s", api.Config.ArtifactBucket, api.Config.Endpoint)
	ctx.Headers = make(map[string]string)
	ctx.Headers["Host"] = host
	ctx.Headers["Content-Type"] = "application/gzip"
	ctx.Headers["Content-Length"] = fmt.Sprintf("%d", contentLen)
	ctx.Headers["x-bce-date"] = formattedTime
	ctx.Bucket = api.Config.ArtifactBucket
	ctx.Host = api.Config.Endpoint

	authStringPrefix := fmt.Sprintf("bce-auth-v1/%s/%s/3600", api.Config.AccessKey, formattedTime)
	objectUuid := uuid.New().String()
	objectUuid = strings.Replace(objectUuid, "-", "", -1)
	objectKey := fmt.Sprintf("%s/%s.tgz", formattedDay, objectUuid)
	canonicalURI := fmt.Sprintf("/%s", objectKey)

	sortHeaderKvs := make([]string, 0, len(ctx.Headers))
	for k, v := range ctx.Headers {
		sortHeaderKvs = append(sortHeaderKvs, fmt.Sprintf("%s:%s", strings.ToLower(k), url.QueryEscape(v)))
	}
	slices.Sort(sortHeaderKvs)

	sortHeaderKeys := make([]string, 0)
	for k, _ := range ctx.Headers {
		sortHeaderKeys = append(sortHeaderKeys, strings.ToLower(k))
	}
	slices.Sort(sortHeaderKeys)

	canonicalRequest := fmt.Sprintf("PUT\n%s\n%s\n%s", canonicalURI, "", strings.Join(sortHeaderKvs, "\n"))

	// 步骤 6: 生成 signingKey
	signingKey := generateHMAC([]byte(api.Config.SecretKey), []byte(authStringPrefix))
	signingKeyHex := hex.EncodeToString(signingKey)

	// 步骤 7: 生成 Signature
	signature := generateHMAC([]byte(signingKeyHex), []byte(canonicalRequest))

	// 输出十六进制格式的签名
	signatureHex := hex.EncodeToString(signature)

	ctx.Authorization = authStringPrefix + "/" + strings.Join(sortHeaderKeys, ";") + "/" + signatureHex
	ctx.ObjectKey = objectKey

	return &ctx
}

func (api *Api) GetDownloadCtx(objectKey string) *DownloadCtx {
	var ctx DownloadCtx

	// 格式化时间，类似于Python的'%Y-%m-%dT%H:%M:%SZ'
	// Go的格式化使用特定的时间作为参考：2006-01-02 15:04:05
	formattedTime := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	host := fmt.Sprintf("%s.%s", api.Config.ArtifactBucket, api.Config.Endpoint)
	ctx.Headers = make(map[string]string)
	ctx.Headers["Host"] = host
	ctx.Headers["x-bce-date"] = formattedTime
	ctx.Bucket = api.Config.ArtifactBucket
	ctx.Host = api.Config.Endpoint

	authStringPrefix := fmt.Sprintf("bce-auth-v1/%s/%s/3600", api.Config.AccessKey, formattedTime)

	canonicalURI := fmt.Sprintf("/%s", objectKey)

	sortHeaderKvs := make([]string, 0, len(ctx.Headers))
	for k, v := range ctx.Headers {
		sortHeaderKvs = append(sortHeaderKvs, fmt.Sprintf("%s:%s", strings.ToLower(k), url.QueryEscape(v)))
	}
	slices.Sort(sortHeaderKvs)

	sortHeaderKeys := make([]string, 0)
	for k, _ := range ctx.Headers {
		sortHeaderKeys = append(sortHeaderKeys, strings.ToLower(k))
	}
	slices.Sort(sortHeaderKeys)

	canonicalRequest := fmt.Sprintf("GET\n%s\n%s\n%s", canonicalURI, "", strings.Join(sortHeaderKvs, "\n"))

	// 步骤 6: 生成 signingKey
	signingKey := generateHMAC([]byte(api.Config.SecretKey), []byte(authStringPrefix))
	signingKeyHex := hex.EncodeToString(signingKey)

	// 步骤 7: 生成 Signature
	signature := generateHMAC([]byte(signingKeyHex), []byte(canonicalRequest))

	// 输出十六进制格式的签名
	signatureHex := hex.EncodeToString(signature)

	ctx.Authorization = authStringPrefix + "/" + strings.Join(sortHeaderKeys, ";") + "/" + signatureHex
	ctx.ObjectKey = objectKey
	return &ctx
}
