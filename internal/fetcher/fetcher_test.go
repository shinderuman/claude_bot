package fetcher

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchPageContentExtractsArticle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != UserAgent {
			t.Errorf("User-Agent = %q", r.UserAgent())
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><head><title>Fallback title</title><meta property="og:title" content="記事の見出し">
<meta property="og:description" content="記事の説明"></head><body><nav>navigation</nav>
<article>本文 <script>script</script><p>`+strings.Repeat("文", MaxContentLength)+`</p></article>
<footer>footer</footer></body></html>`)
	}))
	defer server.Close()

	url := server.URL + "/article"
	page, err := FetchPageContent(t.Context(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if page.URL != url || page.Title != "記事の見出し" || page.Description != "記事の説明" {
		t.Fatalf("metadata = %+v", page)
	}
	want := "本文 " + strings.Repeat("文", MaxContentLength-3) + ContentTruncationSuffix
	if page.Content != want {
		t.Fatalf("unexpected article excerpt (length %d, want %d)", len([]rune(page.Content)), len([]rune(want)))
	}
}
