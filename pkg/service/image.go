package service

import (
	"context"
	"fmt"
	"gorm.io/gen"
	"gorm.io/gorm"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/gdp/env"
	"icode.baidu.com/baidu/gdp/logit"
)

// OpenClaw 镜像 ID 常量
const (
	OpenClawImageIDProd    int32 = 98 // 生产环境 OpenClaw 镜像 ID
	OpenClawImageIDNonProd int32 = 65 // 非生产环境 OpenClaw 镜像 ID
	// OpenClawDevTplIDForImageSync 需要同步更新相同 image_id 模版的特殊模版ID
	OpenClawDevTplIDForImageSync int32 = 224
	// OpenClawProdTplIDForImageSync 需要同步更新相同 image_id 模版的特殊模版ID
	OpenClawProdTplIDForImageSync int32 = 4517
)

type imageService struct {
	db *gorm.DB
}

type ImageResponse struct {
	ID          int32  `json:"id"`
	ImageName   string `json:"imageName"`
	Type        string `json:"type"`
	ImageURL    string `json:"imageUrl"`
	ImageStatus string `json:"imageStatus"`
}

func NewImageService() ImageService {
	return &imageService{}
}

type ImageService interface {
	GetPublicImage() (*[]ImageResponse, error)
	UpdateOpenClawImage(imageURL string) error
	UpdateImageByTplID(appID int32, template string, imageURL string) error
}

// 查询公共镜像模版
func (s *imageService) GetPublicImage() (*[]ImageResponse, error) {
	logger := resource.LoggerService
	var list []model.Image
	imageDao := dao.Use(resource.OrmMain).Image
	err := imageDao.Where(imageDao.Scope.Eq("public")).Scan(&list)
	if err != nil {
		logger.Error(context.Background(), "image found failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("image not found: %w", err)
	}
	resp := make([]ImageResponse, len(list))
	for i, t := range list {
		resp[i] = ImageResponse{
			ID:          t.ID,
			ImageName:   t.Name,
			Type:        t.Type,
			ImageURL:    t.ImageURL,
			ImageStatus: t.Status,
		}
	}
	return &resp, nil
}

// UpdateOpenClawImage 更新 OpenClaw 镜像版本
// 根据环境判断使用不同的 image ID：
// - 生产环境 (runMode == release): image ID = 98
// - 其他环境: image ID = 65
func (s *imageService) UpdateOpenClawImage(imageURL string) error {
	logger := resource.LoggerService
	ctx := context.Background()

	// 根据环境判断镜像 ID
	var imageID int32
	if env.RunMode() == env.RunModeRelease {
		imageID = OpenClawImageIDProd
		logger.Notice(ctx, "UpdateOpenClawImage: running in production environment", logit.Int32("imageID", imageID))
	} else {
		imageID = OpenClawImageIDNonProd
		logger.Notice(ctx, "UpdateOpenClawImage: running in non-production environment", logit.Int32("imageID", imageID))
	}

	// 使用事务更新两张表
	q := dao.Use(resource.OrmMain)
	err := q.Transaction(func(tx *dao.Query) error {
		// 1. 更新 image 表
		imageDao := tx.Image
		_, err := imageDao.Where(imageDao.ID.Eq(imageID)).Update(imageDao.ImageURL, imageURL)
		if err != nil {
			logger.Error(ctx, "UpdateOpenClawImage: failed to update image table",
				logit.Int32("imageID", imageID),
				logit.String("imageURL", imageURL),
				logit.String("error", err.Error()))
			return fmt.Errorf("failed to update image table: %w", err)
		}
		logger.Notice(ctx, "UpdateOpenClawImage: image table updated successfully",
			logit.Int32("imageID", imageID),
			logit.String("imageURL", imageURL))

		// 2. 更新 template 表
		templateDao := tx.Template
		resultInfo, err := templateDao.Where(templateDao.ImageID.Eq(imageID)).Update(templateDao.Image, imageURL)
		if err != nil {
			logger.Error(ctx, "UpdateOpenClawImage: failed to update template table",
				logit.Int32("imageID", imageID),
				logit.String("imageURL", imageURL),
				logit.String("error", err.Error()))
			return fmt.Errorf("failed to update template table: %w", err)
		}
		logger.Notice(ctx, "UpdateOpenClawImage: template table updated successfully",
			logit.Int32("imageID", imageID),
			logit.String("imageURL", imageURL),
			logit.Int64("rowsAffected", resultInfo.RowsAffected))
		return nil
	})

	if err != nil {
		return err
	}

	logger.Notice(ctx, "UpdateOpenClawImage: all tables updated successfully",
		logit.Int32("imageID", imageID),
		logit.String("imageURL", imageURL))

	return nil
}

// UpdateImageByTplID 根据模版ID更新镜像地址
// 更新 template 表的 image 字段和 image 表的 image_url 字段
// 当 tplID 为 2263 时，还需要更新所有相同 image_id 的 template
func (s *imageService) UpdateImageByTplID(appID int32, template string, imageURL string) error {
	logger := resource.LoggerService
	ctx := context.Background()

	q := dao.Use(resource.OrmMain)
	err := q.Transaction(func(tx *dao.Query) error {
		// 1. 查询模版获取 image_id
		templateDao := tx.Template
		tpl, err := templateDao.Where(
			templateDao.AppID.Eq(appID),
			templateDao.Name.Eq(template),
			templateDao.Deleted.Is(false),
		).First()
		if err != nil {
			logger.Error(ctx, "UpdateImageByTplID: failed to query template",
				logit.Int32("appID", appID),
				logit.String("template", template),
				logit.String("error", err.Error()))
			return fmt.Errorf("failed to query template: %w", err)
		}
		imageID := tpl.ImageID

		// 2. 更新 image 表
		imageDao := tx.Image
		_, err = imageDao.Where(imageDao.ID.Eq(imageID)).Update(imageDao.ImageURL, imageURL)
		if err != nil {
			logger.Error(ctx, "UpdateImageByTplID: failed to update image table",
				logit.Int32("imageID", imageID),
				logit.String("imageURL", imageURL),
				logit.String("error", err.Error()))
			return fmt.Errorf("failed to update image table: %w", err)
		}
		logger.Notice(ctx, "UpdateImageByTplID: image table updated successfully",
			logit.Int32("imageID", imageID),
			logit.String("imageURL", imageURL))

		specialTplID := int32(0)
		if env.RunMode() == env.RunModeRelease {
			specialTplID = OpenClawProdTplIDForImageSync
		} else {
			specialTplID = OpenClawDevTplIDForImageSync
		}
		// 3. 更新 template 表
		var resultInfo gen.ResultInfo
		var updateErr error
		if tpl.ID == specialTplID {
			resultInfo, updateErr = templateDao.Where(templateDao.ImageID.Eq(imageID)).Update(templateDao.Image, imageURL)
		} else {
			resultInfo, updateErr = templateDao.Where(templateDao.ID.Eq(tpl.ID)).Update(templateDao.Image, imageURL)
		}
		if updateErr != nil {
			logger.Error(ctx, "UpdateImageByTplID: failed to update template table",
				logit.Int32("imageID", imageID),
				logit.String("imageURL", imageURL),
				logit.String("error", err.Error()))
			return fmt.Errorf("failed to update template table: %w", err)
		}
		logger.Notice(ctx, "UpdateImageByTplID: template table updated successfully",
			logit.Int32("imageID", imageID),
			logit.String("template", template),
			logit.String("imageURL", imageURL),
			logit.Int64("rowsAffected", resultInfo.RowsAffected))
		return nil
	})

	if err != nil {
		return err
	}

	logger.Notice(ctx, "UpdateImageByTplID: all tables updated successfully",
		logit.String("template", template),
		logit.String("imageURL", imageURL))

	return nil
}
