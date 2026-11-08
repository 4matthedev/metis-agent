package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxRequestBody = 1024 * 1024

// NewHTTPFetchTool fetches a URL and returns its text body.
func NewHTTPFetchTool() *Tool {
	return &Tool{
		Name: "http_fetch",
		Description: "Perform an HTTP(S) request and return the status, content type, and text body. " +
			"Supports GET, POST, PUT, PATCH, DELETE, and HEAD, custom headers, and a text or JSON body. " +
			"Binary responses are rejected and output is capped.",
		Parameters: objectSchema(map[string]any{
			"url":         propSchema{Type: "string", Description: "Absolute http:// or https:// URL."},
			"method":      propSchema{Type: "string", Description: "HTTP method (default GET)."},
			"headers":     propSchema{Type: "object", Description: "Optional request headers as a JSON object."},
			"body":        propSchema{Type: "string", Description: "Optional request body (max 1 MB)."},
			"json":        propSchema{Type: "string", Description: "Optional JSON body; sets Content-Type to application/json."},
			"timeout_sec": propSchema{Type: "integer", Description: "Timeout in seconds (default 20, max 60)."},
			"max_bytes":   propSchema{Type: "integer", Description: "Maximum response bytes (default 524288, max 2097152)."},
		}, "url"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			rawURL := strings.TrimSpace(str(args["url"]))
			if rawURL == "" {
				return "", fmt.Errorf("url is required")
			}
			u, err := parseHTTPURL(rawURL)
			if err != nil {
				return "", err
			}
			if err := checkFetchHost(ctx, u.Hostname()); err != nil {
				return "", err
			}

			method := strings.ToUpper(defaultString(strings.TrimSpace(str(args["method"])), "GET"))
			switch method {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
			default:
				return "", fmt.Errorf("unsupported method %q", method)
			}
			body, contentType, err := requestBody(args, method)
			if err != nil {
				return "", err
			}
			headers, err := requestHeaders(args)
			if err != nil {
				return "", err
			}
			if contentType != "" && headers["Content-Type"] == "" {
				headers["Content-Type"] = contentType
			}

			timeoutSec := clamp(intArg(args, "timeout_sec", 20), 1, 60)
			maxBytes := clamp(intArg(args, "max_bytes", 512*1024), 1024, 2*1024*1024)
			reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
			defer cancel()

			var reader io.Reader
			if len(body) > 0 {
				reader = strings.NewReader(body)
			}
			req, err := http.NewRequestWithContext(reqCtx, method, u.String(), reader)
			if err != nil {
				return "", fmt.Errorf("build request: %w", err)
			}
			req.Header.Set("User-Agent", "metis-agent")
			if req.Header.Get("Accept") == "" {
				req.Header.Set("Accept", "text/html,application/json,text/plain;q=0.9,*/*;q=0.5")
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}

			client := newFetchClient(timeoutSec)
			resp, err := client.Do(req)
			if err != nil {
				if reqCtx.Err() != nil {
					return "", reqCtx.Err()
				}
				return "", fmt.Errorf("fetch %s: %w", rawURL, err)
			}
			defer resp.Body.Close()

			responseBody, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
			if err != nil {
				return "", fmt.Errorf("read response: %w", err)
			}
			truncated := false
			if len(responseBody) > maxBytes {
				responseBody = responseBody[:maxBytes]
				truncated = true
			}
			probe := responseBody
			if len(probe) > 8192 {
				probe = probe[:8192]
			}
			if strings.IndexByte(string(probe), 0) >= 0 {
				return "", fmt.Errorf("response is binary (%s)", resp.Header.Get("Content-Type"))
			}

			var b strings.Builder
			fmt.Fprintf(&b, "status: %s\n", resp.Status)
			if ct := resp.Header.Get("Content-Type"); ct != "" {
				fmt.Fprintf(&b, "content-type: %s\n", ct)
			}
			fmt.Fprintf(&b, "url: %s\n", resp.Request.URL.String())
			b.WriteString("---\n")
			b.WriteString(strings.ToValidUTF8(string(responseBody), ""))
			if truncated {
				fmt.Fprintf(&b, "\n... (response truncated at %d bytes)", maxBytes)
			}
			return b.String(), nil
		},
	}
}

// NewDownloadFileTool downloads a URL to a path.
func NewDownloadFileTool(workDir string) *Tool {
	return &Tool{
		Name: "download_file",
		Description: "Download an HTTP(S) URL to a file in the project or elsewhere. The download is written atomically. " +
			"Use it for releases, assets, fixtures, and large text or binary files. Set overwrite=true to replace an existing file.",
		Parameters: objectSchema(map[string]any{
			"url":         propSchema{Type: "string", Description: "Absolute http:// or https:// URL."},
			"path":        propSchema{Type: "string", Description: "Destination file relative to the project, or absolute."},
			"overwrite":   propSchema{Type: "boolean", Description: "Replace an existing destination file."},
			"timeout_sec": propSchema{Type: "integer", Description: "Timeout in seconds (default 60, max 600)."},
			"max_bytes":   propSchema{Type: "integer", Description: "Maximum download bytes (default 67108864, max 268435456)."},
		}, "url", "path"),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			rawURL := strings.TrimSpace(str(args["url"]))
			if rawURL == "" {
				return "", fmt.Errorf("url is required")
			}
			u, err := parseHTTPURL(rawURL)
			if err != nil {
				return "", err
			}
			if err := checkFetchHost(ctx, u.Hostname()); err != nil {
				return "", err
			}
			dest, err := resolvePath(workDir, str(args["path"]))
			if err != nil {
				return "", err
			}
			overwrite := boolArg(args, "overwrite")
			if _, err := os.Lstat(dest); err == nil && !overwrite {
				return "", fmt.Errorf("%s already exists (set overwrite=true)", relPath(workDir, dest))
			}
			timeoutSec := clamp(intArg(args, "timeout_sec", 60), 1, 600)
			maxBytes := clamp(intArg(args, "max_bytes", 64*1024*1024), 1024, 256*1024*1024)

			reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
			if err != nil {
				return "", fmt.Errorf("build request: %w", err)
			}
			req.Header.Set("User-Agent", "metis-agent")
			req.Header.Set("Accept", "*/*")

			client := newFetchClient(timeoutSec)
			resp, err := client.Do(req)
			if err != nil {
				if reqCtx.Err() != nil {
					return "", reqCtx.Err()
				}
				return "", fmt.Errorf("download %s: %w", rawURL, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return "", fmt.Errorf("download %s: server returned %s", rawURL, resp.Status)
			}
			if resp.ContentLength > int64(maxBytes) {
				return "", fmt.Errorf("download is too large (%s; max %s)", humanSize(resp.ContentLength), humanSize(int64(maxBytes)))
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return "", fmt.Errorf("create destination directory: %w", err)
			}
			tmp, err := os.CreateTemp(filepath.Dir(dest), ".metis-download-*")
			if err != nil {
				return "", fmt.Errorf("create temporary file: %w", err)
			}
			tmpName := tmp.Name()
			defer os.Remove(tmpName)
			written, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, int64(maxBytes)+1))
			if copyErr != nil {
				tmp.Close()
				return "", fmt.Errorf("download %s: %w", rawURL, copyErr)
			}
			if written > int64(maxBytes) {
				tmp.Close()
				return "", fmt.Errorf("download exceeded the %s limit", humanSize(int64(maxBytes)))
			}
			if err := tmp.Sync(); err != nil {
				tmp.Close()
				return "", fmt.Errorf("sync download: %w", err)
			}
			if err := tmp.Close(); err != nil {
				return "", fmt.Errorf("close download: %w", err)
			}
			mode := os.FileMode(0o644)
			if fi, err := os.Stat(dest); err == nil {
				mode = fi.Mode().Perm()
			}
			_ = os.Chmod(tmpName, mode)
			if err := os.Rename(tmpName, dest); err != nil {
				// Windows cannot replace an existing file with Rename.
				_ = os.Remove(dest)
				if err := os.Rename(tmpName, dest); err != nil {
					return "", fmt.Errorf("save %s: %w", relPath(workDir, dest), err)
				}
			}
			var b strings.Builder
			fmt.Fprintf(&b, "ok: downloaded %s to %s", humanSize(written), relPath(workDir, dest))
			if ct := resp.Header.Get("Content-Type"); ct != "" {
				fmt.Fprintf(&b, " (%s)", ct)
			}
			return b.String(), nil
		},
	}
}

func parseHTTPURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are supported")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("url has no host")
	}
	return u, nil
}

func requestBody(args map[string]any, method string) (string, string, error) {
	body := str(args["body"])
	jsonBody := str(args["json"])
	if body != "" && jsonBody != "" {
		return "", "", fmt.Errorf("provide either body or json, not both")
	}
	if jsonBody != "" {
		if !json.Valid([]byte(jsonBody)) {
			return "", "", fmt.Errorf("json body is not valid JSON")
		}
		body = jsonBody
	}
	if body == "" {
		return "", "", nil
	}
	if method == http.MethodGet || method == http.MethodHead {
		return "", "", fmt.Errorf("a request body is not allowed with %s", method)
	}
	if len(body) > maxRequestBody {
		return "", "", fmt.Errorf("request body is too large (%s; max %s)", humanSize(int64(len(body))), humanSize(maxRequestBody))
	}
	contentType := "text/plain; charset=utf-8"
	if jsonBody != "" || strings.HasPrefix(strings.TrimSpace(body), "{") || strings.HasPrefix(strings.TrimSpace(body), "[") {
		contentType = "application/json"
	}
	return body, contentType, nil
}

func requestHeaders(args map[string]any) (map[string]string, error) {
	out := map[string]string{}
	raw, ok := args["headers"].(map[string]any)
	if !ok {
		return out, nil
	}
	if len(raw) > 50 {
		return nil, fmt.Errorf("too many headers (max 50)")
	}
	for k, v := range raw {
		k = http.CanonicalHeaderKey(strings.TrimSpace(k))
		if k == "" {
			return nil, fmt.Errorf("header names cannot be empty")
		}
		if strings.ContainsAny(k, "\r\n:") {
			return nil, fmt.Errorf("invalid header name %q", k)
		}
		value, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("header %q must have a string value", k)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("header %q contains a newline", k)
		}
		out[k] = value
	}
	return out, nil
}

func newFetchClient(timeoutSec int) *http.Client {
	timeout := time.Duration(timeoutSec) * time.Second
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return checkFetchHost(req.Context(), req.URL.Hostname())
		},
	}
}

// checkFetchHost blocks link-local and unspecified addresses, which are a
// common target for SSRF attempts via prompt injection. Loopback and private
// addresses remain allowed so local dev servers and docs can be inspected.
func checkFetchHost(ctx context.Context, host string) error {
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	if ip := net.ParseIP(host); ip != nil {
		return validateFetchIP(ip)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, addr := range addrs {
		if err := validateFetchIP(addr.IP); err != nil {
			return err
		}
	}
	return nil
}

func validateFetchIP(ip net.IP) error {
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("refusing to fetch link-local or unspecified address %s", ip)
	}
	return nil
}
