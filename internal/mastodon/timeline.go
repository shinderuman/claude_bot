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

func (c *Client) GetStatus(ctx context.Context, statusID string) (*gomastodon.Status, error) {
	id := gomastodon.ID(statusID)
	return c.client.GetStatus(ctx, id)
}

func ShouldCollectFactsFromStatus(status *gomastodon.Status, ignoreURLRequirement bool) bool {
	if status == nil {
		return false
	}

	switch status.Visibility {
	case "public":
	case "unlisted":
		if !status.Account.Bot {
			return false
		}
	default:
		return false
	}

	content := string(status.Content)

	if strings.Contains(content, "@") {
		return false
	}

	if ignoreURLRequirement {
		return true
	}

	return strings.Contains(content, "http://") || strings.Contains(content, "https://")
}

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

		nextMaxID := statuses[len(statuses)-1].ID
		pg = &gomastodon.Pagination{
			MaxID: nextMaxID,
			Limit: DefaultPageLimit,
		}
	}
	return nil
}

func (c *Client) GetStatusesByRange(ctx context.Context, accountID string, startID, endID string) ([]*gomastodon.Status, error) {
	var allStatuses []*gomastodon.Status

	if startID > endID {
		startID, endID = endID, startID
	}

	endStatus, err := c.GetStatus(ctx, endID)
	if err == nil && endStatus != nil {
		allStatuses = append(allStatuses, endStatus)
	} else {
		log.Printf("終了IDのステータス取得失敗（削除されている可能性があります）: %v", err)
	}

	err = c.fetchStatuses(ctx, accountID, gomastodon.ID(endID), func(statuses []*gomastodon.Status) (bool, error) {
		for _, status := range statuses {
			if string(status.ID) < startID {
				return false, nil
			}

			if string(status.ID) > endID {
				continue
			}

			if status.Reblog != nil {
				continue
			}

			allStatuses = append(allStatuses, status)
		}

		if len(allStatuses) >= SafetyLimitCount {
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return nil, err
	}

	c.sortStatusesByID(allStatuses)

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

func (c *Client) GetStatusesByDateRange(ctx context.Context, accountID string, startTime, endTime time.Time) ([]*gomastodon.Status, error) {
	var allStatuses []*gomastodon.Status
	count := 0

	err := c.fetchStatuses(ctx, accountID, "", func(statuses []*gomastodon.Status) (bool, error) {
		for _, status := range statuses {
			createdAtJST := status.CreatedAt.In(startTime.Location())

			if createdAtJST.After(startTime) && createdAtJST.Before(endTime) {
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

			if createdAtJST.Before(startTime) {
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

	c.sortStatusesByID(allStatuses)

	return allStatuses, nil
}

func (c *Client) sortStatusesByID(statuses []*gomastodon.Status) {
	sort.Slice(statuses, func(i, j int) bool {
		return string(statuses[i].ID) < string(statuses[j].ID)
	})
}
