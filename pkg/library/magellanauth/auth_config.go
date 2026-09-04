package magellanauth

type AuthApiConfig struct {
	Namespace string `json:"namespace"`
	Endpoint  string `json:"endpoint"`
	Token     string `json:"token"`
}

type CodeAuthBean struct {
	RepoType   string   `json:"repoType"`
	Repo       string   `json:"repo"`
	ExpireHour int      `json:"expireHour"`
	Actions    []string `json:"actions"`
	Jwt        string   `json:"jwt,omitempty"`
}

type LogJwtRequest struct {
	ExpireHour int    `json:"expireHour"`
	Uuid       string `json:"uuid"`
}

type LogJwtResponse struct {
	Jwt string `json:"jwt"`
}

type CodeAuthResponse struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Data    SSHKeyResponse `json:"data"`
}
type SSHKeyResponse struct {
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}
