package envvar

import (
	"os"

	"icode.baidu.com/baidu/gdp/env"

	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/magellanauth"
)

const DisableBackgroundTaskEnv = "DISABLE_BACKGROUND_TASK"

func GetICodeHost() string {
	if env.RunMode() == env.RunModeRelease {
		return "icode.baidu.com"
	}
	return "icodeqa.baidu.com"
}

func GetMagellanConfig() *magellanauth.AuthApiConfig {
	if env.RunMode() == env.RunModeRelease {
		return magellanauth.GetAuthApi().Config
	}
	return magellanauth.GetAuthApi().DevConfig
}

func IsDisableBackgroundTask() bool {
	if isDisable, ok := os.LookupEnv(DisableBackgroundTaskEnv); ok {
		return isDisable == "true"
	}
	return false
}

func GetBroadCastCfsClusters() []string {
	if env.RunMode() == env.RunModeRelease {
		return []string{"agent-cce-prod", "agent-sandbox2-prod", "agent-sandbox3-prod"}
	}
	return []string{"agent-cce-prod", "agent-sandbox2"}
}

func GetFuseOwnerToken() string {
	if env.RunMode() == env.RunModeRelease {
		return "56adfa15-7d83-4399-868f-e5354f2bda9d"
	}
	return "9f0ae581-6e17-4d4e-a34d-b967782154c8"
}
