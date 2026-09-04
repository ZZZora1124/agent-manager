package authcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	AppKey    = "appKey"
	SecretKey = "secretKey"
	Timestamp = "timestamp"
	SRandom   = "sRandom"
	Sign      = "sign"
)

// GenSign 鉴权的签名生成逻辑
func GenSign(appKey string, secretKey string, bodyParams *url.Values) string {

	// 参数中追加appKey, timestamp, 随机盐
	bodyParams.Set(AppKey, appKey)
	bodyParams.Set(Timestamp, strconv.FormatInt(time.Now().Unix(), 10))
	bodyParams.Set(SRandom, uuid.New().String())

	// 防止误传递sk及sign，首先移除
	bodyParams.Del(SecretKey)
	bodyParams.Del(Sign)

	// 对Key进行排序
	var keys []string
	for key := range *bodyParams {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// 拼接待计算签名的字符串
	beforeSha256 := strings.Builder{}
	for _, key := range keys {
		value := bodyParams.Get(key)
		beforeSha256.WriteString(value)
	}

	// 计算签名的字符串最后追加sk
	beforeSha256.WriteString(secretKey)

	// SHA256计算签名
	sha256Util := sha256.New()
	sha256Util.Write([]byte(beforeSha256.String()))
	sign := hex.EncodeToString(sha256Util.Sum(nil))

	return sign
}
