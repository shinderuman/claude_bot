package mastodon

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"claude_bot/internal/model"

	"github.com/mattn/go-mastodon"
	"golang.org/x/net/html"
)

type Config struct {
	Server           string
	AccessToken      string
	BotUsername      string
	AllowRemoteUsers bool
	MaxPostChars     int
}

const (
	// DefaultPageLimit はMastodon APIの1ページあたりのデフォルト取得数
	DefaultPageLimit = 40

	// SafetyLimitCount はID範囲取得時の最大取得件数の安全装置
	SafetyLimitCount = 100

	// MaxStatusCollectionCount は日付範囲取得時の最大収集件数
	MaxStatusCollectionCount = 500

	// MaxAPICallCount は日付範囲取得時の最大API呼び出し回数（無限ループ防止）
	MaxAPICallCount = 50

	// SplitPostDelay は分割投稿時の待機時間
	SplitPostDelay = 200 * time.Millisecond
)

type Client struct {
	client *mastodon.Client
	config Config
}

func NewClient(cfg Config) *Client {
	c := mastodon.NewClient(&mastodon.Config{
		Server:      cfg.Server,
		AccessToken: cfg.AccessToken,
	})
	return &Client{
		client: c,
		config: cfg,
	}
}

// StreamUser はホームタイムラインのストリーミングを開始し、イベントをチャネルに送信します
func (c *Client) StreamUser(ctx context.Context, eventChan chan<- mastodon.Event) {
	events, err := c.client.StreamingUser(ctx)
	if err != nil {
		log.Printf("ユーザーストリーミング接続エラー: %v", err)
		return
	}

	log.Println("ユーザーストリーミング接続成功")

	for event := range events {
		eventChan <- event
	}

	log.Println("ユーザーストリーミング接続が切断されました")
}

func (c *Client) GetRootStatusID(ctx context.Context, notification *mastodon.Notification) string {
	if notification.Status.InReplyToID == nil {
		return string(notification.Status.ID)
	}

	currentStatus := notification.Status

	for currentStatus.InReplyToID != nil {
		parentStatus, err := c.convertToIDAndFetchStatus(ctx, currentStatus.InReplyToID)
		if err != nil {
			return string(notification.Status.ID)
		}
		currentStatus = parentStatus
	}

	return string(currentStatus.ID)
}

func (c *Client) convertToIDAndFetchStatus(ctx context.Context, inReplyToID any) (*mastodon.Status, error) {
	statusID := fmt.Sprintf("%v", inReplyToID)
	return c.GetStatus(ctx, statusID)
}

// GetStatus retrieves a status by ID
func (c *Client) GetStatus(ctx context.Context, statusID string) (*mastodon.Status, error) {
	id := mastodon.ID(statusID)
	return c.client.GetStatus(ctx, id)
}

// Message extraction and HTML parsing

func (c *Client) ExtractUserMessage(notification *mastodon.Notification) string {
	content, _, _ := c.ExtractContentFromStatus(notification.Status)
	return content
}

// ExtractContentFromStatus extracts clean text content and images from a status
func (c *Client) ExtractContentFromStatus(status *mastodon.Status) (string, []model.Image, error) {
	content := stripHTML(string(status.Content))
	words := strings.Fields(content)

	var filtered []string
	for _, word := range words {
		if !strings.HasPrefix(word, "@") {
			filtered = append(filtered, word)
		}
	}

	text := strings.Join(filtered, " ")

	var images []model.Image
	for _, attachment := range status.MediaAttachments {
		if attachment.Type == "image" {
			base64Image, mediaType, err := c.downloadImage(attachment.URL)
			if err != nil {
				log.Printf("画像ダウンロードエラー (%s): %v", attachment.URL, err)
				continue
			}
			images = append(images, model.Image{
				Data:      base64Image,
				MediaType: mediaType,
			})
		}
	}

	return text, images, nil
}

func (c *Client) downloadImage(url string) (string, string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	// メディアタイプ判定
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return "", "", fmt.Errorf("not an image: %s", mimeType)
	}

	return base64.StdEncoding.EncodeToString(data), mimeType, nil
}

func stripHTML(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return htmlStr
	}

	var buf strings.Builder
	extractText(doc, &buf)
	return buf.String()
}

// StripHTML exposes stripHTML as a public method
func (c *Client) StripHTML(htmlStr string) string {
	return stripHTML(htmlStr)
}

func extractText(n *html.Node, buf *strings.Builder) {
	if n.Type == html.TextNode {
		buf.WriteString(n.Data)
	} else if n.Type == html.ElementNode && n.Data == "br" {
		buf.WriteString("\n")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractText(c, buf)
	}
}

func (c *Client) BuildMention(acct string) string {
	return "@" + acct + " "
}

func (c *Client) PostResponseWithSplit(ctx context.Context, inReplyToID, mention, response, visibility string) ([]*mastodon.Status, error) {
	parts := splitResponse(response, mention, c.config.MaxPostChars)

	var postedStatuses []*mastodon.Status
	currentReplyID := inReplyToID
	for i, part := range parts {
		// 2投稿目以降は待機して投稿順序を保証
		if i > 0 {
			time.Sleep(SplitPostDelay)
		}

		content := mention + part
		status, err := c.postReply(ctx, currentReplyID, content, visibility)
		if err != nil {
			log.Printf("分割投稿失敗 (%d/%d): %v", i+1, len(parts), err)
			return postedStatuses, err
		}
		currentReplyID = string(status.ID)
		postedStatuses = append(postedStatuses, status)
	}

	return postedStatuses, nil
}

// PostResponseWithMedia posts a response with media attachment
func (c *Client) PostResponseWithMedia(ctx context.Context, inReplyToID, mention, response, visibility, mediaPath string) (string, error) {
	// Upload media
	attachment, err := c.client.UploadMedia(ctx, mediaPath)
	if err != nil {
		log.Printf("メディアアップロードエラー: %v", err)
		return "", err
	}

	// Post with media
	fullResponse := mention + " " + response
	toot := &mastodon.Toot{
		Status:      fullResponse,
		InReplyToID: mastodon.ID(inReplyToID),
		Visibility:  visibility,
		MediaIDs:    []mastodon.ID{attachment.ID},
	}

	status, err := c.client.PostStatus(ctx, toot)
	if err != nil {
		log.Printf("投稿エラー (Media): %v", err)
		if strings.Contains(err.Error(), "422") {
			log.Printf("⚠️ 422 Error detected. Content length: %d", len([]rune(fullResponse)))
			log.Printf("Rejected Content: %s", fullResponse)
		}
		return "", err
	}

	return string(status.ID), nil
}

func (c *Client) postReply(ctx context.Context, inReplyToID, content, visibility string) (*mastodon.Status, error) {
	toot := &mastodon.Toot{
		Status:      content,
		InReplyToID: mastodon.ID(inReplyToID),
		Visibility:  visibility,
	}

	status, err := c.client.PostStatus(ctx, toot)
	if err != nil {
		log.Printf("投稿エラー: %v", err)
		if strings.Contains(err.Error(), "422") {
			log.Printf("⚠️ 422 Error detected (Reply). Content length: %d", len([]rune(content)))
			log.Printf("Rejected Content: %s", content)
		}
		return nil, err
	}

	return status, nil
}

// PostStatus posts a new status (not a reply)
// PostStatus posts a new status (not a reply)
func (c *Client) PostStatus(ctx context.Context, content, visibility string) (*mastodon.Status, error) {
	toot := &mastodon.Toot{
		Status:     content,
		Visibility: visibility,
	}

	status, err := c.client.PostStatus(ctx, toot)
	if err != nil {
		log.Printf("投稿エラー (Status): %v", err)
		if strings.Contains(err.Error(), "422") {
			log.Printf("⚠️ 422 Error detected (Status). Content length: %d", len([]rune(content)))
			log.Printf("Rejected Content: %s", content)
		}
		return nil, err
	}
	return status, nil
}

// FollowAccount follows the specified account
func (c *Client) FollowAccount(ctx context.Context, accountID string) error {
	_, err := c.client.AccountFollow(ctx, mastodon.ID(accountID))
	return err
}

// IsFollowing checks if the bot is following the specified account
func (c *Client) IsFollowing(ctx context.Context, accountID string) (bool, error) {
	relationships, err := c.client.GetAccountRelationships(ctx, []string{accountID})
	if err != nil {
		return false, err
	}
	if len(relationships) == 0 {
		return false, fmt.Errorf("no relationship found for account %s", accountID)
	}
	return relationships[0].Following, nil
}

// Response splitting

func splitResponse(response, mention string, maxChars int) []string {
	mentionLen := len([]rune(mention))
	maxContentLen := maxChars - mentionLen

	runes := []rune(response)
	if len(runes) <= maxContentLen {
		return []string{response}
	}

	return splitByNewline(runes, maxContentLen)
}

func splitByNewline(runes []rune, maxLen int) []string {
	var parts []string
	start := 0

	for start < len(runes) {
		end := start + maxLen
		if end >= len(runes) {
			parts = append(parts, string(runes[start:]))
			break
		}

		splitPos := findLastNewline(runes, start, end)
		if splitPos == -1 {
			splitPos = end
		}

		parts = append(parts, string(runes[start:splitPos]))
		start = skipLeadingNewlines(runes, splitPos)
	}

	return parts
}

func findLastNewline(runes []rune, start, end int) int {
	for i := end - 1; i > start; i-- {
		if runes[i] == '\n' {
			return i
		}
	}
	return -1
}

func skipLeadingNewlines(runes []rune, pos int) int {
	for pos < len(runes) && runes[pos] == '\n' {
		pos++
	}
	return pos
}

func (c *Client) FormatCard(card *mastodon.Card) string {
	var sb strings.Builder
	sb.WriteString("\n\n[参照URL情報]\n")
	sb.WriteString(fmt.Sprintf("URL: %s\n", card.URL))
	if card.Title != "" {
		sb.WriteString(fmt.Sprintf("タイトル: %s\n", card.Title))
	}
	if card.Description != "" {
		sb.WriteString(fmt.Sprintf("説明: %s\n", card.Description))
	}
	return sb.String()
}

// StreamPublic は連合タイムラインのストリーミングを開始し、イベントをチャネルに送信します
func (c *Client) StreamPublic(ctx context.Context, eventChan chan<- mastodon.Event) {
	events, err := c.client.StreamingPublic(ctx, false) // false = 連合タイムライン
	if err != nil {
		log.Printf("連合ストリーミング接続エラー: %v", err)
		return
	}

	log.Println("連合ストリーミング接続成功")

	for event := range events {
		eventChan <- event
	}

	log.Println("連合ストリーミング接続が切断されました")
}

// ExtractStatusFromEvent はイベントから Status を抽出します
func ExtractStatusFromEvent(event mastodon.Event) *mastodon.Status {
	switch e := event.(type) {
	case *mastodon.UpdateEvent:
		return e.Status
	case *mastodon.NotificationEvent:
		return e.Notification.Status
	default:
		return nil
	}
}

// ShouldCollectFactsFromStatus はファクト収集対象の投稿かを判定します
// ポリシー:
// - Public: 収集許可（Bot/人間問わず）
// - Unlisted: Botのみ収集許可（人間のUnlistedは除外）
// - Private/Direct: 収集不可
//
// 共通条件:
// - 本文に実際のURLを含む(http://またはhttps://)
// - メンションを含まない
func ShouldCollectFactsFromStatus(status *mastodon.Status) bool {
	if status == nil {
		return false
	}

	// 1. 公開範囲とアカウント属性によるフィルタリング
	switch status.Visibility {
	case "public":
		// Publicは許可
	case "unlisted":
		// UnlistedはBotの場合のみ許可（人間のUnlistedは恐らく私信や独り言なので除外）
		if !status.Account.Bot {
			return false
		}
	default:
		// Private, Direct は収集不可
		return false
	}

	content := string(status.Content)

	// メンションを含む投稿は除外
	if strings.Contains(content, "@") {
		return false
	}

	// 本文にURLパターンが含まれるかチェック
	// MediaAttachmentsやCardだけでは不十分(ハッシュタグなどもCardになるため)
	// 実際のhttp://またはhttps://を含む投稿のみ対象
	return strings.Contains(content, "http://") || strings.Contains(content, "https://")
}

// fetchStatuses iterates through account statuses with pagination using a callback
func (c *Client) fetchStatuses(ctx context.Context, accountID string, maxID mastodon.ID, handler func([]*mastodon.Status) (bool, error)) error {
	pg := &mastodon.Pagination{
		MaxID: maxID,
		Limit: DefaultPageLimit,
	}

	apiCalls := 0

	for {
		if apiCalls >= MaxAPICallCount {
			log.Printf("API呼び出し回数制限(%d)に到達しました", MaxAPICallCount)
			break
		}

		statuses, err := c.client.GetAccountStatuses(ctx, mastodon.ID(accountID), pg)
		apiCalls++

		if err != nil {
			return fmt.Errorf("failed to get account statuses: %w", err)
		}

		if len(statuses) == 0 {
			break
		}

		shouldContinue, err := handler(statuses)
		if err != nil {
			return err
		}
		if !shouldContinue {
			break
		}

		// 次のページへ
		nextMaxID := statuses[len(statuses)-1].ID
		pg = &mastodon.Pagination{
			MaxID: nextMaxID,
			Limit: DefaultPageLimit,
		}
	}
	return nil
}

// GetStatusesByRange retrieves statuses within a specified ID range
func (c *Client) GetStatusesByRange(ctx context.Context, accountID string, startID, endID string) ([]*mastodon.Status, error) {
	var allStatuses []*mastodon.Status

	// IDの大小関係を確認し、必要なら入れ替える（startID < endID）
	if startID > endID {
		startID, endID = endID, startID
	}

	// endIDのステータス自体も含めるため、まずはendIDのステータスを取得
	endStatus, err := c.GetStatus(ctx, endID)
	if err == nil && endStatus != nil {
		allStatuses = append(allStatuses, endStatus)
	} else {
		log.Printf("終了IDのステータス取得失敗（削除されている可能性があります）: %v", err)
	}

	err = c.fetchStatuses(ctx, accountID, mastodon.ID(endID), func(statuses []*mastodon.Status) (bool, error) {
		for _, status := range statuses {
			// IDがstartIDより小さい（古い）場合は終了
			if string(status.ID) < startID {
				return false, nil
			}

			// IDがendIDより大きい（新しい）場合はスキップ（通常MaxID指定ならありえないが念のため）
			if string(status.ID) > endID {
				continue
			}

			// リブートは除外
			if status.Reblog != nil {
				continue
			}

			allStatuses = append(allStatuses, status)
		}

		// 安全装置
		if len(allStatuses) >= SafetyLimitCount {
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return nil, err
	}

	// ID順（古い順）にソート
	c.sortStatusesByID(allStatuses)

	// startIDのステータスが含まれていない場合、個別に取得して追加
	hasStartID := false
	for _, s := range allStatuses {
		if string(s.ID) == startID {
			hasStartID = true
			break
		}
	}

	if !hasStartID {
		startStatus, err := c.GetStatus(ctx, startID)
		if err == nil && startStatus != nil {
			allStatuses = append([]*mastodon.Status{startStatus}, allStatuses...)
		}
	}

	return allStatuses, nil
}

// GetStatusesByDateRange retrieves statuses within a specified date range (JST)
func (c *Client) GetStatusesByDateRange(ctx context.Context, accountID string, startTime, endTime time.Time) ([]*mastodon.Status, error) {
	var allStatuses []*mastodon.Status
	count := 0

	err := c.fetchStatuses(ctx, accountID, "", func(statuses []*mastodon.Status) (bool, error) {
		for _, status := range statuses {
			// UTCからJSTに変換して比較
			createdAtJST := status.CreatedAt.In(startTime.Location())

			// 時刻範囲でフィルタリング
			if createdAtJST.After(startTime) && createdAtJST.Before(endTime) {
				// リブートは除外
				if status.Reblog != nil {
					continue
				}

				allStatuses = append(allStatuses, status)
				count++
				if count >= MaxStatusCollectionCount {
					log.Printf("最大取得件数(%d)に到達しました", MaxStatusCollectionCount)
					return false, nil
				}
			}

			// endTimeより古い投稿に到達したら終了
			if createdAtJST.Before(startTime) {
				// 固定ツイート（Pinned）の場合はスキップして続行
				isPinned, ok := status.Pinned.(bool)
				if ok && isPinned {
					continue
				}
				return false, nil
			}
		}
		return true, nil
	})

	if err != nil {
		return nil, err
	}

	// ID順（古い順）にソート
	c.sortStatusesByID(allStatuses)

	return allStatuses, nil
}

// sortStatusesByID sorts statuses by ID in ascending order (older to newer)
func (c *Client) sortStatusesByID(statuses []*mastodon.Status) {
	sort.Slice(statuses, func(i, j int) bool {
		return string(statuses[i].ID) < string(statuses[j].ID)
	})
}
