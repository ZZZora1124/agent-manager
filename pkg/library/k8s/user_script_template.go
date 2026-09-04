package k8s

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common/config"
	"icode.baidu.com/baidu/gdp/env"
	"strings"
	"text/template"
)

type UserScriptTemplateData struct {
	ScriptType             string
	SuUserName             string
	WorkingDir             string
	SshDir                 string
	LoginDir               string
	InjectE2bCode          bool
	PostScript             string
	ApiEndpoint            string
	AgentDownloadURL       string
	UgateToken             string
	Username               string
	ShouldInjectUgateToken bool   // 是否在 userScriptTemplate 中注入 ugate token（受小流量控制）
	AigateCliInstallCmd    string // aigate-cli 安装命令
	RootCAInstallCmd       string // Root CA 写入及信任链更新命令
}

// MoltbotShellTemplateData MoltBot shell 脚本模板数据
type MoltbotShellTemplateData struct {
	ClawBotPath               string
	OpenClawPath              string
	SandboxUUID               string
	PythonConfigManagerScript string
	Base64EncodedConfig       string
	OnetoolSoDownloadCmd      string
	IsSmallTrafficUser        bool   // 标记是否为小流量用户
	IsSuperUser               bool   // 标记是否为超级用户
	Username                  string // 用户名，用于写入 models.providers.glm.headers
	OneApiKey                 string // OneAPI的API Key
	PrimaryModel              string // 默认模型（小流量和非小流量分别选择）
}

const (

	// aigate-cli 下载和安装命令（异步执行）
	aigateCliInstallCmd = `
# 异步下载并安装 aigate-cli 工具到 /usr/local/bin
(
  echo "[DIAG] ========================================"
  echo "[DIAG] aigate-cli installation started at: $(date '+%Y-%m-%d %H:%M:%S')"
  echo "[DIAG] Background process PID: $BASHPID, Parent PID: $PPID"
  trap 'echo "[DIAG] Received termination signal at: $(date +%Y-%m-%d_%H:%M:%S)"; exit 1' TERM INT HUP

  echo "[DIAG] Starting wget at: $(date '+%Y-%m-%d %H:%M:%S')"
  wget -O /tmp/aigate-cli-linux-amd64-latest.tar.gz https://apigo.baidu-int.com/eac/aigate-cli/sandbox/aigate-cli-linux-amd64-latest.tar.gz
  wget_code=$?
  echo "[DIAG] wget completed with exit code: $wget_code at: $(date '+%Y-%m-%d %H:%M:%S')"

  if [ $wget_code -eq 0 ]; then
    echo "[DIAG] wget succeeded, downloaded file size: $(ls -lh /tmp/aigate-cli-linux-amd64-latest.tar.gz 2>&1)"
    echo "[DIAG] Starting tar extraction at: $(date '+%Y-%m-%d %H:%M:%S')"
    tar -xzf /tmp/aigate-cli-linux-amd64-latest.tar.gz -C /tmp
    tar_code=$?
    echo "[DIAG] tar completed with exit code: $tar_code"

    if [ -f /tmp/aigate-cli ]; then
      echo "[DIAG] aigate-cli binary found, size: $(ls -lh /tmp/aigate-cli 2>&1)"
      echo "[DIAG] Moving to /usr/local/bin at: $(date '+%Y-%m-%d %H:%M:%S')"
      mv /tmp/aigate-cli /usr/local/bin/aigate-cli
      chmod +x /usr/local/bin/aigate-cli
      echo "[INFO] aigate-cli installed successfully to /usr/local/bin"
      echo "[DIAG] Installed file: $(ls -lh /usr/local/bin/aigate-cli 2>&1)"
    else
      echo "[WARNING] aigate-cli binary not found in archive"
      echo "[DIAG] Contents of /tmp after extraction: $(ls -la /tmp/ | grep -i aigate 2>&1)"
    fi
    rm -f /tmp/aigate-cli-linux-amd64-latest.tar.gz
  else
    echo "[WARNING] Failed to download aigate-cli"
    echo "[DIAG] wget exit code $wget_code"
  fi

  echo "[DIAG] Installation completed at: $(date '+%Y-%m-%d %H:%M:%S')"
  echo "[DIAG] Final check: $(ls -lh /usr/local/bin/aigate-cli 2>&1 || echo 'aigate-cli not found')"
  echo "[DIAG] ========================================"
{{ACP_BOOTSTRAP_INSTALL_CMD}}) > /tmp/aigate-cli-install.log 2>&1 &
`

	// Acp(Clawguard) Bootstrap 命令
	acpBootstrapInstallCmd = `
  echo "[DIAG] ========================================"
  echo "[DIAG] Acp(Clawguard) Bootstrap started at: $(date '+%Y-%m-%d %H:%M:%S')"
  BUSINESS_ID="${BUSINESS_ID:-empty}" && curl -fsSL "https://acp-server.baidu-int.com/bootstrap/${BUSINESS_ID}/?instance_id=${HOSTNAME}&hostname=${HOSTNAME}&username=${SANDBOX_USERNAME}" | bash -s || echo "[WARN] Acp(Clawguard) Bootstrap script execution failed, continuing..."
  echo "[DIAG] Acp(Clawguard) Bootstrap completed at: $(date '+%Y-%m-%d %H:%M:%S')"
  echo "[DIAG] ========================================"
`
)

// buildAigateInstallCmd 将 extra 注入 aigateCliInstallCmd 的异步子 shell 中
// extra 为空时等同于原始 aigateCliInstallCmd
func buildAigateInstallCmd(extra string) string {
	return strings.Replace(aigateCliInstallCmd, "{{ACP_BOOTSTRAP_INSTALL_CMD}}", extra, 1)
}

// sandboxInfraInstallCmd 返回含沙箱基础设施（aigate-cli + Acp Bootstrap）的异步安装命令
func sandboxInfraInstallCmd() string {
	return strings.Replace(
		strings.Replace(aigateCliInstallCmd, "{{ACP_BOOTSTRAP_INSTALL_CMD}}", acpBootstrapInstallCmd, 1),
		"/tmp/aigate-cli-install.log",
		"/tmp/sandbox-infra-install.log",
		1,
	)
}

// getRootCAInstallCmd 生成写入 Root CA、更新系统信任链及 certutil nssdb 的命令，兼容 Debian/Ubuntu 和 CentOS/RHEL
// setRootCa 为 false 时返回空串；suUserName 非空时以该用户身份执行 certutil
func getRootCAInstallCmd(suUserName string, setRootCa bool) string {
	if !setRootCa {
		return ""
	}
	ca := strings.TrimSpace(config.GetRootCA())
	encoded := base64.StdEncoding.EncodeToString([]byte(ca))
	var certutilCmd string
	if suUserName != "" {
		certutilCmd = fmt.Sprintf(`su %s -c '
set +e
rm -rf ~/.pki && mkdir -p ~/.pki/nssdb
certutil -d sql:$HOME/.pki/nssdb -A -t "CT,C,C" -n "OpenClaw Proxy" -i ${ROOTCA_PATH}
set -e
'`, suUserName)
	} else {
		certutilCmd = `set +e
rm -rf ${_USER_HOME}/.pki && mkdir -p ${_USER_HOME}/.pki/nssdb
certutil -d sql:${_USER_HOME}/.pki/nssdb -A -t "CT,C,C" -n "OpenClaw Proxy" -i ${ROOTCA_PATH}
set -e`
	}
	return fmt.Sprintf(`
export BROWSER_EXTRA_ARGS="--disable-features=IPH_DesktopCustomizeChrome,IsolateOrigins,site-per-proces,TrackingProtection3pcd,CookieDeprecationFacilitatedTesting --no-sandbox"
# 写入 Root CA 证书并更新系统信任链（兼容 Debian/Ubuntu 和 CentOS/RHEL）
if [ -d /etc/pki/ca-trust ]; then
  mkdir -p /etc/pki/ca-trust/source/anchors/
  echo '%s' | base64 -d > /etc/pki/ca-trust/source/anchors/rootCA.crt
  update-ca-trust extract
  export ROOTCA_PATH=/etc/pki/ca-trust/source/anchors/rootCA.crt
  export REQUESTS_CA_BUNDLE=/etc/pki/tls/certs/ca-bundle.crt
  export NODE_EXTRA_CA_CERTS=/etc/pki/tls/certs/ca-bundle.crt
else
  mkdir -p /usr/local/share/ca-certificates/
  echo '%s' | base64 -d > /usr/local/share/ca-certificates/rootCA.crt
  update-ca-certificates
  export ROOTCA_PATH=/usr/local/share/ca-certificates/rootCA.crt
  export REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
  export NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt
fi
# 更新 certutil nssdb（用于 Chrome 等浏览器证书信任）
%s
`, encoded, encoded, certutilCmd)
}

type OpenClawShellTemplateData struct {
	UgateToken          string
	Username            string
	AigateCliInstallCmd string // aigate-cli 安装命令
}

const openclawShellTemplate = `
export REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
export NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt
export BROWSER_EXTRA_ARGS="--disable-features=IPH_DesktopCustomizeChrome,IsolateOrigins,site-per-proces,TrackingProtection3pcd,CookieDeprecationFacilitatedTesting"

/opt/gem/start.sh > /tmp/start.log 2>&1 &
`

const userScriptTemplate = `#!/bin/{{.ScriptType}}
set +x
set -e
export PATH=${PATH}:/noah/bin:/usr/bin
echo "[INFO] Running on container in"
echo "hostname: $(uname -n)"
echo "ip: $(hostname -i)"
echo "workspace: ${PWD}"
echo "----- prepare env start ---"
echo ${MAGELLAN_CODE_AUTH}

{{- if .SuUserName}}
# 创建用户
if ! id gem &>/dev/null; then
  groupadd -g 1000 {{.SuUserName}}
  useradd -m -u 1000 -g 1000 {{.SuUserName}} -d /home/{{.SuUserName}}
else
  echo "用户 gem 已存在"
fi
{{- end}}

# 设置用户主目录
_USER_HOME="/root"
{{- if .SuUserName}}
_USER_HOME=/home/{{.SuUserName}}
{{- else}}
if [[ $(whoami) == "root" ]];then
    _USER_HOME=/root
else
    _USER_HOME=/home/$(whoami)
fi
{{- end}}

mkdir -p ${_USER_HOME}/.ssh

{{.RootCAInstallCmd}}
{{- if .ShouldInjectUgateToken}}
# 写入 ugate token
mkdir -p ${_USER_HOME}/.config/uuap
rm -rf ${_USER_HOME}/.config/uuap/.eac_ugate_token_*
cat > ${_USER_HOME}/.config/uuap/.eac_ugate_token_{{.Username}} << EOF
{{.UgateToken}}
EOF
chmod 600 ${_USER_HOME}/.config/uuap/.eac_ugate_token_{{.Username}}
{{- if .SuUserName}}
chown -R {{.SuUserName}}:{{.SuUserName}} ${_USER_HOME}/.config/uuap
{{- end}}
{{- end}}
{{.AigateCliInstallCmd}}
# 处理SSH认证
if [[ ${MAGELLAN_CODE_AUTH} == "true" ]];then
    # 检查是否是多用户模式
    if [[ ${MAGELLAN_MULTI_USER} == "true" ]];then
        echo "multi-user mode detected,copy user keys to ${_USER_HOME}/.users"
        # 多用户模式：从用户专属目录拷贝,拷贝后目录为${_USER_HOME}/.users/{username}/.ssh
        cp -rf ${MAGELLAN_SSH_BASE_DIR} ${_USER_HOME}/
        find ${_USER_HOME}/.users
        # 生成ssh config配置
        {
          IFS=',' read -ra USERS_ARRAY <<< "$MAGELLAN_MULTI_USERS"
          for user in "${USERS_ARRAY[@]}"; do
            user=$(echo "$user" | xargs)  # 去除空格
            [ -n "$user" ] && cat << EOF
Host icode.baidu.com
User $user
IdentityFile ${_USER_HOME}/.users/$user/.ssh/id_rsa
StrictHostKeyChecking no

EOF
          done
        } > ${_USER_HOME}/.ssh/config
        chmod 600 ${_USER_HOME}/.ssh/config
        find ${_USER_HOME}/.users/ -type d -exec chmod 700 {} +
        find ${_USER_HOME}/.users/ -type f -exec chmod 600 {} +
        echo "ssh config已生成到${_USER_HOME}/.ssh/config"
        {{- if .SuUserName}}
        chown -R {{.SuUserName}}:{{.SuUserName}} ${_USER_HOME}/.ssh
        chown -R {{.SuUserName}}:{{.SuUserName}} ${_USER_HOME}/.users
        {{- end}}
    else
        # 单用户模式：使用传统方式
        cp -rf {{.SshDir}} ${_USER_HOME}/
        chmod 600 ${_USER_HOME}/.ssh/*
        ls -ahl ${_USER_HOME}/.ssh
        {{- if .SuUserName}}
        chown -R {{.SuUserName}}:{{.SuUserName}} ${_USER_HOME}/.ssh
        {{- end}}
    fi
fi

# 处理BCloud认证文件
if [[ ${MAGELLAN_BCLOUD_AUTH} == "true" ]];then
    echo "copy $(whoami) bcloud login info"
    cp -rf {{.LoginDir}} ${_USER_HOME}/
    ls -ahl ${_USER_HOME}/.login
    {{- if .SuUserName}}
    # 单用户模式：使用传统方式
    chown -R {{.SuUserName}}:{{.SuUserName}} ${_USER_HOME}/.login
    {{- end}}
fi

# 设置工作目录
cd {{.WorkingDir}}
echo "----- prepare env end ---"
echo '----- user script start ---'

{{- if .PostScript}}
# 后台执行镜像定制的脚本;
{{.PostScript}}
{{- end}}

# 下载并执行agent runtime
wget -q -O /tmp/agent-runtime --no-check-certificate {{.AgentDownloadURL}} && chmod +x /tmp/agent-runtime

{{- if .SuUserName}}
    # su用户模式的内容
    su {{.SuUserName}} -c "/tmp/agent-runtime -endpoint {{.ApiEndpoint}}"
{{- else}}
    /tmp/agent-runtime -endpoint {{.ApiEndpoint}}
{{- end}}

`

// 渲染用户脚本模板
func renderUserScript(data *UserScriptTemplateData) (string, error) {
	tmpl, err := template.New("user_script").Parse(userScriptTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse user script template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute user script template: %w", err)
	}

	return buf.String(), nil
}

func renderOpenClawShell(data *OpenClawShellTemplateData) (string, error) {
	tmpl, err := template.New("openclaw_shell").Parse(openclawShellTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse openclaw shell template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute openclaw shell template: %w", err)
	}

	return buf.String(), nil
}

func GetByTemplateType(createPodFields *CreatePodFields, imageFields *ImageFields, oneApiKey string, openClawToken string, ugateToken string, username string) *UserScriptTemplateData {
	// 判断是否需要在 userScriptTemplate 中注入 ugate token：只有当用户在集群 DNS 配置白名单中时才注入
	shouldInjectUgateToken := false
	setRootCa := false
	if createPodFields != nil {
		inWhitelist, needRootCa := common.IsUserInClusterDnsWhitelistWithRootCa(createPodFields.AppID, username)
		shouldInjectUgateToken = inWhitelist && len(ugateToken) > 0 && len(username) > 0
		setRootCa = needRootCa
	}

	vsCodeScript := `
        if command -v start_comate_server.sh  &>/dev/null; then
            COMATE_COMMIT=${COMATE_SERVER_VERSION:-111706b9f5eefb50096e264c2fe49d3da8287d68-253638699}
            start_comate_server.sh --commit ${COMATE_COMMIT} --port 8200 --default-folder /workspace > /tmp/code-server.log 2>&1 &
		elif command -v code-server  &>/dev/null; then
			echo "启动code-server"
            code-server --bind-addr=0.0.0.0:8200 --disable-update-check --auth=none --disable-telemetry --disable-telemetry /workspace > /tmp/code-server.log 2>&1 &
		fi
	`
	apiEndpoint := "https://api-test.agent-sandbox.baidu-int.com"
	agentDownloadURL := "https://agentic-infra-msg.bj.bcebos.com/runtime/agent-runtime-test"
	if env.RunMode() == "release" {
		apiEndpoint = "https://api.agent-sandbox.baidu-int.com"
		agentDownloadURL = "https://agentic-infra-msg.bj.bcebos.com/runtime/agent-runtime"
	}

	templateData := &UserScriptTemplateData{
		ScriptType:             "bash",
		SuUserName:             "",
		WorkingDir:             WorkspaceVolumeMountPath,
		SshDir:                 SshDir,
		LoginDir:               LoginDir,
		InjectE2bCode:          true,
		PostScript:             vsCodeScript,
		ApiEndpoint:            apiEndpoint,
		AgentDownloadURL:       agentDownloadURL,
		UgateToken:             ugateToken,
		Username:               username,
		ShouldInjectUgateToken: shouldInjectUgateToken,
		AigateCliInstallCmd:    sandboxInfraInstallCmd(),
		RootCAInstallCmd:       getRootCAInstallCmd("", setRootCa),
	}

	if imageFields == nil {
		return templateData
	}

	if strings.ToLower(imageFields.Type) == "all-in-one" {
		return &UserScriptTemplateData{
			ScriptType:    "bash",
			SuUserName:    "gem",
			WorkingDir:    "/home/gem",
			SshDir:        SshDir,
			LoginDir:      LoginDir,
			InjectE2bCode: false,
			PostScript: "[ \"$DISABLE_BAIDU_CLAUDE_INSTALL\" != \"true\" ] && su gem -c \"curl -fsSL http://baidu-cc-client.bj.bcebos.com/baidu-cc/install.sh -o /tmp/ducc-install.sh && bash /tmp/ducc-install.sh\"\n" +
				"export PATH=/home/gem/.baidu-cc/baidu-cc/bin:$PATH\n" +
				"sed -i -E 's|chown (-R )?\\$USER:\\$USER /home/\\$USER|find /home/\\$USER -mindepth 1 -maxdepth 1 ! -user gem -print0 \\| xargs -r -0 chown -R gem:gem|' /opt/gem/run.sh\n" +
				"sed -i 's/5ab44bbb6bc38db0be4caa8f2d65a4d6f56ffd54-247199675/111706b9f5eefb50096e264c2fe49d3da8287d68-253638699/g' /opt/gem/supervisord/supervisord.code_server.conf 2>/dev/null || true\n" +
				"/opt/gem/run.sh > /tmp/aio.log 2>&1 &\n" +
				"[ -f /home/gem/.ssh/config ] && cat >> /home/gem/.ssh/config << 'EOF'\nHost *\nHostkeyAlgorithms +ssh-rsa\nPubkeyAcceptedKeyTypes +ssh-rsa\nEOF",
			ApiEndpoint:            apiEndpoint,
			AgentDownloadURL:       agentDownloadURL,
			UgateToken:             ugateToken,
			Username:               username,
			ShouldInjectUgateToken: shouldInjectUgateToken,
			AigateCliInstallCmd:    sandboxInfraInstallCmd(),
			RootCAInstallCmd:       getRootCAInstallCmd("gem", setRootCa),
		}
	} else if strings.ToLower(imageFields.Type) == "code" {
		return &UserScriptTemplateData{
			ScriptType:             "bash",
			SuUserName:             "",
			WorkingDir:             WorkspaceVolumeMountPath,
			SshDir:                 SshDir,
			LoginDir:               LoginDir,
			InjectE2bCode:          false,
			PostScript:             "/root/sandbox/scripts/run.sh > /tmp/aio.log 2>&1 &\n",
			ApiEndpoint:            apiEndpoint,
			AgentDownloadURL:       agentDownloadURL,
			UgateToken:             ugateToken,
			Username:               username,
			ShouldInjectUgateToken: shouldInjectUgateToken,
			AigateCliInstallCmd:    sandboxInfraInstallCmd(),
			RootCAInstallCmd:       getRootCAInstallCmd("", setRootCa),
		}
	} else if strings.ToLower(imageFields.Type) == "browser" {
		return &UserScriptTemplateData{
			ScriptType:             "bash",
			SuUserName:             "gem",
			WorkingDir:             "/home/gem",
			SshDir:                 SshDir,
			LoginDir:               LoginDir,
			InjectE2bCode:          false,
			PostScript:             "/opt/gem/run.sh > /tmp/aio.log 2>&1 &\n",
			ApiEndpoint:            apiEndpoint,
			AgentDownloadURL:       agentDownloadURL,
			UgateToken:             ugateToken,
			Username:               username,
			ShouldInjectUgateToken: shouldInjectUgateToken,
			AigateCliInstallCmd:    sandboxInfraInstallCmd(),
			RootCAInstallCmd:       getRootCAInstallCmd("gem", setRootCa),
		}
	} else if strings.ToLower(imageFields.Type) == "moltbot" {

		return RenderOpenClawTemplateData(apiEndpoint, agentDownloadURL, ugateToken, username, shouldInjectUgateToken)

	}

	return templateData

}

func RenderOpenClawTemplateData(apiEndpoint string, agentDownloadURL string, ugateToken string, username string, shouldInjectUgateToken bool) *UserScriptTemplateData {

	shellCmd, _ := renderOpenClawShell(&OpenClawShellTemplateData{
		UgateToken:          ugateToken,
		Username:            username,
		AigateCliInstallCmd: buildAigateInstallCmd(""),
	})

	return &UserScriptTemplateData{
		ScriptType:             "bash",
		SuUserName:             "gem",
		WorkingDir:             "/home/gem",
		SshDir:                 SshDir,
		LoginDir:               LoginDir,
		InjectE2bCode:          false,
		PostScript:             shellCmd,
		ApiEndpoint:            apiEndpoint,
		AgentDownloadURL:       agentDownloadURL,
		UgateToken:             ugateToken,
		Username:               username,
		ShouldInjectUgateToken: shouldInjectUgateToken,
		AigateCliInstallCmd:    buildAigateInstallCmd(""),
		RootCAInstallCmd:       "",
	}
}
