package gw

type GwConfig struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Business    string `json:"business"`
	SecretKey   string `json:"secretKey"`
	AccountName string `json:"accountName"`
	AccountID   string `json:"accountID"`
	DomainType  string `json:"domainType"`
	Protocol    string `json:"protocol"`
	BackendHost string `json:"backendHost"`
	BackendPort int    `json:"backendPort"`
}
