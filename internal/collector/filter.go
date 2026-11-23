package collector

import (
	"strings"

	gomastodon "github.com/mattn/go-mastodon"
)

func shouldCollectFactsFromStatus(status *gomastodon.Status, ignoreURLRequirement bool) bool {
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
