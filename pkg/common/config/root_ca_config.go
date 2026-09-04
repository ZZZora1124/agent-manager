package config

import (
	"icode.baidu.com/baidu/gdp/conf"
)

type RootCAConfig struct {
	CA string `json:"ca"`
}

var rootCAConfig *RootCAConfig = nil

func GetRootCAConfig() *RootCAConfig {
	if rootCAConfig == nil {
		config := &RootCAConfig{}
		err := conf.Parse("root_ca.toml", config)
		if err != nil {
			panic("parse root ca config error:" + err.Error())
		}
		rootCAConfig = config
	}
	return rootCAConfig
}

func GetRootCA() string {
	return GetRootCAConfig().CA
}
