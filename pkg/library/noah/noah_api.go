package noah

import (
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/gdp/conf"
	"io"
	"net/http"
	"net/url"
	"time"
)

var noahApi *Api = nil
var noahConfig *ApiConfig = nil

type Api struct {
	Config *ApiConfig
}

// GetNoahApi 获取 Noah API 客户端单例
func GetNoahApi() *Api {
	if noahConfig == nil {
		config := &ApiConfig{}
		err := conf.Parse("noah.toml", config)
		if err != nil {
			panic(fmt.Sprintf("parse noah config error: %v", err))
		}
		noahConfig = config
	}
	if noahApi == nil {
		noahApi = &Api{
			Config: noahConfig,
		}
	}
	return noahApi
}

// BindHostID 绑定 host_id 到 Noah
func (api *Api) BindHostID(hostID int64, hostname, idc string) error {
	hosts := map[string]interface{}{
		fmt.Sprintf("%d", hostID): map[string]interface{}{
			"hostname": hostname,
			"idc":      idc,
		},
	}
	hostsString := mustMarshalJSON(hosts)
	if hostsString == "" {
		return fmt.Errorf("bind host_id failed: invalid hosts format")
	}

	params := url.Values{}
	params.Set("r", "Machine/MachineExternal/addHost")
	params.Set("hosts", hostsString)
	params.Set("provider", "ee_vm")
	params.Set("token", api.Config.Token)

	reqURL := "http://noah.baidu.com/goat/index.php?" + params.Encode()

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(reqURL)
	if err != nil {
		return fmt.Errorf("bind host_id failed: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bind host_id failed with status: %d, responseBody: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// BindBNS 绑定 BNS 服务
func (api *Api) BindBNS(bnsID, bns string) error {
	params := url.Values{}
	params.Set("r", "Machine/MachineExternal/mountNewHost")
	params.Set("ids", bnsID)
	params.Set("serviceName", bns)
	params.Set("provider", "ee_vm")
	params.Set("token", api.Config.Token)

	reqURL := "http://noah.baidu.com/goat/index.php?" + params.Encode()

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(reqURL)
	if err != nil {
		return fmt.Errorf("bind bns failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bind bns failed with status: %d, responseBody: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// mustMarshalJSON JSON 序列化，失败时 panic
func mustMarshalJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil || len(data) == 0 {
		return ""
	}
	return string(data)
}
