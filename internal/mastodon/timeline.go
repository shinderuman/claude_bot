package mastodon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	gomastodon "github.com/mattn/go-mastodon"
)

func (c *Client) GetRootStatusID(ctx context.Context, currentStatus *gomastodon.Status) (string, error) {
	for currentStatus.InReplyToID != nil {
		parentStatus, err := c.GetStatus(ctx, fmt.Sprint(currentStatus.InReplyToID))
		if err != nil {
			var apiErr *gomastodon.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
				return string(currentStatus.ID), nil
			}
			return "", err
		}
		currentStatus = parentStatus
	}
	return string(currentStatus.ID), nil
}

func (c *Client) GetStatus(ctx context.Context, statusID string) (*gomastodon.Status, error) {
	id := gomastodon.ID(statusID)
	return c.client.GetStatus(ctx, id)
}

func (c *Client) fetchStatuses(ctx context.Context, accountID string, maxID gomastodon.ID, handler func([]*gomastodon.Status) (bool, error)) error {
	pg := &gomastodon.Pagination{
		MaxID: maxID,
		Limit: DefaultPageLimit,
	}

	apiCalls := 0

	for {
		if apiCalls >= MaxAPICallCount {
			log.Printf("投稿取得のAPI呼び出し上限: %d回", MaxAPICallCount)
			break
		}

		requestedMaxID := pg.MaxID
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
		if requestedMaxID == nextMaxID {
			break
		}
		pg = &gomastodon.Pagination{
			MaxID: nextMaxID,
			Limit: DefaultPageLimit,
		}
	}
	return nil
}

func (c *Client) GetStatusesByRange(ctx context.Context, accountID string, startID, endID string) ([]*gomastodon.Status, error) {
	var allStatuses []*gomastodon.Status

	if compareStatusIDs(startID, endID) > 0 {
		startID, endID = endID, startID
	}

	endStatus, err := c.GetStatus(ctx, endID)
	if err == nil && endStatus != nil {
		allStatuses = append(allStatuses, endStatus)
	} else {
		log.Printf("範囲末尾の投稿取得失敗: %v", err)
	}

	err = c.fetchStatuses(ctx, accountID, gomastodon.ID(endID), func(statuses []*gomastodon.Status) (bool, error) {
		for _, status := range statuses {
			if compareStatusIDs(string(status.ID), startID) < 0 {
				return false, nil
			}

			if compareStatusIDs(string(status.ID), endID) > 0 {
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

	sortStatusesByID(allStatuses)

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
			createdAt := status.CreatedAt

			if !createdAt.Before(startTime) && createdAt.Before(endTime) {
				if status.Reblog != nil {
					continue
				}

				allStatuses = append(allStatuses, status)
				count++
				if count >= MaxStatusCollectionCount {
					log.Printf("投稿取得上限: %d件", MaxStatusCollectionCount)
					return false, nil
				}
			}

			if createdAt.Before(startTime) {
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

	sortStatusesByID(allStatuses)

	return allStatuses, nil
}

func sortStatusesByID(statuses []*gomastodon.Status) {
	sort.Slice(statuses, func(i, j int) bool {
		return compareStatusIDs(string(statuses[i].ID), string(statuses[j].ID)) < 0
	})
}

func compareStatusIDs(left, right string) int {
	if len(left) != len(right) && strings.Trim(left+right, "0123456789") == "" {
		return len(left) - len(right)
	}
	return strings.Compare(left, right)
}
