package httpserver

import (
	"context"
	"github.com/google/uuid"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/bos"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/response"
	"icode.baidu.com/baidu/gdp/ghttp"
	"icode.baidu.com/baidu/gdp/logit"
	"net/http"
	"strings"
)

type UploadArtifactRequest struct {
	SandboxID   string `json:"sandboxID"`
	Scope       string `json:"scope"`
	AccessToken string `json:"accessToken"`
	ContentLen  int64  `json:"contentLen"`
	Password    string `json:"password"`
}

type UploadArtifactContext struct {
	Headers       map[string]string `json:"headers"`
	Host          string            `json:"host"`
	Bucket        string            `json:"bucket"`
	ObjectKey     string            `json:"objectKey"`
	ArtifactID    string            `json:"artifactID"`
	Authorization string            `json:"authorization"`
}

type DownloadArtifactRequest struct {
	SandboxID   string `json:"sandboxID"`
	AccessToken string `json:"accessToken"`
	ArtifactID  string `json:"artifactID"`
}

type DownloadArtifactContext struct {
	Headers       map[string]string `json:"headers"`
	Host          string            `json:"host"`
	Bucket        string            `json:"bucket"`
	ObjectKey     string            `json:"objectKey"`
	ArtifactID    string            `json:"artifactID"`
	Authorization string            `json:"authorization"`
	Password      string            `json:"password"`
}

func uploadArtifactHandler(ctx context.Context, req ghttp.Request) ghttp.Response {

	request := &UploadArtifactRequest{}

	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	artifactID := uuid.New().String()
	artifactID = strings.Replace(artifactID, "-", "", -1)
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime
	artifactDao := dao.Use(resource.OrmMain).Artifact

	sandboxRuntime, err := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.SandboxUUID.Eq(request.SandboxID),
		sandboxRuntimeDao.Token.Eq(request.AccessToken),
	).First()
	if err != nil {
		resource.LoggerService.Notice(ctx, "uploadArtifactHandler auth failed",
			logit.String("sandboxID", request.SandboxID),
		)
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	upCtx := bos.GetBosApi().GetUploadCtx(artifactID, request.ContentLen)

	res := UploadArtifactContext{
		Headers:       upCtx.Headers,
		Host:          upCtx.Host,
		Bucket:        upCtx.Bucket,
		ObjectKey:     upCtx.ObjectKey,
		ArtifactID:    artifactID,
		Authorization: upCtx.Authorization,
	}

	artifact := &model.Artifact{
		AppID:      sandboxRuntime.AppID,
		SandboxID:  request.SandboxID,
		ArtifactID: artifactID,
		Scope:      request.Scope,
		Size:       request.ContentLen,
		ObjectKey:  upCtx.ObjectKey,
		Password:   request.Password,
	}
	artifactDao.Create(artifact)

	return response.NewGResponse(http.StatusOK, &res)
}

func downloadArtifactHandler(ctx context.Context, req ghttp.Request) ghttp.Response {

	// appID := ctx.Value(constant.CtxAppIDKey).(int32)
	request := &DownloadArtifactRequest{}

	if err := util.Bind(req, request); err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}
	sandboxRuntimeDao := dao.Use(resource.OrmMain).SandboxRuntime

	artifactDao := dao.Use(resource.OrmMain).Artifact

	sandboxRuntime, err := sandboxRuntimeDao.Where(
		sandboxRuntimeDao.SandboxUUID.Eq(request.SandboxID),
		sandboxRuntimeDao.Token.Eq(request.AccessToken),
	).First()
	if err != nil {
		resource.LoggerService.Notice(ctx, "downloadArtifactHandler auth failed",
			logit.String("sandboxID", request.SandboxID),
		)
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	artifact, err := artifactDao.Where(
		artifactDao.ArtifactID.Eq(request.ArtifactID),
	).First()
	if err != nil {
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	if artifact.Scope != "PUBLIC" && artifact.AppID != sandboxRuntime.AppID {
		return response.NewGResponse(http.StatusBadRequest,
			response.ErrParams)
	}

	if artifact.Scope == "SANDBOX" {
		if artifact.SandboxID != request.SandboxID {
			return response.NewGResponse(http.StatusBadRequest,
				response.ErrParams)
		}
	}

	downCtx := bos.GetBosApi().GetDownloadCtx(artifact.ObjectKey)

	res := DownloadArtifactContext{
		Headers:       downCtx.Headers,
		Host:          downCtx.Host,
		Bucket:        downCtx.Bucket,
		ObjectKey:     downCtx.ObjectKey,
		ArtifactID:    request.ArtifactID,
		Authorization: downCtx.Authorization,
		Password:      artifact.Password,
	}
	return response.NewGResponse(http.StatusOK, &res)
}
