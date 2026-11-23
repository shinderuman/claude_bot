package mastodon

import (
	"context"

	gomastodon "github.com/mattn/go-mastodon"
)

func (c *Client) StreamUser(ctx context.Context) (<-chan gomastodon.Event, error) {
	return c.client.StreamingUser(ctx)
}

func (c *Client) StreamPublic(ctx context.Context) (<-chan gomastodon.Event, error) {
	return c.client.StreamingPublic(ctx, false)
}

func ExtractStatusFromEvent(event gomastodon.Event) *gomastodon.Status {
	switch e := event.(type) {
	case *gomastodon.UpdateEvent:
		return e.Status
	case *gomastodon.NotificationEvent:
		return e.Notification.Status
	default:
		return nil
	}
}
