// Package update checks GitHub releases and replaces the running binary.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repoAPI = "https://api.github.com/repos/4matthedev/metis-agent/releases/latest"
	// maxAssetBytes bounds a release archive download.
	maxAssetBytes = 256 * 1024 * 1024
)

// Release is the subset of the GitHub API response we need.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset is one downloadable release file.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Latest fetches the latest published release.
func Latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repoAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "metis-agent-updater")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("check for updates: GitHub returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	if rel.TagName == "" {
		return nil, errors.New("latest release has no tag")
	}
	return &rel, nil
}

// IsNewer reports whether latest is newer than current. Development builds
// ("dev", empty) are always considered older than a tagged release.
func IsNewer(current, latest string) bool {
	c, okC := parseVersion(current)
	l, okL := parseVersion(latest)
	if !okL {
		return false
	}
	if !okC {
		return true
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	if v == "" || v == "dev" || v == "latest" {
		return out, false
	}
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Result describes a successful update.
type Result struct {
	From     string
	To       string
	Asset    string
	Replaced string
}

// Update downloads the release matching the current platform and replaces the
// running executable. Progress is written to out.
func Update(ctx context.Context, current string, out io.Writer) (Result, error) {
	rel, err := Latest(ctx)
	if err != nil {
		return Result{}, err
	}
	if !IsNewer(current, rel.TagName) {
		return Result{From: current, To: rel.TagName}, nil
	}
	version := strings.TrimPrefix(rel.TagName, "v")
	assetName := fmt.Sprintf("metis-agent_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	var asset *Asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == assetName {
			asset = &rel.Assets[i]
			break
		}
	}
	if asset == nil {
		return Result{}, fmt.Errorf("release %s has no asset %s", rel.TagName, assetName)
	}
	if out != nil {
		fmt.Fprintf(out, "downloading %s\n", asset.Name)
	}
	data, err := download(ctx, asset.URL)
	if err != nil {
		return Result{}, err
	}
	if err := verifyChecksum(ctx, rel, asset.Name, data); err != nil {
		return Result{}, err
	}
	bin, err := extractBinary(data)
	if err != nil {
		return Result{}, err
	}
	target, err := os.Executable()
	if err != nil {
		return Result{}, fmt.Errorf("locate current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	if err := replaceExecutable(target, bin); err != nil {
		return Result{}, err
	}
	if out != nil {
		fmt.Fprintf(out, "updated %s: %s -> %s\n", target, current, rel.TagName)
	}
	return Result{From: current, To: rel.TagName, Asset: asset.Name, Replaced: target}, nil
}

func download(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "metis-agent-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: server returned %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if len(data) > maxAssetBytes {
		return nil, fmt.Errorf("release asset exceeds %d bytes", maxAssetBytes)
	}
	return data, nil
}

// verifyChecksum checks data against the release's SHA256SUMS when present.
func verifyChecksum(ctx context.Context, rel *Release, asset string, data []byte) error {
	sumsURL := ""
	for _, a := range rel.Assets {
		if a.Name == "SHA256SUMS" {
			sumsURL = a.URL
			break
		}
	}
	if sumsURL == "" {
		return nil
	}
	body, err := download(ctx, sumsURL)
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("SHA256SUMS does not list %s", asset)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	return nil
}

// extractBinary returns the metis-agent executable from a .tar.gz archive.
func extractBinary(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		base := filepath.Base(hdr.Name)
		if base != "metis-agent" && base != "metis-agent.exe" {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxAssetBytes))
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, errors.New("archive did not contain metis-agent")
}

// replaceExecutable writes bin over target, keeping a .old copy as a fallback.
func replaceExecutable(target string, bin []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".metis-update-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil && runtime.GOOS != "windows" {
		return err
	}
	old := target + ".old"
	_ = os.Remove(old)
	if err := os.Rename(target, old); err != nil {
		return fmt.Errorf("replace executable: %w (installed at %s)", err, target)
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Rename(old, target)
		return fmt.Errorf("install update: %w", err)
	}
	_ = os.Remove(old)
	return nil
}
