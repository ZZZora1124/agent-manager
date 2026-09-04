package service

import (
	"context"
	"icode.baidu.com/baidu/devops-ai/agent-runtime-manager/pkg/library/k8s"
)

type BindPvService struct{}

type BindPvRequest struct {
	PvcPrefix string   `json:"pvcPrefix"`
	SubDir    string   `json:"subDir"`
	MountPath string   `json:"mountPath"`
	Clusters  []string `json:"clusters"`
	SaveDb    bool     `json:"saveDb"`
}

type BindPvResponse struct {
	BindID int64 `json:"bindID"`
}

func (b *BindPvService) BindPv(ctx context.Context, cluster *k8s.Cluster, pvcName string,
	pvName string, subDir string) error {

	return cluster.CreateStaticPvcAndPv(ctx, pvName, pvcName, subDir)

}
