package dns

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/gdp/conf"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DnsPath = "/controller/v1/dns/bcedns/domain"
)

var dnsApi *Api = nil
var dnsConfig *ApiConfig = nil

type Api struct {
	Config *ApiConfig
}

// GetDnsApi 获取 DNS API 客户端单例
func GetDnsApi() *Api {
	if dnsConfig == nil {
		config := &ApiConfig{}
		err := conf.Parse("dns.toml", config)
		if err != nil {
			panic(fmt.Sprintf("parse dns config error: %v", err))
		}
		dnsConfig = config
	}
	if dnsApi == nil {
		dnsApi = &Api{
			Config: dnsConfig,
		}
	}
	return dnsApi
}

// BindDNS 推送 DNS 解析记录
func (api *Api) BindDNS(hostname, ip string) error {
	// 从完整 hostname 中提取域名部分
	// hostname 格式: host-xxx.bcc-gajl
	parts := strings.Split(hostname, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid hostname format: %s", hostname)
	}

	data := map[string]interface{}{
		"domain": parts[0],                                    // host-xxx
		"zone":   strings.Join(parts[1:], ".") + ".baidu.com", // bcc-gajl.baidu.com
		"type":   "A",
		"ttl":    600,
		"rdata":  ip,
		"view":   "pnet",
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal dns data failed: %w", err)
	}

	reqURL := fmt.Sprintf("%s%s", api.Config.Endpoint, DnsPath)
	req, err := http.NewRequest("POST", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("create dns request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	timeMilli := time.Now().UnixMilli()

	req.Header.Set("Authorization", api.calSign(timeMilli))
	req.Header.Set("Timestamp", fmt.Sprintf("%d", timeMilli))
	req.Header.Set("Product-Name", api.Config.ProductName)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("bind dns failed: %w", err)
	}
	defer resp.Body.Close()
	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bind dns failed with status: %d, body: %s", resp.StatusCode, string(resBody))
	}
	return nil
}

func (api *Api) BindPtrDNS(hostname, ip string) error {
	// 从完整 hostname 中提取域名部分
	// hostname 格式: host-xxx.bcc-gajl
	parts := strings.Split(hostname, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid hostname format: %s", hostname)
	}
	ipSplits := strings.Split(ip, ".")
	domain := ipSplits[3] + "." + ipSplits[2]
	data := map[string]interface{}{
		"domain": domain,
		"zone":   fmt.Sprintf("%s.%s.in-addr.arpa", ipSplits[1], ipSplits[0]),
		"type":   "PTR",
		"ttl":    600,
		"rdata":  hostname + ".baidu.com",
		"view":   "pnet",
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal dns data failed: %w", err)
	}

	reqURL := fmt.Sprintf("%s%s", api.Config.Endpoint, DnsPath)
	req, err := http.NewRequest("POST", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("create dns request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	timeMilli := time.Now().UnixMilli()

	req.Header.Set("Authorization", api.calSign(timeMilli))
	req.Header.Set("Timestamp", fmt.Sprintf("%d", timeMilli))
	req.Header.Set("Product-Name", api.Config.ProductName)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("bind dns failed: %w", err)
	}
	defer resp.Body.Close()
	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bind ptr dns failed with status: %d, body: %s", resp.StatusCode, string(resBody))
	}
	return nil
}

func (api *Api) calSign(timeMilli int64) string {
	// 根据productName + timeMilli + token 计算digest hex
	digest := api.Config.ProductName + fmt.Sprintf("%d", timeMilli) + api.Config.Token
	return fmt.Sprintf("%x", md5.Sum([]byte(digest)))
}
