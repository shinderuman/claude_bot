package anthropic

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"claude_bot/internal/llm/provider"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

const (
	zAIAnthropicBaseURL     = "https://api.z.ai/api/anthropic"
	zAIRequestRateLimitCode = "1302"
	zAIServiceOverloadCode  = "1305"
)

var zAIErrorSummaries = map[string]string{
	"1000":                  "認証失敗",
	"1001":                  "認証ヘッダーなし",
	"1003":                  "認証トークン期限切れ",
	"1005":                  "二要素認証が必要",
	"1113":                  "残高不足または利用枠なし",
	"1200":                  "API呼び出し失敗",
	"1210":                  "APIパラメーター不正",
	"1211":                  "不明なモデル",
	"1212":                  "モデルが呼び出し方式に非対応",
	"1213":                  "パラメーター不足",
	"1214":                  "パラメーター値が無効",
	"1215":                  "併用できないパラメーターを指定",
	"1220":                  "アクセス権限なし",
	"1221":                  "API提供終了",
	"1222":                  "APIが存在しない",
	"1230":                  "API処理中にエラー",
	"1234":                  "ネットワークエラー",
	"1261":                  "プロンプトが長すぎる",
	"1301":                  "入力または生成内容がコンテンツ制限に抵触",
	zAIRequestRateLimitCode: "リクエスト頻度の上限に到達",
	zAIServiceOverloadCode:  "サービスが一時的に混雑",
	"1308":                  "利用枠の上限に到達",
	"1309":                  "GLM Coding Planの期限切れ",
	"1310":                  "週次または月次の利用枠超過",
	"1311":                  "現在のプランではモデルを利用不可",
	"1313":                  "公正利用ポリシーによりリクエスト頻度を制限",
	"1314":                  "法人向けプランの期限切れ",
	"1315":                  "APIキーは法人向けCoding Plan専用",
	"1316":                  "5時間枠超過・追加利用の残高不足",
	"1317":                  "7日間枠超過・追加利用の残高不足",
	"1318":                  "5時間枠超過・月額上限により追加利用不可",
	"1319":                  "7日間枠超過・月額上限により追加利用不可",
	"1320":                  "5時間枠超過",
	"1321":                  "7日間枠超過・月額上限により追加利用不可",
}

type zaiError struct {
	error
	title string
}

func (e *zaiError) NotificationTitle() string { return e.title }
func (e *zaiError) Unwrap() error             { return e.error }

func (c *Client) classifyZAIError(err error) error {
	var apiErr *anthropic.Error
	if c.config.AnthropicBaseURL != zAIAnthropicBaseURL || !errors.As(err, &apiErr) {
		return err
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(apiErr.RawJSON()), &body) != nil {
		return err
	}
	code := body.Error.Code
	if summary, ok := zAIErrorSummaries[code]; ok {
		err = &zaiError{error: err, title: "GLM " + code + " " + summary}
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || (code != zAIRequestRateLimitCode && code != zAIServiceOverloadCode) {
		return err
	}

	retryAfter := apiErr.Response.Header.Get("Retry-After")
	delay, parseErr := time.ParseDuration(retryAfter + "s")
	if parseErr != nil {
		if retryAt, dateErr := http.ParseTime(retryAfter); dateErr == nil {
			delay = time.Until(retryAt)
		}
	}
	return &provider.TemporaryRateLimitError{Err: err, RetryAfter: delay}
}
