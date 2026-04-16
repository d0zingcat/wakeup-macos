// Package updater checks GitHub Releases for new versions and applies
// binary updates with checksum verification and atomic replacement.
package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repo       = "d0zingcat/wakeup-macos"
	apiBaseURL = "https://api.github.com"
	plistName  = "com.wakeup.daemon"
)

// Release represents a GitHub release.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a downloadable file from a GitHub release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Updater checks for and applies updates from GitHub Releases.
type Updater struct {
	currentVersion string
	binaryPath     string
	httpClient     *http.Client
}

// New creates an Updater for the given current version and binary path.
func New(currentVersion, binaryPath string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		binaryPath:     binaryPath,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// CheckLatest fetches the latest release from GitHub.
func (u *Updater) CheckLatest(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", apiBaseURL, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "wakeup-updater")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &rel, nil
}

// Check returns the current version, latest version, and whether an update
// is available.
func (u *Updater) Check(ctx context.Context) (current, latest string, hasUpdate bool, err error) {
	rel, err := u.CheckLatest(ctx)
	if err != nil {
		return u.currentVersion, "", false, err
	}
	newer, err := IsNewer(u.currentVersion, rel.TagName)
	if err != nil {
		return u.currentVersion, rel.TagName, false, err
	}
	return u.currentVersion, rel.TagName, newer, nil
}

// Apply downloads, verifies, and installs the release, then restarts the daemon.
func (u *Updater) Apply(ctx context.Context, rel *Release) error {
	binaryAsset, checksumAsset, err := selectAssets(rel)
	if err != nil {
		return err
	}

	// Download tarball and checksums in sequence (checksums file is tiny).
	fmt.Printf("  Downloading %s...\n", binaryAsset.Name)
	tarballData, err := u.download(ctx, binaryAsset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("download binary: %w", err)
	}

	checksumData, err := u.download(ctx, checksumAsset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}

	// Verify checksum before touching any files.
	fmt.Println("  Verifying checksum...")
	if err := verifyChecksum(tarballData, checksumData, binaryAsset.Name); err != nil {
		return err
	}

	// Extract binary from tarball.
	binaryData, err := extractBinary(tarballData)
	if err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}

	// Atomic replace.
	fmt.Println("  Replacing binary...")
	if err := atomicReplace(binaryData, u.binaryPath); err != nil {
		return err
	}

	// Restart daemon.
	fmt.Println("  Restarting daemon...")
	if err := restartDaemon(); err != nil {
		return fmt.Errorf("restart daemon: %w", err)
	}

	return nil
}

// IsNewer returns true if latest is a newer semver than current.
// If current is "dev", it always returns true.
func IsNewer(current, latest string) (bool, error) {
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")

	if current == "dev" || current == "" {
		return true, nil
	}

	curParts, err := parseSemver(current)
	if err != nil {
		return false, fmt.Errorf("parse current version %q: %w", current, err)
	}
	latParts, err := parseSemver(latest)
	if err != nil {
		return false, fmt.Errorf("parse latest version %q: %w", latest, err)
	}

	for i := 0; i < 3; i++ {
		if latParts[i] > curParts[i] {
			return true, nil
		}
		if latParts[i] < curParts[i] {
			return false, nil
		}
	}
	return false, nil // equal
}

func parseSemver(v string) ([3]int, error) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("expected MAJOR.MINOR.PATCH, got %q", v)
	}
	var result [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, fmt.Errorf("non-numeric component %q: %w", p, err)
		}
		result[i] = n
	}
	return result, nil
}

func selectAssets(rel *Release) (binary, checksum *Asset, err error) {
	arch := runtime.GOARCH
	wantName := fmt.Sprintf("wakeup_darwin_%s.tar.gz", arch)

	for i := range rel.Assets {
		switch rel.Assets[i].Name {
		case wantName:
			binary = &rel.Assets[i]
		case "checksums.txt":
			checksum = &rel.Assets[i]
		}
	}

	if binary == nil {
		return nil, nil, fmt.Errorf("no asset found for darwin/%s (expected %s)", arch, wantName)
	}
	if checksum == nil {
		return nil, nil, fmt.Errorf("no checksums.txt found in release assets")
	}
	return binary, checksum, nil
}

func (u *Updater) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "wakeup-updater")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	return io.ReadAll(resp.Body)
}

func verifyChecksum(tarballData, checksumData []byte, assetName string) error {
	// Parse checksums.txt: each line is "<sha256hex>  <filename>"
	lines := strings.Split(strings.TrimSpace(string(checksumData)), "\n")
	var expectedHex string
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == assetName {
			expectedHex = parts[0]
			break
		}
	}
	if expectedHex == "" {
		return fmt.Errorf("checksum for %s not found in checksums.txt", assetName)
	}

	actual := sha256.Sum256(tarballData)
	actualHex := hex.EncodeToString(actual[:])

	if actualHex != expectedHex {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", assetName, expectedHex, actualHex)
	}
	return nil
}

func extractBinary(tarballData []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(tarballData))
	if err != nil {
		return nil, fmt.Errorf("gzip decompress: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}

		// goreleaser puts the binary at the root of the archive as "wakeup"
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "wakeup" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read binary from tar: %w", err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("binary 'wakeup' not found in archive")
}

func atomicReplace(binaryData []byte, targetPath string) error {
	dir := filepath.Dir(targetPath)

	// Write to temp file in same directory (same filesystem = atomic rename).
	tmp, err := os.CreateTemp(dir, ".wakeup-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(binaryData); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod: %w", err)
	}

	// Backup current binary.
	backupPath := targetPath + ".bak"
	os.Remove(backupPath) // remove old backup if exists
	if err := os.Rename(targetPath, backupPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("backup current binary: %w", err)
	}

	// Atomic rename new binary into place.
	if err := os.Rename(tmpPath, targetPath); err != nil {
		// Restore backup on failure.
		os.Rename(backupPath, targetPath)
		os.Remove(tmpPath)
		return fmt.Errorf("replace binary: %w", err)
	}

	return nil
}

func restartDaemon() error {
	cmd := exec.Command("launchctl", "kickstart", "-k", "system/"+plistName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl kickstart: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
