package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/version"
)

const (
	updateRepo        = "polyxmedia/mnemos"
	updateAPIBase     = "https://api.github.com"
	updateReleaseBase = "https://github.com"
	updateHTTPTimeout = 60 * time.Second
)

// updateTransport lets tests point fetch/download at an httptest server.
// Keeping it unexported and package-scoped avoids any production surface —
// unit tests flip it, restore it in a defer. updateSelfPath lets the end-
// to-end test target a fake binary rather than overwriting the test runner.
var (
	updateAPIOverride  = ""
	updateBaseOverride = ""
	updateHTTPClient   = &http.Client{Timeout: updateHTTPTimeout}
	updateSelfPath     = defaultSelfPath
)

func defaultSelfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real, nil
	}
	return exe, nil
}

func runUpdate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	var (
		yes   = fs.Bool("yes", false, "skip the confirmation prompt")
		pin   = fs.String("version", "", "install this version instead of latest (e.g. v0.2.0)")
		force = fs.Bool("force", false, "reinstall even if already on the target version")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	exe, err := updateSelfPath()
	if err != nil {
		return fmt.Errorf("locate current binary: %w", err)
	}

	target := strings.TrimSpace(*pin)
	if target == "" {
		latest, err := fetchLatestVersion(ctx)
		if err != nil {
			return fmt.Errorf("fetch latest release: %w", err)
		}
		target = latest
	}
	if !strings.HasPrefix(target, "v") {
		target = "v" + target
	}

	current := version.Version
	if !*force && !isNewerVersion(current, target) {
		fmt.Printf("mnemos %s is already up to date (latest: %s)\n", current, target)
		return nil
	}

	fmt.Printf("mnemos %s → %s\n", current, target)
	fmt.Printf("will download %s and replace %s\n\n", assetName(target, runtime.GOOS, runtime.GOARCH), exe)
	if !*yes {
		if !confirm(os.Stdin, os.Stdout) {
			fmt.Println("aborted.")
			return nil
		}
	}

	tmp, err := os.MkdirTemp("", "mnemos-update-*")
	if err != nil {
		return fmt.Errorf("tempdir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	archiveName := assetName(target, runtime.GOOS, runtime.GOARCH)
	archivePath := filepath.Join(tmp, archiveName)
	if err := downloadAsset(ctx, target, archiveName, archivePath); err != nil {
		return fmt.Errorf("download %s: %w", archiveName, err)
	}

	sumsPath := filepath.Join(tmp, "checksums.txt")
	if err := downloadAsset(ctx, target, "checksums.txt", sumsPath); err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}
	wantSum, ok, err := parseChecksum(sumsPath, archiveName)
	if err != nil {
		return fmt.Errorf("parse checksums: %w", err)
	}
	if !ok {
		return fmt.Errorf("checksums.txt has no entry for %s", archiveName)
	}
	if err := verifyChecksum(archivePath, wantSum); err != nil {
		return fmt.Errorf("verify %s: %w", archiveName, err)
	}

	binaryName := "mnemos"
	if runtime.GOOS == "windows" {
		binaryName = "mnemos.exe"
	}
	extracted := filepath.Join(tmp, binaryName)
	if err := extractBinary(archivePath, archiveFormat(archiveName), binaryName, extracted); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if err := replaceSelf(exe, extracted); err != nil {
		return fmt.Errorf("replace %s: %w", exe, err)
	}

	fmt.Println()
	fmt.Printf("installed mnemos %s at %s\n", target, exe)
	fmt.Println("restart your agent to pick up the new binary.")
	return nil
}

// assetName mirrors .goreleaser.yml's archive name template. Keep this in
// sync if the release config changes.
func assetName(tag, goos, goarch string) string {
	ver := strings.TrimPrefix(tag, "v")
	osTitle := strings.ToUpper(goos[:1]) + goos[1:]
	arch := goarch
	if goarch == "amd64" {
		arch = "x86_64"
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("mnemos_%s_%s_%s.%s", ver, osTitle, arch, ext)
}

func archiveFormat(name string) string {
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		return "tar.gz"
	case strings.HasSuffix(name, ".zip"):
		return "zip"
	default:
		return ""
	}
}

func fetchLatestVersion(ctx context.Context) (string, error) {
	base := updateAPIBase
	if updateAPIOverride != "" {
		base = updateAPIOverride
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", base, updateRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api: %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.TagName == "" {
		return "", errors.New("no tag_name in response")
	}
	return body.TagName, nil
}

func downloadAsset(ctx context.Context, tag, name, dst string) error {
	base := updateReleaseBase
	if updateBaseOverride != "" {
		base = updateBaseOverride
	}
	url := fmt.Sprintf("%s/%s/releases/download/%s/%s", base, updateRepo, tag, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

// parseChecksum scans a goreleaser-style checksums.txt looking for the line
// `<hex>  <filename>`. Returns the hex digest, found=true on match.
func parseChecksum(path, filename string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		// Whitespace-separated: digest then filename. Some tools emit a
		// single space, some emit two; Fields handles both.
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		if parts[len(parts)-1] == filename {
			return parts[0], true, nil
		}
	}
	return "", false, s.Err()
}

func verifyChecksum(path, wantHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, wantHex) {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, wantHex)
	}
	return nil
}

func extractBinary(archivePath, format, binaryName, dst string) error {
	switch format {
	case "tar.gz":
		return extractFromTarGz(archivePath, binaryName, dst)
	case "zip":
		return extractFromZip(archivePath, binaryName, dst)
	default:
		return fmt.Errorf("unknown archive format: %s", format)
	}
}

func extractFromTarGz(archivePath, binaryName, dst string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}
		return writeExtractedFile(dst, tr)
	}
	return fmt.Errorf("%s not found in archive", binaryName)
}

func extractFromZip(archivePath, binaryName, dst string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	for _, zf := range zr.File {
		if filepath.Base(zf.Name) != binaryName {
			continue
		}
		r, err := zf.Open()
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		return writeExtractedFile(dst, r)
	}
	return fmt.Errorf("%s not found in archive", binaryName)
}

func writeExtractedFile(dst string, src io.Reader) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, src)
	return err
}

// replaceSelf replaces dst with the binary at src. On unix the running
// executable's file handle stays valid after rename; on windows we shuffle
// the old binary aside first because the running .exe is otherwise locked.
func replaceSelf(dst, src string) error {
	if err := os.Chmod(src, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		backup := dst + ".old"
		_ = os.Remove(backup)
		if err := os.Rename(dst, backup); err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			_ = os.Rename(backup, dst) // best-effort recovery
			return err
		}
		return nil
	}
	// Unix: atomic rename is enough. The process keeps its mmap'd inode
	// open; new launches pick up the new file.
	return os.Rename(src, dst)
}

// isNewerVersion reports whether latest is strictly newer than current.
// "dev" on current is always treated as older — developers running local
// builds should always be offered the latest release. Malformed versions
// fall back to string inequality rather than erroring, because the update
// path should degrade gracefully.
func isNewerVersion(current, latest string) bool {
	if current == "dev" || current == "" {
		return true
	}
	c, cok := parseSemver(current)
	l, lok := parseSemver(latest)
	if !cok || !lok {
		return current != latest
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseSemver(s string) ([3]int, bool) {
	s = strings.TrimPrefix(s, "v")
	// Strip any -suffix (e.g. "-rc1", "-next").
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// confirm reads one line from in and returns true if the user typed y or yes
// (case-insensitive). Any other response — including EOF — cancels.
func confirm(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, "proceed? [y/N] ")
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	resp := strings.ToLower(strings.TrimSpace(line))
	return resp == "y" || resp == "yes"
}
