package agfs

type Config struct {
	Endpoint string `json:"endpoint"`
	Driver   string `json:"driver"`
}

// Config AgFS 配置，bootstrap 时初始化。
var agfsConf *Config = nil

func GetAgfsConfig() *Config {
	return agfsConf
}

func SetAgfsConfig(config *Config) {
	agfsConf = config
}
