package runtimeapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

type RuntimeApi struct {
}

func GetRuntimeApi() *RuntimeApi {
	return &RuntimeApi{}
}

type Process struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args"`
}

type StartReq struct {
	Process Process `json:"process"`
}

type ProcessResponse struct {
	ExitCode int32  `json:"exitCode"`
	Stdout   string `json:"stdout"`
}

func mergeBytes(a, b []byte) []byte {
	var buf bytes.Buffer
	buf.Write(a)
	buf.Write(b)
	return buf.Bytes()
}

func (a RuntimeApi) StartCommand(sandboxID string, accessToken string, command string) bool {
	// 查询空间对应的资源关系
	pathCommand := "USER=`(whoami)`\n" +
		"if [ \"$USER\" == \"root\" ]; then\n" +
		"    export PATH=/root/.baidu-cc/baidu-cc/bin:$PATH\n" +
		"else\n" +
		"    export PATH=/home/$USER/.baidu-cc/baidu-cc/bin:$PATH\n" +
		"fi\n" +
		command
	startReq := StartReq{
		Process: Process{
			Cmd:  "bash",
			Args: []string{"-l", "-c", pathCommand},
		},
	}
	startReqByte, _ := json.Marshal(startReq)
	reqBody := mergeBytes([]byte("00000"), startReqByte)
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		urlStr := fmt.Sprintf("https://49983-%s.agent-sandbox.baidu-int.com/process.Process/Start", sandboxID)
		req, _ := http.NewRequest("POST", urlStr, bytes.NewReader(reqBody))
		req.Header.Set("X-Access-Token", accessToken)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return false
	}
	return true
}

func (a RuntimeApi) StartCommandSync(sandboxID string, accessToken string, command string) bool {
	// 查询空间对应的资源关系
	startReq := StartReq{
		Process: Process{
			Cmd:  "bash",
			Args: []string{"-l", "-c", command},
		},
	}
	startReqByte, _ := json.Marshal(startReq)
	var processResp ProcessResponse
	err := util.Backoff(util.DefaultRetry, func() (bool, error) {
		urlStr := fmt.Sprintf("https://49983-%s.agent-sandbox.baidu-int.com/process.Process/StartSync", sandboxID)
		req, _ := http.NewRequest("POST", urlStr, bytes.NewReader(startReqByte))
		req.Header.Set("X-Access-Token", accessToken)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()

		respByte, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 200 {
			_ = json.Unmarshal(respByte, &processResp)
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return false
	}
	return processResp.ExitCode == 0
}

func (a RuntimeApi) WriteFile(sandboxID string, accessToken string, filePath string, fileContent []byte) bool {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filePath)
	if err != nil {
		return false
	}
	if _, err := part.Write(fileContent); err != nil {
		return false
	}
	writer.Close()

	err = util.Backoff(util.DefaultRetry, func() (bool, error) {
		urlStr := fmt.Sprintf("http://49983-%s.agent-sandbox.baidu-int.com/files?path=%s",
			sandboxID, url.QueryEscape(filePath))
		req, _ := http.NewRequest("POST", urlStr, bytes.NewReader(body.Bytes()))
		req.Header.Set("X-Access-Token", accessToken)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		io.ReadAll(resp.Body)

		if resp.StatusCode == 200 {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return false
	}
	return true
}
