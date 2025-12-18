package mastodon

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	gomastodon "github.com/mattn/go-mastodon"
)

func (c *Client) GetRootStatusID(ctx context.Context, notification *gomastodon.Notification) string {
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

func (c *Client) convertToIDAndFetchStatus(ctx context.Context, inReplyToID any) (*gomastodon.Status, error) {
	statusID := fmt.Sprintf("%v", inReplyToID)
	return c.GetStatus(ctx, statusID)
}

// GetStatus retrieves a status by ID
func (c *Client) GetStatus(ctx context.Context, statusID string) (*gomastodon.Status, error) {
	id := gomastodon.ID(statusID)
	return c.client.GetStatus(ctx, id)
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
// ignoreURLRequirement: trueの場合、URLが含まれていなくても収集対象とする（Peerなど）
func ShouldCollectFactsFromStatus(status *gomastodon.Status, ignoreURLRequirement bool) bool {
	if status == nil {
		return false
	}

	// 公開範囲とアカウント属性によるフィルタリング
	switch status.Visibility {
	case "public":
		// Publicは許可
	case "unlisted":
		// UnlistedはBotの場合のみ許可
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

	// URL要件を無視する場合はここで許可
	if ignoreURLRequirement {
		return true
	}

	// 本文にURLパターンが含まれるかチェック
	// MediaAttachmentsやCardだけでは不十分(ハッシュタグなどもCardになるため)
	// 実際のhttp://またはhttps://を含む投稿のみ対象
	return strings.Contains(content, "http://") || strings.Contains(content, "https://")
}

// fetchStatuses iterates through account statuses with pagination using a callback
func (c *Client) fetchStatuses(ctx context.Context, accountID string, maxID gomastodon.ID, handler func([]*gomastodon.Status) (bool, error)) error {
	pg := &gomastodon.Pagination{
		MaxID: maxID,
		Limit: DefaultPageLimit,
	}

	apiCalls := 0

	for {
		if apiCalls >= MaxAPICallCount {
			log.Printf("API呼び出し回数制限(%d)に到達しました", MaxAPICallCount)
			break
		}

		statuses, err := c.client.GetAccountStatuses(ctx, gomastodon.ID(accountID), pg)
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
		pg = &gomastodon.Pagination{
			MaxID: nextMaxID,
			Limit: DefaultPageLimit,
		}
	}
	return nil
}

// GetStatusesByRange retrieves statuses within a specified ID range
func (c *Client) GetStatusesByRange(ctx context.Context, accountID string, startID, endID string) ([]*gomastodon.Status, error) {
	var allStatuses []*gomastodon.Status

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

	err = c.fetchStatuses(ctx, accountID, gomastodon.ID(endID), func(statuses []*gomastodon.Status) (bool, error) {
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
			allStatuses = append([]*gomastodon.Status{startStatus}, allStatuses...)
		}
	}

	return allStatuses, nil
}

// GetStatusesByDateRange retrieves statuses within a specified date range (JST)
func (c *Client) GetStatusesByDateRange(ctx context.Context, accountID string, startTime, endTime time.Time) ([]*gomastodon.Status, error) {
	var allStatuses []*gomastodon.Status
	count := 0

	err := c.fetchStatuses(ctx, accountID, "", func(statuses []*gomastodon.Status) (bool, error) {
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
func (c *Client) sortStatusesByID(statuses []*gomastodon.Status) {
	sort.Slice(statuses, func(i, j int) bool {
		return string(statuses[i].ID) < string(statuses[j].ID)
	})
}
