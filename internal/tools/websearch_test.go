package tools

import "testing"

func TestParseSearchHTMLDuckDuckGo(t *testing.T) {
	page := `<div class="result">
<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fdocs&amp;rut=abc">Example <b>Docs</b></a>
<a class="result__snippet" href="#">A useful <b>reference</b>.</a>
</div>
<div class="result">
<a rel="nofollow" class="result__a" href="https://go.dev/doc/">Go Documentation</a>
<div class="result__snippet">The Go programming language docs.</div>
</div>`
	got := parseSearchHTML(page, 5)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %#v", len(got), got)
	}
	if got[0].URL != "https://example.com/docs" {
		t.Fatalf("first URL = %q", got[0].URL)
	}
	if got[0].Title != "Example Docs" {
		t.Fatalf("first title = %q", got[0].Title)
	}
	if got[1].Snippet != "The Go programming language docs." {
		t.Fatalf("second snippet = %q", got[1].Snippet)
	}
}

func TestParseSearchHTMLLite(t *testing.T) {
	page := `<a class='result-link' href='https://example.org/a'>A &amp; B</a>
<td class='result-snippet'>Snippet here.</td>`
	got := parseSearchHTML(page, 5)
	if len(got) != 1 || got[0].Title != "A & B" || got[0].Snippet != "Snippet here." {
		t.Fatalf("unexpected results: %#v", got)
	}
}
