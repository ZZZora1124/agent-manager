package bos

type ApiConfig struct {
	Endpoint       string `json:"Endpoint"`
	AccessKey      string `json:"AccessKey"`
	SecretKey      string `json:"SecretKey"`
	ArtifactBucket string `json:"ArtifactBucket"`
	ReportBucket   string `json:"ReportBucket"`
}
