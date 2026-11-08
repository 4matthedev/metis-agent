package tools

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	ddgResultRE  = regexp.MustCompile(`(?is)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRE = regexp.MustCompile(`(?is)<(?:a|div)[^>]+class="result__snippet"[^>]*>(.*?)</(?:a|div)>`)
	liteLinkRE   = regexp.MustCompile(`(?is)<a[^>]+class=['"]result-link['"][^>]+href=['"]([^'"]+)['"][^>]*>(.*?)</a>`)
	liteSnipRE   = regexp.MustCompile(`(?is)<td[^>]+class=['"]result-snippet['"][^>]*>(.*?)</td>`)
	tagRE        = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRE      = regexp.MustCompile(`\s+`)
)

// NewWebSearchTool searches the public web without an API key. It is
// read-only and therefore does not require approval.
func NewWebSearchTool() *Tool {
	return &Tool{
		Name: "web_search",
		Description: "Search the web for current information. Returns result titles, URLs, and snippets. " +
			"Use http_fetch afterwards to read a promising result in full.",
		Parameters: objectSchema(map[string]any{
			"query":       propSchema{Type: "string", Description: "Search query."},
			"max_results": propSchema{Type: "integer", Description: "Maximum results (default 5, max 10)."},
		}, "query"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			q := strings.TrimSpace(str(args["query"]))
			if q == "" {
				return "", fmt.Errorf("query is required")
			}
			limit := clamp(intArg(args, "max_results", 5), 1, 10)
			searchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()

			var lastErr error
			for _, endpoint := range []string{
				"https://html.duckduckgo.com/html/?q=" + url.QueryEscape(q),
				"https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(q),
			} {
				results, err := fetchSearchResults(searchCtx, endpoint, limit)
				if err != nil {
					lastErr = err
					continue
				}
				if len(results) > 0 {
					return formatSearchResults(q, results), nil
				}
			}
			if lastErr != nil {
				return "", fmt.Errorf("web search failed: %w", lastErr)
			}
			return fmt.Sprintf("no results for %q", q), nil
		},
	}
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

func fetchSearchResults(ctx context.Context, endpoint string, limit int) ([]searchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) metis-agent")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	client := newFetchClient(20)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search server returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}
	return parseSearchHTML(string(body), limit), nil
}

// parseSearchHTML accepts both DuckDuckGo result layouts.
func parseSearchHTML(page string, limit int) []searchResult {
	links := ddgResultRE.FindAllStringSubmatch(page, -1)
	snips := ddgSnippetRE.FindAllStringSubmatch(page, -1)
	if len(links) == 0 {
		links = liteLinkRE.FindAllStringSubmatch(page, -1)
		snips = liteSnipRE.FindAllStringSubmatch(page, -1)
	}
	var out []searchResult
	for i, m := range links {
		if len(out) >= limit {
			break
		}
		link := html.UnescapeString(m[1])
		if strings.HasPrefix(link, "//") {
			link = "https:" + link
		}
		// DuckDuckGo wraps outbound links in a redirect endpoint.
		if u, err := url.Parse(link); err == nil {
			if target := u.Query().Get("uddg"); target != "" {
				link = target
			}
		}
		r := searchResult{Title: cleanHTMLText(m[2]), URL: link}
		if i < len(snips) {
			r.Snippet = cleanHTMLText(snips[i][1])
		}
		if r.Title == "" || r.URL == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}

func cleanHTMLText(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}

func formatSearchResults(query string, results []searchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "results for %q\n", query)
	for i, r := range results {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
