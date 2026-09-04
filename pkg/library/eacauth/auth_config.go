package eacauth

import "icode.baidu.com/baidu/gdp/conf"

type Config struct {
	AuthEndpoint          string `json:"authEndpoint"`
	AccessKey             string `json:"accessKey"`
	SecretKey             string `json:"secretKey"`
	CardCallbackAESKey    string `json:"cardCallbackAESKey"`
	UserSendCardAppKey    string `json:"userSendCardAppKey"`
	UserSendCardAppSecret string `json:"userSendCardAppSecret"`
	UserSendCardHost      string `json:"userSendCardHost"`
}

var eacAuthConfig *Config = nil

func GetEacAuthConfig() *Config {
	if eacAuthConfig == nil {
		config := &Config{}
		err := conf.Parse("eac_auth.toml", config)
		if err != nil {
			panic("parse eac auth config error:" + err.Error())
		}
		eacAuthConfig = config
	}
	return eacAuthConfig
}
