package gw

// Backend 后端服务器结构
type Backend struct {
	Addr string `json:"addr"`
	Port int    `json:"port"`
}

// DomainRequest 域名添加请求结构
type DomainRequest struct {
	DomainName  string    `json:"domain"`
	Admins      []string  `json:"admins"`
	DomainType  string    `json:"type"`
	AccountID   string    `json:"pdb_path"`
	AccountName string    `json:"pdb_name"`
	BkProtocol  string    `json:"bk_protocol"`
	BackendList []Backend `json:"backends"`
	Description string    `json:"description"`
}

// DomainResponse 域名添加响应结构
type DomainResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}
