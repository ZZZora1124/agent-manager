package mcpcenter

type ApiConfig struct {
	Endpoint string `json:"endpoint"`
}

type ValidateResponse struct {
	Code int           `json:"code"`
	Msg  string        `json:"msg"`
	Data *UserResponse `json:"data"`
}

type UserResponse struct {
	Admins []string `json:"admins"`
}

type WorkspaceInfoResponse struct {
	Code int                `json:"code"`
	Msg  string             `json:"msg"`
	Data *WorkspaceResponse `json:"data"`
}

type WorkspaceResponse struct {
	Name            string   `json:"name"`
	CloudAccountIds []string `json:"cloudAccountIds"`
}

type UserPermissionRoleResponse struct {
	Code int                `json:"code"`
	Msg  string             `json:"msg"`
	Desc string             `json:"desc"`
	Data *UeserRoleResponse `json:"data"`
}

type UeserRoleResponse struct {
	ID              int    `json:"id"`
	WorkspaceID     int    `json:"workspaceId"`
	SubjectName     string `json:"subjectName"`
	Label           string `json:"label"`
	SubjectNameType string `json:"subjectNameType"`
	Role            string `json:"role"`
}

type TextMsg struct {
	LogID    int64    `json:"logId"`
	ToUser   string   `json:"touser"`
	MsgType  string   `json:"msgtype"`
	Richtext RichText `json:"richtext"`
}

type RichText struct {
	Content []RichBlock `json:"content"`
}

type RichBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Href  string `json:"href,omitempty"`
	Label string `json:"label,omitempty"`
}

type TextMsgResponse struct {
	RequestID string `json:"requestId"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
}

type TokenMsg struct {
	Identity  string `json:"identity"`
	ClientId  string `json:"client_id"`
	Timestamp string `json:"timestamp"`
	Random    string `json:"random"`
	Sign      string `json:"sign"`
}

type TokenMsgResponse struct {
	XRequestID string   `json:"X-Request-ID"`
	Code       int      `json:"code"`
	Message    string   `json:"msg"`
	Data       *MsgData `json:"data"`
}

type MsgData struct {
	AccessToken string `json:"token"`
	CreateAt    int64  `json:"createAt"`
	ExpireAt    int64  `json:"expireAt"`
}
