package mastodon

import (
	"context"
	"fmt"

	gomastodon "github.com/mattn/go-mastodon"
)

// UpdateProfile updates the account profile (note)
func (c *Client) UpdateProfile(ctx context.Context, note string) error {
	profile := &gomastodon.Profile{
		Note: &note,
	}
	_, err := c.client.AccountUpdate(ctx, profile)
	return err
}

// UpdateProfileFields updates the account profile fields
func (c *Client) UpdateProfileFields(ctx context.Context, fields []gomastodon.Field) error {
	// go-mastodon should handle mapping Fields to fields_attributes if supported
	profile := &gomastodon.Profile{
		Fields: &fields,
	}
	_, err := c.client.AccountUpdate(ctx, profile)
	return err
}

// GetAccountCurrentUser retrieves the authenticated user's account
func (c *Client) GetAccountCurrentUser(ctx context.Context) (*gomastodon.Account, error) {
	return c.client.GetAccountCurrentUser(ctx)
}

// GetAccountByUsername finds an account by username
func (c *Client) GetAccountByUsername(ctx context.Context, username string) (*gomastodon.Account, error) {
	// Use AccountsSearch to find the user
	// Limit is set higher to increase chance of finding the exact match among fuzzy results
	results, err := c.client.AccountsSearch(ctx, username, 5)
	if err != nil {
		return nil, err
	}

	for _, account := range results {
		// Strict matching: Check Username or Acct
		if account.Username == username || account.Acct == username {
			return account, nil
		}
	}

	return nil, fmt.Errorf("account not found (strict match failed): %s", username)
}

// FollowAccount follows the specified account
func (c *Client) FollowAccount(ctx context.Context, accountID string) error {
	_, err := c.client.AccountFollow(ctx, gomastodon.ID(accountID))
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
