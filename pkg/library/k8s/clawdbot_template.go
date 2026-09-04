package k8s

import (
	"bytes"
	"fmt"
	"text/template"
)

const clawdBotTemplate = `
{
  "meta": {
    "lastTouchedVersion": "2026.1.24-3",
    "lastTouchedAt": "2026-01-29T04:09:31.571Z"
  },
  "wizard": {
    "lastRunAt": "2026-01-29T04:09:31.569Z",
    "lastRunVersion": "2026.1.24-3",
    "lastRunCommand": "doctor",
    "lastRunMode": "remote"
  },
  "auth": {
    "profiles": {
      "anthropic:default": {
        "provider": "anthropic",
        "mode": "api_key"
      }
    }
  },
  {{- if .IsSuperUser}}
  "models": {
    "mode": "merge",
    "providers": {
      "oneapi": {
        "baseUrl": "https://oneapi-comate.baidu-int.com",
        "apiKey": "{{.OneApiKey}}",
        "api": "anthropic-messages",
        "models": [
          {
            "id": "glm-4.7-internal",
            "name": "glm-4.7-internal",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 8192
          },
          {
            "id": "Kimi-K2.5-internal",
            "name": "Kimi-K2.5-internal",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "MiniMax-M2.5-internal",
            "name": "MiniMax-M2.5-internal",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "GLM-5",
            "name": "GLM-5",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "Kimi-K2.5",
            "name": "Kimi-K2.5",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "MiniMax-M2.5",
            "name": "MiniMax-M2.5",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "Claude Haiku 4.5",
            "name": "Claude Haiku 4.5",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "Claude Sonnet 4.6",
            "name": "Claude Sonnet 4.6",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "Claude Opus 4.6",
            "name": "Claude Opus 4.6",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "deepseek-v3.1",
            "name": "deepseek-v3.1",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "ERNIE-5.0",
            "name": "ERNIE-5.0",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 128000,
            "maxTokens": 4096
          }
        ],
        "headers": {
          "comate_custom_header": "{\"username\":\"{{.Username}}\",\"source\":\"openclaw\"}"
        }
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "oneapi/glm-4.7-internal"
      },
      "models": {
        "oneapi/glm-4.7-internal": {},
        "oneapi/Kimi-K2.5-internal": {},
        "oneapi/MiniMax-M2.5-internal": {},
        "oneapi/GLM-5": {},
        "oneapi/Kimi-K2.5": {},
        "oneapi/MiniMax-M2.5": {},
        "oneapi/Claude Haiku 4.5": {},
        "oneapi/Claude Sonnet 4.6": {},
        "oneapi/Claude Opus 4.6": {},
        "oneapi/deepseek-v3.1": {},
        "oneapi/ERNIE-5.0": {}
      },
      "maxConcurrent": 4,
      "subagents": {
        "maxConcurrent": 8
      }
    }
  },
  {{- else}}
  "models": {
    "mode": "merge",
    "providers": {
      "oneapi": {
        "baseUrl": "http://oneapi-comate.baidu-int.com/v1",
        "apiKey": "{{.OneApiKey}}",
        "api": "anthropic-messages",
        "headers": {
          "comate_custom_header": "{\"username\":\"{{.Username}}\",\"source\":\"openclaw\"}"
        },
        "models": [
          {
            "id": "glm-5-openclaw",
            "name": "glm-5-openclaw",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "deepseek-v3.1",
            "name": "deepseek-v3.1",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 4096
          },
          {
            "id": "ERNIE-5.0",
            "name": "ERNIE-5.0",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 128000,
            "maxTokens": 4096
          },
          {
            "id": "glm-4.7-internal",
            "name": "glm-4.7-internal",
            "reasoning": false,
            "input": [
              "text"
            ],
            "cost": {
              "input": 0,
              "output": 0,
              "cacheRead": 0,
              "cacheWrite": 0
            },
            "contextWindow": 200000,
            "maxTokens": 8192
          }
        ]
      }
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "oneapi/glm-4.7-internal"
      },
      "models": {
        "oneapi/glm-4.7-internal": {},
        "oneapi/glm-5-openclaw": {},
        "oneapi/deepseek-v3.1": {},
        "oneapi/ERNIE-5.0": {}
      },
      "maxConcurrent": 4,
      "subagents": {
        "maxConcurrent": 8
      }
    }
  },
  {{- end}}
  "messages": {
    "ackReactionScope": "group-mentions"
  },
  "commands": {
    "native": "auto",
    "nativeSkills": "auto"
  },
  "gateway": {
    "controlUi": {
	   "allowInsecureAuth": true,
       "dangerouslyDisableDeviceAuth": true,
	   "allowedOrigins": [
			"https://8081-{{.SandboxUUID}}.agent-sandbox.baidu-int.com",
			"https://openclaw-8081-{{.SandboxUUID}}.n.baidu-int.com",
			"https://8081-{{.SandboxUUID}}.agent-sandbox-fe.baidu-int.com"
			]
    },
    "port": 8081,
    "mode": "remote",
    "bind": "lan",
    "auth": {
      "mode": "token",
	  {{- if .OpenClawToken}}
      "token": "{{.OpenClawToken}}"
	  {{- else}}
      "token": "123456"
	  {{- end}}
    },
    "trustedProxies": [
      "10.27.142.208","10.27.252.152"
    ],
    "tailscale": {
      "mode": "off",
      "resetOnExit": false
    },
    "remote": {
      "url": "ws://127.0.0.1:8081",
      "token": "{{.OpenClawToken}}"
    },
     "http": {
      "endpoints": {
        "responses": {
          "enabled": true
        }
      }
    }
  },
  "plugins": {
    "load": {
      "paths": [
        "/home/gem/.openclaw/plugins"
      ]
    },
    "allow": [
      "session-statistics-plugin"
    ],
    "entries": {
      "session-statistics-plugin": {
        "enabled": true
      },
      "telegram": {
        "enabled": true
      }
    }
  },
  "browser": {
    "enabled": true,
    "defaultProfile": "external",
    "profiles": {
      "external": {
        "cdpUrl": "http://127.0.0.1:9222",
        "color": "#00AA00"
      },
      "openclaw": {
        "cdpPort": 18800,
        "color": "#FF4500"
      }
    }
  }
}

`

type ClawdBotTemplateData struct {
	OneApiKey          string
	OpenClawToken      string
	SandboxUUID        string
	Username           string
	IsSmallTrafficUser bool
	IsSuperUser        bool
}

func RenderClawdBotJson(data *ClawdBotTemplateData) (string, error) {
	tmpl, err := template.New("clawd_bot").Parse(clawdBotTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse user script template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute user script template: %w", err)
	}

	return buf.String(), nil
}
