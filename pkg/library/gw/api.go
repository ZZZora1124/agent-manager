package gw

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/gdp/conf"
	"net/http"
	"time"
)

type GwApi struct {
	Config *GwConfig
}

const addDomainApiPath = "/api/v1/onetool/addDomain"

var gwApi *GwApi = nil
var gwConfig *GwConfig = nil

func GetGwApi() *GwApi {
	if gwConfig == nil {
		config := &GwConfig{}
		err := conf.Parse("gw.toml", config)
		if err != nil {
			panic("parse gw config error: " + err.Error())
		}
		gwConfig = config
	}
	if gwApi == nil {
		gwApi = &GwApi{
			Config: gwConfig,
		}
	}
	return gwApi
}

// generateToken 生成鉴权 Token
func (a *GwApi) generateToken() (string, string) {
	tm := fmt.Sprintf("%d", time.Now().Unix())
	zeroTokenStr := a.Config.Business + tm + a.Config.SecretKey
	hash := md5.Sum([]byte(zeroTokenStr))
	zeroTokenMd5 := hex.EncodeToString(hash[:])
	return zeroTokenMd5, tm
}

// AddDomain 添加域名
func (a *GwApi) AddDomain(req *DomainRequest) (*DomainResponse, error) {
	var resp DomainResponse

	// 设置默认值
	if req.DomainType == "" {
		req.DomainType = a.Config.DomainType
	}
	if req.AccountID == "" {
		req.AccountID = a.Config.AccountID
	}
	if req.AccountName == "" {
		req.AccountName = a.Config.AccountName
	}
	if req.BkProtocol == "" {
		req.BkProtocol = a.Config.Protocol
	}
	req.BackendList = []Backend{
		{
			Addr: a.Config.BackendHost,
			Port: a.Config.BackendPort,
		},
	}

	// 序列化请求体
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request error: %w", err)
	}

	err = util.Backoff(util.DefaultRetry, func() (bool, error) {
		// 创建请求
		httpReq, err := http.NewRequest("POST", a.Config.Endpoint+addDomainApiPath, bytes.NewReader(reqBody))
		if err != nil {
			return false, fmt.Errorf("create request error: %w", err)
		}

		// 生成鉴权 Token
		token, tm := a.generateToken()

		// 设置请求头
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("One-Tool-Token", token)
		httpReq.Header.Set("Resource-Type", a.Config.Business)
		httpReq.Header.Set("Timestamp", tm)

		// 发送请求
		client := &http.Client{}
		httpResp, err := client.Do(httpReq)
		if err != nil {
			return false, fmt.Errorf("send request error: %w", err)
		}
		defer httpResp.Body.Close()

		// 读取响应
		respBody := make([]byte, 0)
		buf := make([]byte, 1024)
		for {
			n, err := httpResp.Body.Read(buf)
			if n > 0 {
				respBody = append(respBody, buf[:n]...)
			}
			if err != nil {
				break
			}
		}

		// 解析响应
		err = json.Unmarshal(respBody, &resp)
		if err != nil {
			return false, fmt.Errorf("unmarshal response error: %w", err)
		}

		return true, nil
	})

	if err != nil {
		return nil, err
	}

	return &resp, nil
}
