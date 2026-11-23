package mastodon

import (
	"context"
	"fmt"

	gomastodon "github.com/mattn/go-mastodon"
)

func (c *Client) UpdateProfile(ctx context.Context, note string) error {
	profile := &gomastodon.Profile{
		Note: &note,
	}
	_, err := c.client.AccountUpdate(ctx, profile)
	if err != nil && errorNotifier != nil {
		go errorNotifier("プロフィール更新エラー (Note)", err.Error())
	}
	return err
}

func (c *Client) UpdateProfileFields(ctx context.Context, fields []gomastodon.Field) error {
	profile := &gomastodon.Profile{
		Fields: &fields,
	}
	_, err := c.client.AccountUpdate(ctx, profile)
	if err != nil && errorNotifier != nil {
		go errorNotifier("プロフィール更新エラー (Fields)", err.Error())
	}
	return err
}

func (c *Client) GetAccountCurrentUser(ctx context.Context) (*gomastodon.Account, error) {
	return c.client.GetAccountCurrentUser(ctx)
}

func (c *Client) GetAccountByUsername(ctx context.Context, username string) (*gomastodon.Account, error) {
	results, err := c.client.AccountsSearch(ctx, username, 5)
	if err != nil {
		return nil, err
	}

	for _, account := range results {
		if account.Username == username || account.Acct == username {
			return account, nil
		}
	}

	return nil, fmt.Errorf("account not found (strict match failed): %s", username)
}

func (c *Client) FollowAccount(ctx context.Context, accountID string) error {
	_, err := c.client.AccountFollow(ctx, gomastodon.ID(accountID))
	return err
}

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
