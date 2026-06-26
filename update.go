package main

import (
	"bufio"
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
	"strings"
	"time"
)

const updateRepo = "lmrisdal/lgctl"

// cmdUpdate replaces the running binary with the latest GitHub release asset for
// the current architecture. It mirrors what packaging/install.sh does for the
// binary (download + checksum + atomic temp-then-rename swap) but in pure Go so
// the single static binary can update itself with no extra tooling.
func cmdUpdate(force bool) error {
	asset := fmt.Sprintf("lgctl-linux-%s", runtime.GOARCH)
	switch runtime.GOARCH {
	case "amd64", "arm64":
	default:
		return fmt.Errorf("unsupported architecture %q; no release asset to update to", runtime.GOARCH)
	}

	rel, err := latestRelease(updateRepo)
	if err != nil {
		return err
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	current := strings.TrimPrefix(version, "v")
	if !force && latest == current {
		fmt.Printf("Already up to date (lgctl %s).\n", version)
		return nil
	}
	logf("updating lgctl %s -> %s", current, latest)

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current binary: %w", err)
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return fmt.Errorf("resolve current binary: %w", err)
	}

	binURL := assetURL(rel, asset)
	if binURL == "" {
		return fmt.Errorf("release %s has no asset %q", rel.TagName, asset)
	}
	sumsURL := assetURL(rel, "SHA256SUMS.txt")

	// Stage the new binary next to the target so the final rename is atomic (same
	// filesystem) and can't hit ETXTBSY while the old binary is still running.
	dir := filepath.Dir(self)
	tmp, err := os.CreateTemp(dir, ".lgctl-update-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("cannot write to %s (try: sudo lgctl update)", dir)
		}
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed into place

	sum, err := download(binURL, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}

	if sumsURL != "" {
		want, err := expectedSum(sumsURL, asset)
		if err != nil {
			return err
		}
		if want != "" && !strings.EqualFold(want, sum) {
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, sum, want)
		}
	}

	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, self); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("cannot replace %s (try: sudo lgctl update)", self)
		}
		return err
	}

	fmt.Printf("Updated lgctl to %s (%s).\n", latest, self)
	return nil
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func assetURL(rel *ghRelease, name string) string {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

func latestRelease(repo string) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query latest release: GitHub returned %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	if rel.TagName == "" {
		return nil, errors.New("no published release found")
	}
	return &rel, nil
}

// download copies the URL body into w and returns the hex-encoded sha256 of the
// bytes written, so callers can verify against the published SHA256SUMS.
func download(url string, w io.Writer) (string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned %s", resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// expectedSum fetches SHA256SUMS.txt and returns the checksum for the named
// asset, or "" if the file doesn't list it.
func expectedSum(url, asset string) (string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch checksums: GitHub returned %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		// Lines are "<hex>␠␠<filename>" as produced by sha256sum; the name may
		// carry a leading path component, so match on the basename.
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && filepath.Base(fields[1]) == asset {
			return fields[0], nil
		}
	}
	return "", sc.Err()
}

var httpClient = &http.Client{Timeout: 60 * time.Second}
