package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/dao"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/resource"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/model"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/util/constant"
	"icode.baidu.com/baidu/gdp/logit"
)

type templateService struct {
	db *gorm.DB
}

func NewTemplateService() TemplateService {
	return &templateService{}
}

type TemplateService interface {
	CreateTemplate(ctx context.Context, req CreateTemplateRequest) (*TemplateResponse, error)
	DeleteTemplate(ctx context.Context, templateID int) (*TemplateResponse, error)
	SelectTemplate(ctx context.Context, templateID int) (*TemplateResponse, error)
	SelecTemplateAll(ctx context.Context, appID int) (*[]TemplateResponse, error)
	GetCommonTemplate(ctx context.Context) (*[]TemplateResponse, error)
	UpdateTemplate(ctx context.Context, req CreateTemplateRequest, role string) (*TemplateResponse, error)
}

type CodeBean struct {
	Module string `json:"module"`
	Branch string `json:"branch"`
}
type CreateTemplateRequest struct {
	TemplateID int32             `json:"templateId"`
	AppID      int32             `json:"appId"`
	Name       string            `json:"templateName"`
	Image      *ImageMeta        `json:"image"`
	Resource   *ResourceSpec     `json:"resource"`
	EnvirVars  map[string]string `json:"envVars"`
	Icode      bool              `json:"icode"`
	CodeBean   *CodeBean         `json:"codeBean"`
	Ducc       bool              `json:"ducc"`
	PostScript string            `json:"postScript"`
}

type ImageMeta struct {
	ImageID   int32  `json:"imageId"`
	ImageName string `json:"imageName"`
	Type      string `json:"type"`
	ImageURL  string `json:"address"`
}
type ResourceSpec struct {
	Type   string `json:"type"`
	Memory int32  `json:"memory"`
	CPU    int32  `json:"cpu"`
	Disk   int32  `json:"disk"`
}

type TemplateResponse struct {
	TemplateID   int32          `json:"templateId"`
	AppID        int32          `json:"appId"`
	TemplateName string         `json:"templateName"`
	ImageID      int32          `json:"imageId"`
	ImageURL     string         `json:"imageUrl"`
	Type         string         `json:"type"`
	ImageStatus  string         `json:"imageStatus"`
	ImageName    string         `json:"imageName"`
	Memory       int32          `json:"memory"`
	CPU          int32          `json:"cpu"`
	Disk         int32          `json:"disk"`
	CreateTime   time.Time      `json:"createTime"`
	EnvirVars    datatypes.JSON `json:"envVars"`
	Description  string         `json:"description"`
	ResType      string         `json:"resType"`
	Total        int32          `json:"total"`
	Running      int32          `json:"running"`
	Icode        bool           `json:"icode"`
	Ducc         bool           `json:"ducc"`
	CodeBean     *CodeBean      `json:"codeBean"`
	PostScript   string         `json:"postScript"`
}

// 创建应用
func (s *templateService) CreateTemplate(ctx context.Context, req CreateTemplateRequest) (*TemplateResponse, error) {
	logger := resource.LoggerService
	logger.Notice(ctx, "create template",
		logit.String("name", req.Name),
		logit.Int32("appID", req.AppID),
		logit.String("imageURL", req.Image.ImageURL),
		logit.Int32("imageID", req.Image.ImageID),
		logit.String("imageType", req.Image.Type),
		logit.String("resourceType", req.Resource.Type),
		logit.Int32("memory", req.Resource.Memory),
		logit.Int32("cpu", req.Resource.CPU),
		logit.Int32("disk", req.Resource.Disk),
		logit.String("env", fmt.Sprintf("%v", req.EnvirVars)))
	// 1. 参数基本校验
	if ok, err := checkTemplateParams(req); !ok {
		return nil, err
	}
	// 2. 校验name是否重复
	if y, _ := checkNameDuplicate(req.Name, req.AppID); y {
		return nil, fmt.Errorf("应用名称已存在，请更换名称")
	}
	// 3. 获取用户名用于校验镜像权限
	username := ctx.Value(constant.CtxLoginUserKey).(string)
	// 4. 创建镜像
	fromImageID := req.Image.ImageID
	imageDao := dao.Use(resource.OrmMain).Image
	if req.Image.ImageID == 0 {
		// 自定义模版 - 校验镜像权限和大小
		hasPullAccess, imageSize, err := util.CheckImageAccess(username, req.Image.ImageURL)
		if err != nil {
			logger.Error(ctx, "check image access failed", logit.String("error", err.Error()))
			return nil, err
		}
		logger.Notice(ctx, "image access check passed", logit.Bool("hasPullAccess", hasPullAccess), logit.Int64("imageSize", imageSize))
		//校验imageName是否重复
		if y, _ := checkImageNameDuplicate(req.Image.ImageName); y {
			return nil, fmt.Errorf("镜像名称已存在，请重试")
		}
		image := model.Image{
			Name:     req.Image.ImageName,
			Type:     req.Image.Type,
			ImageURL: req.Image.ImageURL,
			Status:   "unpreheated",
			Scope:    "custom",
		}
		if err := imageDao.Create(&image); err != nil {
			logger.Error(ctx, "insert image failed", logit.String("error", err.Error()))
			return nil, fmt.Errorf("insert image failed: %w", err)
		}
		fromImageID = image.ID
	}
	// 4. 创建应用
	envJSON, err := json.Marshal(req.EnvirVars)
	if err != nil {
		logger.Error(ctx, "marshal environment error", logit.String("error", err.Error()))
		return nil, fmt.Errorf("marshal environment error: %w", err)
	}
	codeBeanJSON := "{}"
	if req.CodeBean != nil {
		codeBeanBytes, err := json.Marshal(req.CodeBean)
		if err != nil {
			logger.Error(ctx, "marshal codeBean error", logit.String("error", err.Error()))
			return nil, fmt.Errorf("marshal codeBean error: %w", err)
		}
		codeBeanJSON = string(codeBeanBytes)
	}
	template := model.Template{
		AppID:       req.AppID,
		Name:        req.Name,
		Image:       req.Image.ImageURL,
		Memory:      req.Resource.Memory,
		CPU:         req.Resource.CPU,
		Disk:        req.Resource.Disk,
		Type:        req.Image.Type,
		EnvirVars:   string(envJSON),
		ImageID:     fromImageID,
		ResType:     req.Resource.Type,
		CreateTime:  time.Now(),
		Description: "",
		Icode:       req.Icode,
		Ducc:        req.Ducc,
		CreateUser:  ctx.Value(constant.CtxLoginUserKey).(string),
		CodeBean:    codeBeanJSON,
		PostScript:  req.PostScript,
	}
	// 5. 写库
	templateDao := dao.Use(resource.OrmMain).Template
	if err := templateDao.Create(&template); err != nil {
		logger.Error(ctx, "create template failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("db error: %w", err)
	}
	// 6. 组装返回 DTO
	resp := TemplateResponse{
		TemplateID:   template.ID,
		TemplateName: template.Name,
		ImageID:      template.ImageID,
		ImageURL:     template.Image,
		Type:         template.Type,
		Memory:       template.Memory,
		CPU:          template.CPU,
		Disk:         template.Disk,
		CreateTime:   template.CreateTime,
		EnvirVars:    datatypes.JSON(template.EnvirVars),
		ResType:      template.ResType,
		Total:        0,
		Running:      0,
		Icode:        template.Icode,
		Ducc:         template.Ducc,
		AppID:        template.AppID,
	}
	return &resp, nil
}

// 删除应用
func (s *templateService) DeleteTemplate(ctx context.Context, templateID int) (*TemplateResponse, error) {
	logger := resource.LoggerService
	logger.Notice(ctx, "delete template", logit.Int32("templateID", int32(templateID)))
	// 1. 先查是否存在
	templateDao := dao.Use(resource.OrmMain).Template
	tpl, err := templateDao.Where(templateDao.ID.Eq(int32(templateID)), templateDao.Deleted.Is(false)).First()
	if err != nil {
		logger.Error(ctx, "template not found", logit.String("error", err.Error()))
		return nil, fmt.Errorf("当前应用不存在: %w", err)
	}
	// 查看该应用下有没有正在运行的实例
	flag, err := checkSandboxReleased(ctx, nil, int32(templateID))
	if err != nil {
		logger.Error(ctx, "checkSandboxReleased failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("checkSandboxReleased failed: %w", err)
	}
	if !flag {
		return nil, fmt.Errorf("当前应用有运行中的实例，禁止删除")
	}
	// 2. 判断是否是公共应用模版
	if tpl.AppID == 0 {
		logger.Error(ctx, "template is common template", logit.String("error", "template is common template"))
		return nil, fmt.Errorf("当前应用为公共应用模版，禁止删除")
	}
	// 3. 软删除应用（标记deleted字段为true）
	_, err = templateDao.Where(templateDao.ID.Eq(int32(templateID))).UpdateColumn(templateDao.Deleted, true)
	if err != nil {
		logger.Error(ctx, "template soft delete failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("db error: %w", err)
	}
	// 4. 软删除实例（标记deleted字段为true）
	sandboxDao := dao.Use(resource.OrmMain).SandboxRuntime
	_, err = sandboxDao.Where(sandboxDao.TemplateID.Eq(int32(templateID))).UpdateColumn(sandboxDao.Deleted, true)
	if err != nil {
		logger.Error(ctx, "sandbox runtime soft delete failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("db error: %w", err)
	}
	// 5. 删除镜像
	// 查询是否是公共镜像
	imageDao := dao.Use(resource.OrmMain).Image
	imag, _ := imageDao.Where(imageDao.ID.Eq(tpl.ImageID)).First()
	if imag == nil {
		return nil, nil
	}
	if imag.Scope != "public" {
		// 删除镜像
		_, err = imageDao.Where(imageDao.ID.Eq(tpl.ImageID)).Delete()
		if err != nil {
			logger.Error(ctx, "image delete failed", logit.String("error", err.Error()))
			return nil, fmt.Errorf("db error: %w", err)
		}
		resource.PoolChangeChan <- tpl.ID
	}
	// 6. 返回成功
	return nil, nil
}

// 根据templateID查询应用
func (s *templateService) SelectTemplate(ctx context.Context, templateID int) (*TemplateResponse, error) {
	list, err := selectTemplate(ctx, 0, templateID, s.db)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil // 空列表返回零值
	}
	return &list[0], nil
}

// 查询appId下的所有应用
func (s *templateService) SelecTemplateAll(ctx context.Context, appID int) (*[]TemplateResponse, error) {
	list, err := selectTemplate(ctx, appID, 0, s.db)
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// 查询公共应用模版
func (s *templateService) GetCommonTemplate(ctx context.Context) (*[]TemplateResponse, error) {
	list, err := selectTemplate(ctx, -1, 0, s.db)
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// UpdateTemplate 编辑模版
func (s *templateService) UpdateTemplate(ctx context.Context, req CreateTemplateRequest, role string) (*TemplateResponse, error) {
	logger := resource.LoggerService
	logger.Notice(ctx, "update template", logit.Int32("templateID", req.TemplateID))
	if req.TemplateID <= 0 {
		return nil, fmt.Errorf("无效的模板Id")
	}
	templateDao := dao.Use(resource.OrmMain).Template
	var tpl *model.Template
	var err error
	if role == constant.RoleMember {
		// 成员自己可以编辑
		tpl, err = templateDao.Where(templateDao.ID.Eq(req.TemplateID), templateDao.CreateUser.Eq(ctx.Value(constant.CtxLoginUserKey).(string))).First()
	} else {
		// 管理员可以编辑其他人的应用
		tpl, err = templateDao.Where(templateDao.ID.Eq(req.TemplateID)).First()
	}
	if err != nil || tpl == nil {
		logger.Error(ctx, "template found failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("应用查询失败，请重试: %w", err)
	}
	// 1. 参数基本校验
	if ok, err := checkTemplateParams(req); !ok {
		return nil, err
	}
	// 2. 镜像处理
	fromImageID := req.Image.ImageID
	imageDao := dao.Use(resource.OrmMain).Image
	image, err := imageDao.Where(imageDao.ID.Eq(fromImageID)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Error(ctx, "image found failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("镜像查询失败，请重试: %w", err)
	}
	if image == nil {
		// 新建自定义镜像 - 校验镜像权限和大小
		username := ctx.Value(constant.CtxLoginUserKey).(string)
		hasPullAccess, imageSize, err := util.CheckImageAccess(username, req.Image.ImageURL)
		if err != nil {
			logger.Error(ctx, "check image access failed", logit.String("error", err.Error()))
			return nil, err
		}
		logger.Notice(ctx, "image access check passed", logit.Bool("hasPullAccess", hasPullAccess), logit.Int64("imageSize", imageSize))
		if y, _ := checkImageNameDuplicate(req.Image.ImageName); y {
			return nil, fmt.Errorf("镜像名称已存在，请重试")
		}
		newImage := model.Image{
			Name:     req.Image.ImageName,
			Type:     req.Image.Type,
			ImageURL: req.Image.ImageURL,
			Status:   "unpreheated",
			Scope:    "custom",
		}
		if err := imageDao.Create(&newImage); err != nil {
			logger.Error(ctx, "create image failed", logit.String("error", err.Error()))
			return nil, fmt.Errorf("镜像创建失败，请重试: %w", err)
		}
		fromImageID = newImage.ID
	}
	// 3. 更新应用
	envJSON, err := json.Marshal(req.EnvirVars)
	if err != nil {
		logger.Error(ctx, "marshal environment error", logit.String("error", err.Error()))
		return nil, fmt.Errorf("marshal environment error: %w", err)
	}
	codeBeanJSON := "{}"
	if req.CodeBean != nil {
		codeBeanBytes, err := json.Marshal(req.CodeBean)
		if err != nil {
			logger.Error(ctx, "marshal codeBean error", logit.String("error", err.Error()))
			return nil, fmt.Errorf("marshal codeBean error: %w", err)
		}
		codeBeanJSON = string(codeBeanBytes)
	}
	templateDao.Where(templateDao.ID.Eq(tpl.ID)).
		Updates(map[string]interface{}{
			"image":       req.Image.ImageURL,
			"memory":      req.Resource.Memory,
			"cpu":         req.Resource.CPU,
			"disk":        req.Resource.Disk,
			"type":        req.Image.Type,
			"envir_vars":  string(envJSON),
			"image_id":    fromImageID,
			"res_type":    req.Resource.Type,
			"icode":       req.Icode,
			"ducc":        req.Ducc,
			"update_time": time.Now(),
			"update_user": ctx.Value(constant.CtxLoginUserKey).(string),
			"code_bean":   codeBeanJSON,
			"post_script": req.PostScript,
		})

	resource.PoolChangeChan <- tpl.ID

	// 5. 组装返回 DTO
	resp := TemplateResponse{
		TemplateID:   tpl.ID,
		TemplateName: req.Name,
		ImageID:      fromImageID,
		ImageURL:     req.Image.ImageURL,
		Type:         req.Image.Type,
		Memory:       req.Resource.Memory,
		CPU:          req.Resource.CPU,
		Disk:         req.Resource.Disk,
		CreateTime:   tpl.CreateTime,
		EnvirVars:    datatypes.JSON(envJSON),
		ResType:      req.Resource.Type,
		Total:        0,
		Running:      0,
		Icode:        req.Icode,
		Ducc:         req.Ducc,
		AppID:        tpl.AppID,
		CodeBean:     req.CodeBean,
		PostScript:   req.PostScript,
	}
	return &resp, nil
}

// 查询应用列表
func selectTemplate(ctx context.Context, appID int, templateID int, db *gorm.DB) ([]TemplateResponse, error) {
	logger := resource.LoggerService
	logger.Notice(ctx, "select template", logit.Int("appID", appID), logit.Int("templateID", templateID))
	var list []model.Template
	templateDao := dao.Use(resource.OrmMain).Template
	if templateID != 0 {
		// 查询单个应用
		err := templateDao.Where(templateDao.ID.Eq(int32(templateID)), templateDao.Deleted.Is(false)).Scan(&list)
		if err != nil {
			logger.Error(ctx, "template found failed", logit.String("error", err.Error()))
			return nil, fmt.Errorf("未找到当前应用: %w", err)
		}
	} else {
		// 查询appId下的所有应用
		err := templateDao.Where(templateDao.AppID.Eq(int32(appID)), templateDao.Deleted.Is(false)).Scan(&list)
		if err != nil {
			logger.Error(ctx, "template found failed", logit.String("error", err.Error()))
			return nil, fmt.Errorf("未找到当前应用: %w", err)
		}
	}

	// 2 根据template_id查询每个模板的实例数量
	templateIds := make([]int32, len(list))
	for i, v := range list {
		templateIds[i] = v.ID
	}
	countMap, err := getCounts(ctx, db, templateIds)
	if err != nil {
		logger.Error(ctx, "getCounts failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("getCounts failed: %w", err)
	}

	// 3 根据imageID查询每个模板的镜像信息
	imageIds := make([]int32, len(list))
	for i, r := range list {
		imageIds[i] = r.ImageID
	}
	imgMap, err := GetImageMap(imageIds)
	if err != nil {
		logger.Error(ctx, "getImageMap failed", logit.String("error", err.Error()))
		return nil, fmt.Errorf("getImageMap failed: %w", err)
	}
	// 4 组装返回数据
	resp := make([]TemplateResponse, len(list))
	for i, t := range list {
		imageStatus := ""
		imageName := ""
		codeBean := &CodeBean{}
		envJSON := t.EnvirVars
		if imgMap != nil && imgMap[t.ImageID] != nil {
			imageStatus = imgMap[t.ImageID].Status
			imageName = imgMap[t.ImageID].Name
		}
		if envJSON == "" {
			envJSON = "{}"
		}

		if t.CodeBean == "" || t.CodeBean == "{}" {
			codeBean = nil
		} else {
			json.Unmarshal([]byte(t.CodeBean), codeBean)
		}
		resp[i] = TemplateResponse{
			TemplateID:   t.ID,
			TemplateName: t.Name,
			ImageID:      t.ImageID,
			ImageURL:     t.Image,
			Total:        countMap[t.ID].Total,
			Running:      countMap[t.ID].Running,
			CreateTime:   t.CreateTime,
			AppID:        t.AppID,
			Type:         t.Type,
			ImageStatus:  imageStatus,
			ImageName:    imageName,
			Memory:       t.Memory,
			CPU:          t.CPU,
			Disk:         t.Disk,
			EnvirVars:    datatypes.JSON(envJSON),
			Description:  t.Description,
			ResType:      t.ResType,
			Icode:        t.Icode,
			Ducc:         t.Ducc,
			CodeBean:     codeBean,
			PostScript:   t.PostScript,
		}
	}
	return resp, nil
}

// 获取沙箱实例数量与运行数量
func getCounts(ctx context.Context, db *gorm.DB, templateIds []int32) (map[int32]struct{ Total, Running int32 }, error) {
	type row struct {
		TemplateID int32 `gorm:"column:template_id"`
		Total      int32 `gorm:"column:total"`
		Running    int32 `gorm:"column:running"`
	}
	var rows []row
	sandboxDao := dao.Use(resource.OrmMain).SandboxRuntime // 确保这里非 nil
	db = sandboxDao.UnderlyingDB()
	err := db.WithContext(ctx).
		Raw(`SELECT template_id,
		            COUNT(*)                         AS total,
		            SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END) AS running
		     FROM   sandbox_runtime
		     WHERE  template_id IN ?
		     AND    deleted = false
		     GROUP  BY template_id`, templateIds).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	countMap := make(map[int32]struct{ Total, Running int32 }, len(rows))
	for _, r := range rows {
		countMap[r.TemplateID] = struct{ Total, Running int32 }{Total: r.Total, Running: r.Running}
	}
	return countMap, nil
}

// 判断沙箱下的实例是否被释放
func checkSandboxReleased(ctx context.Context, db *gorm.DB, templateID int32) (bool, error) {
	sandboxDao := dao.Use(resource.OrmMain).SandboxRuntime
	db = sandboxDao.UnderlyingDB()
	var running int32
	err := db.WithContext(ctx).
		Raw(`SELECT COALESCE(SUM(CASE WHEN status = 'running' OR status = 'pending' THEN 1 ELSE 0 END), 0)AS running
		     FROM   sandbox_runtime
		     WHERE  template_id =  ?
		     AND    deleted = false`, templateID).
		Scan(&running).Error
	if err != nil {
		return false, err
	}
	return running == 0, nil
}

// 镜像批量查询
func GetImageMap(imageIds []int32) (map[int32]*model.Image, error) {
	var fullImages []model.Image
	imageDao := dao.Use(resource.OrmMain).Image
	err := imageDao.Where(imageDao.ID.In(imageIds...)).Scan(&fullImages)
	if err != nil {
		return nil, err
	}

	imgMap := make(map[int32]*model.Image, len(fullImages))
	for i := range fullImages {
		imgMap[fullImages[i].ID] = &fullImages[i]
	}
	return imgMap, nil
}

// 返回 true 表示已存在重名
func checkNameDuplicate(name string, appID int32) (bool, error) {
	templateDao := dao.Use(resource.OrmMain).Template
	count, err := templateDao.Where(templateDao.AppID.Eq(appID), templateDao.Name.Eq(name), templateDao.Deleted.Is(false)).Count()
	return count > 0, err
}
func checkImageNameDuplicate(name string) (bool, error) {
	imageDao := dao.Use(resource.OrmMain).Image
	count, err := imageDao.Where(imageDao.Name.Eq(name)).Count()
	return count > 0, err
}

// 请求参数判断
func checkTemplateParams(req CreateTemplateRequest) (bool, error) {
	var nameRe = regexp.MustCompile(`^[a-zA-Z0-9\p{Han}_-]+$`)
	if req.AppID == 0 {
		return false, fmt.Errorf("appId 为空")
	}
	if req.Name == "" {
		return false, fmt.Errorf("名称为空")
	}
	if utf8.RuneCountInString(req.Name) < 1 || utf8.RuneCountInString(req.Name) > 64 {
		return false, fmt.Errorf("名称长度必须在1-64个字符之间")
	}
	if !nameRe.MatchString(req.Name) {
		return false, fmt.Errorf("名称只能包含中英文、数字、下划线、横杠")
	}
	if req.Resource == nil {
		return false, fmt.Errorf("资源参数为空")
	}
	if req.Resource.Type == "" {
		return false, fmt.Errorf("资源类型为空")
	}
	if ok, v := checkResourceParams(req.Resource.CPU, req.Resource.Memory, req.Resource.Disk); !ok {
		return false, v
	}
	if req.Image == nil {
		return false, fmt.Errorf("镜像资源为空")
	}
	if req.Image.ImageName == "" {
		return false, fmt.Errorf("镜像名称为空")
	}
	if utf8.RuneCountInString(req.Image.ImageName) < 1 || utf8.RuneCountInString(req.Image.ImageName) > 64 {
		return false, fmt.Errorf("镜像名称长度必须在1-64个字符之间")
	}
	if !nameRe.MatchString(req.Name) {
		return false, fmt.Errorf("镜像名称只能包含中英文、数字、下划线、横杠")
	}
	if req.Image.Type == "" {
		return false, fmt.Errorf("镜像资源类型为空")
	}
	if req.Image.ImageURL == "" {
		return false, fmt.Errorf("镜像资源地址为空")
	}
	// 判断镜像地址是否规范
	if flag := util.ValidImageAddr(req.Image.ImageURL); !flag {
		return false, fmt.Errorf("镜像资源地址格式不正确")
	}

	if len(req.EnvirVars) > 64 {
		return false, fmt.Errorf("环境变量不能超过64个")
	}
	return true, nil
}

func checkResourceParams(cpu int32, memory int32, disk int32) (bool, error) {
	// 1. 校验 CPU
	cpu = cpu / 1000
	memory = memory / 1024
	switch cpu {
	case 1, 2, 4, 8, 16:
	default:
		return false, fmt.Errorf("cpu 只能为 1、2、4、8、16c")
	}

	// 2. 校验并解析内存比例
	// 允许两种比例
	if memory != cpu*2 && memory != cpu*4 {
		return false, fmt.Errorf("memory 必须是 cpu 的 2 倍或 4 倍")
	}

	// 3. 校验磁盘
	if disk < 20 || disk > 100 || disk%10 != 0 {
		return false, fmt.Errorf("disk 必须在 20~100 GB 之间且为 10 的倍数")
	}
	return true, nil
}
