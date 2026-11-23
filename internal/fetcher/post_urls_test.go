package fetcher

import (
	"slices"
	"testing"
)

func TestExtractPostURLsPreservesLinkedURLs(t *testing.T) {
	content := `<p><a href="https://example.com/記事?x=1&amp;y=2">https://example.com/truncated...</a>
<a href="https://example.com/tags/go">tag</a> https://other.example/post.
<a href="https://example.com/記事?x=1&amp;y=2">duplicate</a></p>`
	want := []string{"https://example.com/記事?x=1&y=2", "https://other.example/post"}
	if got := ExtractPostURLs(content); !slices.Equal(got, want) {
		t.Fatalf("ExtractPostURLs = %q, want %q", got, want)
	}
}
