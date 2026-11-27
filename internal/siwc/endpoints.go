package siwc

import (
	"net/http"
	"time"
)

const (
	OfficialAuthBaseURL = "https://auth.openai.com"
	OfficialAPIBaseURL  = "https://api.openai.com/v1"

	AuthorizePath   = "/api/accounts/authorize"
	TokenPath       = "/api/accounts/oauth/token"
	DiscoveryPath   = "/.well-known/openid-configuration"
	ModelsPath      = "/models"
	ResponsesPath   = "/responses"
	CallbackPath    = "/auth/callback"
	DefaultPort     = 1455
	LoopbackHost    = "127.0.0.1"
	DynamicClientID = "dynamic_agent_client"
	AgentNameHint   = "claude_bot"

	RequestScopes  = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	PlanUsageScope = "chatgpt.tokens.use.direct"
)

type Endpoints struct {
	AuthBaseURL string
	APIBaseURL  string
}

func OfficialEndpoints() Endpoints {
	return Endpoints{AuthBaseURL: OfficialAuthBaseURL, APIBaseURL: OfficialAPIBaseURL}
}

func (e Endpoints) AuthorizeURL() string {
	return e.AuthBaseURL + AuthorizePath
}

func (e Endpoints) TokenURL() string {
	return e.AuthBaseURL + TokenPath
}

func (e Endpoints) DiscoveryURL() string {
	return e.AuthBaseURL + DiscoveryPath
}

func (e Endpoints) Issuer() string {
	return e.AuthBaseURL
}

func (e Endpoints) Resource() string {
	return e.APIBaseURL
}

func (e Endpoints) ModelsURL() string {
	return e.APIBaseURL + ModelsPath
}

func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
