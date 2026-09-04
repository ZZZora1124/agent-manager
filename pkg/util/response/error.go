// Copyright(C) 2024 Baidu Inc. All Rights Reserved.

// Package response 业务错误码
package response

import (
	"fmt"
)

type Error struct {
	Code    int    `json:"code"`    // 错误码
	Message string `json:"message"` // 错误信息
}

// Error 返回错误字符串
func (e Error) Error() string {
	return fmt.Sprintf("errno[%d], errmsg[%s]", e.Code, e.Message)
}

// Code 返回错误码
func Code(err error) int {
	if e, ok := err.(Error); ok {
		return e.Code
	}
	return -1
}

// Message 错误信息
func Message(err error) string {
	if e, ok := err.(Error); ok {
		return e.Message
	}
	return err.Error()
}

// New 创建错误
func New(message string, code int) Error {
	return Error{
		Code:    code,
		Message: message,
	}
}

// 成功错误码
const (
	// SuccessCode 成功返回码
	SuccessCode int = 0
)

// 系统错误码
const (
	// InternalErrCode 系统内部错误
	InternalErrCode = 10000 + iota
)

// 用户错误码
const (
	ParamsErrCode = 10201 + iota
	NotLoginErrCode
	ResourceNotFoundErrCode
	ClusterNotFoundErrCode
	AppNotMatchErrCode
	TemplateNotFoundErrCode
	UserNotGrantForAppErrCode
	CreateContainerErrorCode
	DeleteContainerErrorCode
	CreateContainerTimeoutErrorCode

	QueryDBErrorCode
	UnmarshalJsonErrCode
	MarshalJsonErrCode
	ContainerCountLimitErrCode
	K8sOperateErrCode
)

var (
	// 非必要情况error可以不定义错误码，只定义错误信息
	// 错误码规范：
	// 0 : 成功
	// 10000 - 19999: 系统对外输出错误码
	// 20000 - 99999: 业务自定义错误码

	// Success 成功
	Success = New("success", SuccessCode)

	// ErrInternalServer 系统内部错误
	ErrInternalServer = New("internal server error", InternalErrCode)

	// ErrParams 参数错误
	ErrParams = New("params error", ParamsErrCode)

	NotLoginErr = New("not login", NotLoginErrCode)

	ResourceNotFoundErr = New("resource not found", ResourceNotFoundErrCode)

	ClusterNotFoundErr = New("cluster not found", ClusterNotFoundErrCode)

	AppNotMatchErr = New("app not match", AppNotMatchErrCode)

	// TemplateNotFoundErr 模版不存在
	TemplateNotFoundErr = New("template not found", TemplateNotFoundErrCode)

	UserNotGrantForAppErr = New("user not grant for app", UserNotGrantForAppErrCode)

	CreateContainerError = New("create container error", CreateContainerErrorCode)

	CreateContainerTimeoutError = New("create container timeout error", CreateContainerTimeoutErrorCode)

	DeleteContainerError = New("delete container error", DeleteContainerErrorCode)

	QueryDbErr = New("query db error", QueryDBErrorCode)

	UnmarshalJsonErr = New("unmarshal json error", UnmarshalJsonErrCode)

	MarshalJsonErr = New("marshal json error", MarshalJsonErrCode)

	ContainerCountLimitErr = New("container count limit error", ContainerCountLimitErrCode)

	K8sOperateErr = New("k8s operate error", K8sOperateErrCode)
)

// 迁移错误码
const (
	MigrationNotFoundErrCode   = 10301
	MigrationConflictErrCode   = 10302
	MigrationTargetNodeErrCode = 10303
)

var (
	MigrationNotFoundErr   = New("pvc binding not found", MigrationNotFoundErrCode)
	MigrationConflictErr   = New("migration conflict: another migration in progress", MigrationConflictErrCode)
	MigrationTargetNodeErr = New("target node invalid", MigrationTargetNodeErrCode)
)
