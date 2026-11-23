package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const nodeInfoTimeout = 5 * time.Second

var fediverseSoftware = map[string]bool{
	"mastodon":   true,
	"misskey":    true,
	"pleroma":    true,
	"akkoma":     true,
	"calckey":    true,
	"firefish":   true,
	"gotosocial": true,
	"pixelfed":   true,
	"lemmy":      true,
	"kbin":       true,
	"peertube":   true,
	"friendica":  true,
	"hubzilla":   true,
	"diaspora":   true,
	"gnusocial":  true,
	"sharkey":    true,
	"iceshrimp":  true,
	"foundkey":   true,
	"cherrypick": true,
}

func IsFediverseServer(ctx context.Context, domain string) bool {
	ctx, cancel := context.WithTimeout(ctx, nodeInfoTimeout)
	defer cancel()

	var discovery struct {
		Links []struct{ Rel, Href string } `json:"links"`
	}
	if err := fetchNodeInfo(ctx, fmt.Sprintf("https://%s/.well-known/nodeinfo", domain), &discovery); err != nil {
		return false
	}

	for _, link := range discovery.Links {
		if !strings.Contains(link.Rel, "ns/schema/2.") {
			continue
		}
		var node struct {
			Software struct{ Name string } `json:"software"`
		}
		if err := fetchNodeInfo(ctx, link.Href, &node); err != nil {
			return false
		}
		return fediverseSoftware[strings.ToLower(node.Software.Name)]
	}
	return false
}

func fetchNodeInfo(ctx context.Context, url string, target any) error {
	if err := IsValidURL(url, nil); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("NodeInfo HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, MaxBodySize)).Decode(target)
}
