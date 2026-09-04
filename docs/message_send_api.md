# 如流单聊消息发送接口

## 接口信息

### 基本信息

| 项目 | 内容 |
|------|------|
| 接口路径 | /message/send |
| HTTP 方法 | POST |
| 接口说明 | 发送单聊文本消息到如流 |

---

## 请求参数

### 请求头 (Headers)

| 名称 | 类型 | 是否必须 | 描述 |
|------|------|----------|------|
| Content-Type | String | 是 | application/json |

### 请求体 (Request Body)

```json
{
    "toUser": "yugeyang",
    "content": "这是一条测试消息",
    "appKey": "your_app_key",
    "appSecret": "your_app_secret"
}
```

### 参数说明

| 参数 | 类型 | 是否必须 | 说明 |
|------|------|----------|------|
| toUser | String | 是 | 接收消息的用户的 uuapName（邮箱前缀），多个用户用 `|` 分隔，例如：`user1|user2` |
| content | String | 是 | 消息文本内容 |
| appKey | String | 是 | 如流应用的 app_key |
| appSecret | String | 是 | 如流应用的 app_secret |

---

## 返回结果

### 成功响应

```json
{
    "code": 200,
    "message": "message sent successfully",
    "toUser": "yugeyang"
}
```

### 失败响应

#### 参数错误
```json
{
    "code": 400,
    "message": "toUser, content, appKey and appSecret are required"
}
```

#### 发送失败
```json
{
    "code": 500,
    "message": "failed to send message, errcode=40070, errmsg=接收者不在可见范围或未关注"
}
```

---

## 使用示例

### cURL 示例

```bash
curl -X POST \
  'http://your-server/message/send' \
  -H 'Content-Type: application/json' \
  -d '{
    "toUser": "yugeyang",
    "content": "您好，这是一条测试消息",
    "appKey": "your_app_key",
    "appSecret": "your_app_secret"
}'
```

### 发送给多个用户

```bash
curl -X POST \
  'http://your-server/message/send' \
  -H 'Content-Type: application/json' \
  -d '{
    "toUser": "yugeyang|test_rd1|test_rd2",
    "content": "这是群发消息",
    "appKey": "your_app_key",
    "appSecret": "your_app_secret"
}'
```

### Python 示例

```python
import requests
import json

url = "http://your-server/message/send"

headers = {
    "Content-Type": "application/json"
}

data = {
    "toUser": "yugeyang",
    "content": "您好，这是一条测试消息",
    "appKey": "your_app_key",
    "appSecret": "your_app_secret"
}

response = requests.post(url, headers=headers, data=json.dumps(data))
print(response.json())
```

### Go 示例

```go
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "net/http"
)

type SendMessageRequest struct {
    ToUser    string `json:"toUser"`
    Content   string `json:"content"`
    AppKey    string `json:"appKey"`
    AppSecret string `json:"appSecret"`
}

func main() {
    url := "http://your-server/message/send"

    request := SendMessageRequest{
        ToUser:    "yugeyang",
        Content:   "您好，这是一条测试消息",
        AppKey:    "your_app_key",
        AppSecret: "your_app_secret",
    }

    jsonData, _ := json.Marshal(request)

    resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
    if err != nil {
        fmt.Println("Error:", err)
        return
    }
    defer resp.Body.Close()

    body, _ := ioutil.ReadAll(resp.Body)
    fmt.Println(string(body))
}
```

---

## 注意事项

1. **用户标识**：`toUser` 使用 uuapName（即百度邮箱前缀，不含 @baidu.com）
2. **多用户发送**：多个用户使用 `|` 分隔，例如：`user1|user2|user3`
3. **权限要求**：需要在如流企业后台开启权限：**以机器人身份发送消息到单聊**
4. **限流规则**：默认限流为 10 QPS/机器人
5. **加密方式**：app_secret 会自动使用 MD5 加密后再发送
6. **消息类型**：目前仅支持文本消息

---

## 错误码

| 错误码 | 错误信息 | 说明 |
|--------|----------|------|
| 200 | message sent successfully | 成功 |
| 400 | toUser, content, appKey and appSecret are required | 缺少必填参数 |
| 500 | 具体错误信息 | 发送失败，查看错误信息 |

---

## 与卡片发送接口对比

| 特性 | 消息接口 (`/message/send`) | 卡片接口 (`/card/send`) |
|------|---------------------------|-------------------------|
| 接口路径 | POST /message/send | POST /card/send |
| 内容类型 | 文本消息 | 工作卡片 |
| 必填参数 | toUser, content, appKey, appSecret | receiver, appKey, appSecret, sandboxID |
| 使用场景 | 简单文本通知 | 交互式卡片（带按钮等） |

---

## 技术支持

- 如流开放平台文档：[链接](https://ku.baidu-int.com)
- 联系人：魏小伟 (weixiaowei@baidu.com)

---

创建时间：2025-02-14

