package card

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/common"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"log"
	"strconv"
	"time"

	"icode.baidu.com/baidu/gdp/logit"
)

// SandboxRuntimeService 沙箱运行时服务接口
type SandboxRuntimeService interface {
	RestoreSandboxRuntime(ctx context.Context, sandboxRuntimeID int64, role string, triggerType string, envVars map[string]string) (map[string]interface{}, error)
	GetSandboxRuntimeEnvVars(ctx context.Context, sandboxRuntimeID int64) (map[string]string, error)
}

// CallbackHandler 回调处理器
type CallbackHandler struct {
	// 回调的密钥 - 警告：不要硬编码密钥，应该从环境变量或配置中心读取
	encodingAESKey string
	// 沙箱运行时服务
	sandboxRuntimeService SandboxRuntimeService
}

// NewCallbackHandler 创建回调处理器实例
func NewCallbackHandler(aesKey string, sandboxRuntimeService SandboxRuntimeService) *CallbackHandler {
	if aesKey == "" {
		log.Fatal("AES key cannot be empty")
	}
	return &CallbackHandler{
		encodingAESKey:        aesKey,
		sandboxRuntimeService: sandboxRuntimeService,
	}
}

// ===== 数据结构定义 =====

// CallbackRequest 回调请求结构
type CallbackRequest struct {
	LogID   string           `json:"log_id"`
	AppKey  string           `json:"app_key"`
	EventID string           `json:"event_id"`
	UserID  string           `json:"user_id"`
	MsgJSON *CallbackMsgJSON `json:"msg_json"`
}

// CallbackMsgJSON 回调消息JSON结构
type CallbackMsgJSON struct {
	AppContext      string          `json:"app_context"`      // 消息中的上下文，发送卡片的时候业务方自己传的（json string格式）
	InteractivityID string          `json:"interactivity_id"` // 工作卡控件绑定事件ID
	OAuthCode       string          `json:"oauth_code"`       // 换取 user_access_token，用户身份支持发送双人会话卡片
	MsgID           string          `json:"msg_id"`
	MsgSeq          int             `json:"msg_seq"`
	Fields          []CallbackField `json:"fields"` // 卡片消息中反馈的内容：用户更新详情
}

// CallbackField 回调字段
type CallbackField struct {
	Name  string `json:"name"`  // 搭建平台卡片模板输入控件id
	Value string `json:"value"` // 用户在对应控件操作值（注意：下拉框操作返回值为index，非value）
}

// ContactBO 联系人业务对象
type ContactBO struct {
	ID       int64  `json:"id"`        // 用户imid
	RealName string `json:"real_name"` // 用户真实姓名
	UserID   string `json:"userid"`    // 用户邮箱前缀
}

// CallbackResponse 卡片回调响应 - 带加密
type CallbackResponse struct {
	Code    int    `json:"code"`     // 状态码，0表示成功
	MsgDesc string `json:"msg_desc"` // 弹框tips，比如：操作成功
	MsgJSON string `json:"msg_json"` // 加密后的卡片更新参数
}

// CallbackResponseMsgJSON 工作卡回调响应对象
type CallbackResponseMsgJSON struct {
	Data       map[string]interface{} `json:"data"`                  // 卡片更新参数（包含一个或者多个卡片绑定的动态变量）
	Success    bool                   `json:"success"`               // 是否成功
	AppContext map[string]interface{} `json:"app_context,omitempty"` // 如果需要回调更新发送卡片内容中的app_context，此处需要返回全量更新
}

// ===== 核心业务逻辑 =====

// HandleCallback 处理回调业务逻辑
// 参数：
//   - ctx: 上下文
//   - logID: 请求的logid（从header获取）
//   - appKey: app_key参数
//   - eventID: event_id参数
//   - userID: user_id参数
//   - encryptedMsgJSON: 加密的msg_json参数
//
// 返回：CallbackResponse 或 error
func (h *CallbackHandler) HandleCallback(ctx context.Context, logID, appKey, eventID, userID, encryptedMsgJSON string) (*CallbackResponse, error) {
	// 解析回调请求
	callbackReq, err := h.parseCallbackRequest(logID, appKey, eventID, userID, encryptedMsgJSON)
	if err != nil {
		resource.LoggerService.Notice(ctx, "failed to parse callback request", logit.String("error", err.Error()))
		return h.buildFailureResponse(ctx, "操作失败")
	}

	resource.LoggerService.Notice(ctx, "received callback request",
		logit.String("logId", callbackReq.LogID),
		logit.String("eventId", callbackReq.EventID),
		logit.String("userId", callbackReq.UserID),
		logit.String("interactivityId", callbackReq.MsgJSON.InteractivityID))

	// 获取回调详情
	msgJSON := callbackReq.MsgJSON
	interactivityID := msgJSON.InteractivityID

	// 根据事件ID处理不同的回调事件
	var data map[string]interface{}
	switch interactivityID {
	case "restartCallback":
		var appContext map[string]interface{}
		if err := json.Unmarshal([]byte(callbackReq.MsgJSON.AppContext), &appContext); err != nil {
			resource.LoggerService.Notice(ctx, "failed to parse app_context", logit.String("error", err.Error()))
			return h.buildFailureResponse(ctx, "操作失败")
		}
		sandboxID, ok := appContext["sandboxID"].(string)
		if !ok {
			resource.LoggerService.Notice(ctx, "failed to convert sandboxID type")
			return h.buildFailureResponse(ctx, "操作失败")
		}
		// 获取 userID 用于发送消息
		userID := ctx.Value(constant.CtxLoginUserKey).(string)
		if err := h.processRestartCallbackEvent(ctx, msgJSON, sandboxID); err != nil {
			resource.LoggerService.Notice(ctx, "processRestartCallbackEvent failed", logit.String("error", err.Error()))
			// 操作失败时,异步发送错误消息给用户
			if userID != "" {
				sandboxRuntimeID, _ := strconv.ParseInt(sandboxID, 10, 64)
				go h.sendMessageAfterRestore(context.Background(), userID, sandboxRuntimeID, err.Error())
			}
		}
		data = make(map[string]interface{})
		data["callbackButtonText"] = "已操作恢复"
		data["callbackButtonState"] = "disabled"
	default:
		data = make(map[string]interface{})
	}

	// 构造响应
	response, err := h.buildCallbackResponse(data, "已操作恢复")
	if err != nil {
		resource.LoggerService.Notice(ctx, "failed to build response", logit.String("error", err.Error()))
	}

	return response, nil
}

// parseCallbackRequest 解析回调请求
func (h *CallbackHandler) parseCallbackRequest(logID, appKey, eventID, userID, encryptedMsgJSON string) (*CallbackRequest, error) {
	if encryptedMsgJSON == "" {
		return nil, fmt.Errorf("msg_json parameter is empty")
	}

	// 解密 msg_json
	decryptedMsgJSON, err := h.DecryptMsgJSON(encryptedMsgJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt msg_json: %w", err)
	}

	// 反序列化 msg_json
	var msgJSON CallbackMsgJSON
	if err := json.Unmarshal([]byte(decryptedMsgJSON), &msgJSON); err != nil {
		return nil, fmt.Errorf("failed to parse msg_json: %w", err)
	}

	return &CallbackRequest{
		LogID:   logID,
		AppKey:  appKey,
		EventID: eventID,
		UserID:  userID,
		MsgJSON: &msgJSON,
	}, nil
}

// processRestartCallbackEvent 处理重启回调事件
func (h *CallbackHandler) processRestartCallbackEvent(ctx context.Context, msgJSON *CallbackMsgJSON, sandboxID string) error {
	// 转换 sandboxID 为 int64
	sandboxRuntimeID, err := strconv.ParseInt(sandboxID, 10, 64)
	if err != nil {
		resource.LoggerService.Notice(ctx, "failed to parse sandboxID to int64",
			logit.String("error", err.Error()),
			logit.String("sandboxID", sandboxID))
		return fmt.Errorf("invalid sandboxID format: %w", err)
	}

	// 调用 RestoreSandboxRuntime 服务
	// role 参数传空字符串，表示不做权限校验（从卡片回调过来的都认为有权限）
	// 从卡片回调触发的恢复，触发类型为 manual
	_, err = h.sandboxRuntimeService.RestoreSandboxRuntime(ctx, sandboxRuntimeID, "", constant.TriggerTypeManual, nil)
	if err != nil {
		resource.LoggerService.Notice(ctx, "failed to restore sandbox runtime",
			logit.String("error", err.Error()),
			logit.Int64("sandboxRuntimeID", sandboxRuntimeID))
		return err
	}

	resource.LoggerService.Notice(ctx, "successfully restored sandbox runtime",
		logit.Int64("sandboxRuntimeID", sandboxRuntimeID))

	// 获取 userID 用于发送消息
	userID := ctx.Value(constant.CtxLoginUserKey).(string)
	if userID == "" {
		resource.LoggerService.Notice(ctx, "userID not found in context")
		// 不影响主流程，直接返回成功
		return nil
	}

	// 获取 sandbox runtime 的启动时间和创建用户
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	var sandboxRuntime model.SandboxRuntime
	err = sandboxRuntimeDao.Where(sandboxRuntimeDao.ID.Eq(sandboxRuntimeID)).Scan(&sandboxRuntime)
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to get sandbox runtime",
			logit.String("error", err.Error()),
			logit.Int64("sandboxRuntimeID", sandboxRuntimeID))
		// 不影响主流程，使用默认话术
		sandboxRuntime.StartTime = time.Time{}
		sandboxRuntime.CreateUser = userID // 兜底使用当前用户
	}

	// 检查是否应该跳过发送消息（包括白名单和auto_wake检查）
	if common.ShouldSkipSendingRestartMessage(ctx, userID, sandboxRuntime.SandboxUUID, resource.LoggerService) {
		resource.LoggerService.Notice(ctx, "skip sending message after restore due to whitelist or auto_wake status",
			logit.String("userId", userID),
			logit.String("sandboxUUID", sandboxRuntime.SandboxUUID))
		return nil
	}

	// 获取通知话术（根据用户、sandbox启动时间和redis配置判断）
	successMessage := common.GetPreStartNotificationMessage(context.Background(), resource.RedisClient, sandboxRuntime.CreateUser, sandboxRuntime.StartTime)
	go h.sendMessageAfterRestore(context.Background(), userID, sandboxRuntimeID, successMessage)

	return nil
}

// sendMessageAfterRestore 恢复沙箱后发送消息通知用户
func (h *CallbackHandler) sendMessageAfterRestore(ctx context.Context, userID string, sandboxRuntimeID int64, messageContent string) {
	resource.LoggerService.Notice(ctx, "preparing to send message after restore",
		logit.String("userId", userID),
		logit.Int64("sandboxRuntimeID", sandboxRuntimeID),
		logit.String("message", messageContent))

	// 获取 sandbox runtime 的环境变量
	envVars, err := h.sandboxRuntimeService.GetSandboxRuntimeEnvVars(ctx, sandboxRuntimeID)
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to get sandbox runtime envVars",
			logit.String("error", err.Error()),
			logit.Int64("sandboxRuntimeID", sandboxRuntimeID))
		return
	}

	// 从环境变量中获取 appKey 和 appSecret
	appKey, ok := envVars["INFOFLOW_APP_KEY"]
	if !ok || appKey == "" {
		resource.LoggerService.Error(ctx, "INFOFLOW_APP_KEY not found in envVars",
			logit.Int64("sandboxRuntimeID", sandboxRuntimeID))
		return
	}

	appSecret, ok := envVars["INFOFLOW_APP_SECRET"]
	if !ok || appSecret == "" {
		resource.LoggerService.Error(ctx, "INFOFLOW_APP_SECRET not found in envVars",
			logit.Int64("sandboxRuntimeID", sandboxRuntimeID))
		return
	}

	// 发送文本消息
	client, err := NewSendMessageClient(appKey, appSecret)
	if err != nil {
		resource.LoggerService.Error(ctx, "failed to create send message client",
			logit.String("error", err.Error()),
			logit.String("userId", userID))
		return
	}

	err = client.SendTextMessage(ctx, userID, messageContent, appKey, appSecret)
	if err != nil {
		resource.LoggerService.Error(ctx, "send message after restore failed",
			logit.String("userId", userID),
			logit.String("error", err.Error()))
		return
	}

	resource.LoggerService.Notice(ctx, "send message after restore success",
		logit.String("userId", userID))
}

// BuildCallbackResponse 导出版本，供外部 handler 构造回调响应
func (h *CallbackHandler) BuildCallbackResponse(data map[string]interface{}, msgDesc string) (*CallbackResponse, error) {
	return h.buildCallbackResponse(data, msgDesc)
}

// buildCallbackResponse 构造回调响应
func (h *CallbackHandler) buildCallbackResponse(data map[string]interface{}, msgDesc string) (*CallbackResponse, error) {
	responseMsgJSON := CallbackResponseMsgJSON{
		Data:    data,
		Success: true,
	}

	// 序列化响应数据
	jsonBytes, err := json.Marshal(responseMsgJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize response data: %w", err)
	}

	// 加密响应数据
	encryptedMsgJSON, err := h.EncryptMsgJSON(string(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt response data: %w", err)
	}

	return &CallbackResponse{
		Code:    0,
		MsgDesc: msgDesc,
		MsgJSON: encryptedMsgJSON,
	}, nil
}

// buildFailureResponse 构造失败响应
func (h *CallbackHandler) buildFailureResponse(ctx context.Context, msgDesc string) (*CallbackResponse, error) {
	responseMsgJSON := CallbackResponseMsgJSON{
		Data:    make(map[string]interface{}),
		Success: false,
	}

	// 序列化响应数据
	jsonBytes, err := json.Marshal(responseMsgJSON)
	if err != nil {
		resource.LoggerService.Notice(ctx, "failed to serialize failure response data", logit.String("error", err.Error()))
		// 即使序列化失败，也返回一个基本的失败响应
		return &CallbackResponse{
			Code:    -1,
			MsgDesc: msgDesc,
			MsgJSON: "",
		}, nil
	}

	// 加密响应数据
	encryptedMsgJSON, err := h.EncryptMsgJSON(string(jsonBytes))
	if err != nil {
		resource.LoggerService.Notice(ctx, "failed to encrypt failure response data", logit.String("error", err.Error()))
		// 即使加密失败，也返回一个基本的失败响应
		return &CallbackResponse{
			Code:    -1,
			MsgDesc: msgDesc,
			MsgJSON: "",
		}, nil
	}

	return &CallbackResponse{
		Code:    -1,
		MsgDesc: msgDesc,
		MsgJSON: encryptedMsgJSON,
	}, nil
}

// BuildFailureResponse 构造失败响应（导出方法）
func (h *CallbackHandler) BuildFailureResponse(ctx context.Context, msgDesc string) (*CallbackResponse, error) {
	return h.buildFailureResponse(ctx, msgDesc)
}

// ===== 加密/解密工具方法 =====

// EncryptMsgJSON 加密消息JSON（公开方法）
func (h *CallbackHandler) EncryptMsgJSON(msgJSON string) (string, error) {
	if msgJSON == "" {
		return "", fmt.Errorf("msgJSON is empty")
	}

	// 解码Base64的AES密钥
	// 使用支持自动padding和URL-safe的函数
	keyBytes, err := base64DecodeWithAutoPadding(h.encodingAESKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode AES key: %w", err)
	}

	// 创建AES加密器
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	// 使用PKCS5Padding填充
	plaintext := pkcs5Padding([]byte(msgJSON), aes.BlockSize)

	// ECB模式加密
	ciphertext := make([]byte, len(plaintext))
	encryptECB(block, ciphertext, plaintext)

	// Base64 URL Safe编码
	return base64.URLEncoding.EncodeToString(ciphertext), nil
}

// DecryptMsgJSON 解密消息JSON（公开方法）
// 参考 Java 实现：CallbackDemoController.decryptMsgJson
func (h *CallbackHandler) DecryptMsgJSON(encryptedMsg string) (string, error) {
	if encryptedMsg == "" {
		return "", fmt.Errorf("encryptedMsg is empty")
	}

	// 解码Base64的AES密钥
	// Java: Base64.decodeBase64(ENCODING_AES_KEY)
	// Java的Base64库会自动处理缺少padding的情况，Go需要手动处理
	keyBytes, err := base64DecodeWithAutoPadding(h.encodingAESKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode AES key: %w", err)
	}

	// 创建AES解密器
	// Java: Cipher cipher = Cipher.getInstance("AES/ECB/PKCS5Padding");
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	// Base64解码加密数据
	// Java: cipher.doFinal(Base64.decodeBase64(encryptedMsg))
	ciphertext, err := base64DecodeWithAutoPadding(encryptedMsg)
	if err != nil {
		return "", fmt.Errorf("failed to decode msg base64: %w", err)
	}

	// ECB模式解密
	plaintext := make([]byte, len(ciphertext))
	decryptECB(block, plaintext, ciphertext)

	// 去除PKCS5Padding
	plaintext, err = pkcs5Unpadding(plaintext)
	if err != nil {
		return "", fmt.Errorf("failed to remove padding: %w", err)
	}

	return string(plaintext), nil
}

// base64DecodeWithAutoPadding 自动补齐Base64 padding后解码
// 兼容Java的Base64.decodeBase64行为，自动处理缺少'='的情况
// 自动检测并使用URL-safe Base64解码（Java的encodeBase64URLSafeString）
func base64DecodeWithAutoPadding(s string) ([]byte, error) {
	// 补齐Base64 padding（Base64长度必须是4的倍数）
	for len(s)%4 != 0 {
		s += "="
	}

	// 检测是否为URL-safe Base64（包含 - 或 _）
	// Java的 Base64.encodeBase64URLSafeString 使用URL-safe编码
	isURLSafe := false
	for _, ch := range s {
		if ch == '-' || ch == '_' {
			isURLSafe = true
			break
		}
	}

	if isURLSafe {
		// 使用URL-safe Base64解码器
		return base64.URLEncoding.DecodeString(s)
	}
	// 使用标准Base64解码器
	return base64.StdEncoding.DecodeString(s)
}

// ===== 加密工具函数 =====

// pkcs5Padding PKCS5填充
func pkcs5Padding(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := make([]byte, padding)
	for i := range padtext {
		padtext[i] = byte(padding)
	}
	return append(data, padtext...)
}

// pkcs5Unpadding 去除PKCS5填充
func pkcs5Unpadding(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, fmt.Errorf("data is empty")
	}
	padding := int(data[length-1])
	if padding > length {
		return nil, fmt.Errorf("invalid padding")
	}
	return data[:length-padding], nil
}

// encryptECB ECB模式加密
func encryptECB(block cipher.Block, dst, src []byte) {
	blockSize := block.BlockSize()
	for len(src) > 0 {
		block.Encrypt(dst, src[:blockSize])
		src = src[blockSize:]
		dst = dst[blockSize:]
	}
}

// decryptECB ECB模式解密
func decryptECB(block cipher.Block, dst, src []byte) {
	blockSize := block.BlockSize()
	for len(src) > 0 {
		block.Decrypt(dst, src[:blockSize])
		src = src[blockSize:]
		dst = dst[blockSize:]
	}
}
