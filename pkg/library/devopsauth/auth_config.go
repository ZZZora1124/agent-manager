package devopsauth

type AuthApiConfig struct {
	AuthEndpoint          string `json:"authEndpoint"`
	ModelAuthEndpoint     string `json:"modelAuthEndpoint"`
	ClientToken           string `json:"clientToken"`
	GlobalAccessToken     string `json:"globalAccessToken"`
	ZtAuthorizationSecret string `json:"ztAuthorizationSecret"`
}

type ValidateResponse struct {
	Code int           `json:"code"`
	Msg  string        `json:"msg"`
	Data *UserResponse `json:"data"`
}

type UserResponse struct {
	Username string `json:"username"`
}
