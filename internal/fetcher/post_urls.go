package fetcher

import (
	"strings"

	"golang.org/x/net/html"
	"mvdan.cc/xurls/v2"
)

var textURLs = xurls.Strict()

func ExtractPostURLs(content string) []string {
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return nil
	}

	var links []string
	for node := range doc.Descendants() {
		switch {
		case node.Type == html.ElementNode && node.Data == "a":
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					links = append(links, attr.Val)
				}
			}
		case node.Type == html.TextNode:
			insideLink := false
			for parent := range node.Ancestors() {
				if parent.Data == "a" {
					insideLink = true
					break
				}
			}
			if !insideLink {
				links = append(links, textURLs.FindAllString(node.Data, -1)...)
			}
		}
	}

	var urls []string
	seen := make(map[string]struct{}, len(links))
	for _, link := range links {
		if _, exists := seen[link]; exists || IsValidURL(link, nil) != nil || IsNoiseURL(link) {
			continue
		}
		seen[link] = struct{}{}
		urls = append(urls, link)
	}
	return urls
}
