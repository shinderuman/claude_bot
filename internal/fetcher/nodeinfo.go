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

// NodeInfoLinks represents the .well-known/nodeinfo response
type NodeInfoLinks struct {
	Links []NodeInfoLink `json:"links"`
}

type NodeInfoLink struct {
	Rel  string `json:"rel"`
	Href string `json:"href"`
}

// NodeInfo represents the NodeInfo document
type NodeInfo struct {
	Software NodeInfoSoftware `json:"software"`
}

type NodeInfoSoftware struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// IsFediverseServer checks if the given domain is a Fediverse server
func IsFediverseServer(domain string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), nodeInfoTimeout)
	defer cancel()

	// Get NodeInfo links
	wellKnownURL := fmt.Sprintf("https://%s/.well-known/nodeinfo", domain)
	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	var links NodeInfoLinks
	if err := json.Unmarshal(body, &links); err != nil {
		return false
	}

	// Find NodeInfo 2.0 or 2.1 link
	var nodeInfoURL string
	for _, link := range links.Links {
		// Check for standard NodeInfo schema URLs
		// e.g. http://nodeinfo.diaspora.software/ns/schema/2.0
		if strings.Contains(link.Rel, "ns/schema/2.") {
			nodeInfoURL = link.Href
			break
		}
	}

	if nodeInfoURL == "" {
		return false
	}

	// Get NodeInfo document
	req, err = http.NewRequestWithContext(ctx, "GET", nodeInfoURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	var nodeInfo NodeInfo
	if err := json.Unmarshal(body, &nodeInfo); err != nil {
		return false
	}

	// Check if software is Fediverse
	softwareName := strings.ToLower(nodeInfo.Software.Name)
	return fediverseSoftware[softwareName]
}
