package config

import (
	"icode.baidu.com/baidu/gdp/conf"
)

type Config struct {
	Endpoint     string `json:"endpoint"`
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`

	// Comate related config
	ComateClientID     string `json:"comateClientID"`
	ComateClientSecret string `json:"comateClientSecret"`

	// UgateToken 白名单 AppID 列表
	UgateTokenAllowedAppIDs []int32 `json:"ugateTokenAllowedAppIDs"`

	// Dodo AppID
	DodoAppId int32 `json:"dodoAppId"`

	// 浏览器自动登录服务端点
	BrowserAutoLoginEndpoint string `json:"browserAutoLoginEndpoint"`

	// Dodo AccessToken 用于浏览器自动登录鉴权
	DodoAccessToken string `json:"dodoAccessToken"`

	// 沙箱 Ugate-Token 状态查询接口地址
	SandboxTokenStatusEndpoint string `json:"sandboxTokenStatusEndpoint"`

	// UUAP 推送 AI 网关 Agent 信息配置
	UuapPushEndpoint  string `json:"uuapPushEndpoint"`  // 推送接口地址
	UuapPushAppKey    string `json:"uuapPushAppKey"`    // 如流机器人 AK
	UuapPushSecretKey string `json:"uuapPushSecretKey"` // 如流机器人 SK

	// 用户身份发卡换 token 目标 AppKey
	UserSendCardTargetAppKey string `json:"userSendCardTargetAppKey"`
}

var authCenterConfig *Config = nil

func GetAuthCenterConfig() *Config {
	if authCenterConfig == nil {
		config := &Config{}
		err := conf.Parse("auth_center.toml", config)
		if err != nil {
			panic("parse eac auth config error:" + err.Error())
		}
		authCenterConfig = config
	}
	return authCenterConfig
}

func GetDodoAppId() int32 {
	return GetAuthCenterConfig().DodoAppId
}
