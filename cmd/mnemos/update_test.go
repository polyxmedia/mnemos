package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/version"
)

func TestAssetName(t *testing.T) {
	// Mirror .goreleaser.yml. If the template changes, this test must
	// change in lockstep — that's the whole point.
	cases := []struct {
		tag, goos, goarch, want string
	}{
		{"v0.2.0", "linux", "amd64", "mnemos_0.2.0_Linux_x86_64.tar.gz"},
		{"v0.2.0", "linux", "arm64", "mnemos_0.2.0_Linux_arm64.tar.gz"},
		{"v0.3.1", "darwin", "amd64", "mnemos_0.3.1_Darwin_x86_64.tar.gz"},
		{"v0.3.1", "darwin", "arm64", "mnemos_0.3.1_Darwin_arm64.tar.gz"},
		{"v1.0.0", "windows", "amd64", "mnemos_1.0.0_Windows_x86_64.zip"},
		{"0.2.0", "linux", "amd64", "mnemos_0.2.0_Linux_x86_64.tar.gz"},
	}
	for _, c := range cases {
		got := assetName(c.tag, c.goos, c.goarch)
		if got != c.want {
			t.Errorf("assetName(%q, %q, %q) = %q, want %q", c.tag, c.goos, c.goarch, got, c.want)
		}
	}
}

func TestArchiveFormat(t *testing.T) {
	cases := map[string]string{
		"mnemos_0.2.0_Linux_x86_64.tar.gz":  "tar.gz",
		"mnemos_0.2.0_Windows_x86_64.zip":   "zip",
		"mnemos_0.2.0_Darwin_arm64.unknown": "",
	}
	for in, want := range cases {
		if got := archiveFormat(in); got != want {
			t.Errorf("archiveFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.2.0", false},
		{"v0.2.0", "v0.1.0", false},
		{"v0.2.0", "v0.2.1", true},
		{"v1.0.0", "v0.9.9", false},
		{"v0.2.0", "v0.10.0", true}, // numeric compare, not lexical
		{"dev", "v0.2.0", true},
		{"", "v0.2.0", true},
		{"v0.2.0-rc1", "v0.2.0", false}, // strips prerelease suffix
		{"garbage", "v0.2.0", true},     // malformed current differs → treat as older
	}
	for _, c := range cases {
		got := isNewerVersion(c.current, c.latest)
		if got != c.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	v, ok := parseSemver("v1.2.3")
	if !ok || v != [3]int{1, 2, 3} {
		t.Errorf("v1.2.3: got %v ok=%v", v, ok)
	}
	v, ok = parseSemver("0.2.0-rc1")
	if !ok || v != [3]int{0, 2, 0} {
		t.Errorf("rc suffix must strip: got %v ok=%v", v, ok)
	}
	if _, ok := parseSemver("nonsense"); ok {
		t.Error("nonsense must not parse")
	}
	if _, ok := parseSemver("1.2"); ok {
		t.Error("two-part version must not parse")
	}
}

func TestParseChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checksums.txt")
	body := `abc123def456  mnemos_0.2.0_Linux_x86_64.tar.gz
deadbeef  mnemos_0.2.0_Darwin_arm64.tar.gz
# comment lines tolerated

fffff  mnemos_0.2.0_Windows_x86_64.zip
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		file, want string
		ok         bool
	}{
		{"mnemos_0.2.0_Linux_x86_64.tar.gz", "abc123def456", true},
		{"mnemos_0.2.0_Darwin_arm64.tar.gz", "deadbeef", true},
		{"mnemos_0.2.0_Windows_x86_64.zip", "fffff", true},
		{"does-not-exist.tar.gz", "", false},
	}
	for _, c := range cases {
		got, ok, err := parseChecksum(path, c.file)
		if err != nil {
			t.Errorf("%s: err %v", c.file, err)
			continue
		}
		if ok != c.ok || got != c.want {
			t.Errorf("parseChecksum(%q) = (%q, %v), want (%q, %v)", c.file, got, ok, c.want, c.ok)
		}
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob")
	content := []byte("hello world")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	want := hex.EncodeToString(h[:])
	if err := verifyChecksum(path, want); err != nil {
		t.Errorf("expected match, got %v", err)
	}
	if err := verifyChecksum(path, strings.Repeat("0", 64)); err == nil {
		t.Error("expected mismatch error")
	}
}

func TestExtractBinaryTarGz(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "mnemos.tar.gz")
	writeTarGz(t, archivePath, map[string][]byte{
		"LICENSE":   []byte("MIT"),
		"README.md": []byte("# mnemos"),
		"mnemos":    []byte("#!/bin/sh\necho stub"),
	})
	dst := filepath.Join(dir, "extracted")
	if err := extractBinary(archivePath, "tar.gz", "mnemos", dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "echo stub") {
		t.Errorf("extracted bytes wrong: %q", got)
	}
	// Binary must be executable (permission preserved).
	fi, _ := os.Stat(dst)
	if runtime.GOOS != "windows" && fi.Mode()&0o111 == 0 {
		t.Errorf("extracted binary not executable: %s", fi.Mode())
	}
}

func TestExtractBinaryZip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "mnemos.zip")
	writeZip(t, archivePath, map[string][]byte{
		"mnemos.exe": []byte("windows stub"),
		"README.md":  []byte("# mnemos"),
	})
	dst := filepath.Join(dir, "extracted.exe")
	if err := extractBinary(archivePath, "zip", "mnemos.exe", dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "windows stub" {
		t.Errorf("got %q", got)
	}
}

func TestExtractBinaryMissing(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "mnemos.tar.gz")
	writeTarGz(t, archivePath, map[string][]byte{"LICENSE": []byte("MIT")})
	err := extractBinary(archivePath, "tar.gz", "mnemos", filepath.Join(dir, "x"))
	if err == nil {
		t.Error("expected error when binary missing from archive")
	}
}

func TestReplaceSelfUnixAtomic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replace-old-first behaviour tested implicitly through CI on windows")
	}
	dir := t.TempDir()
	old := filepath.Join(dir, "mnemos")
	next := filepath.Join(dir, "mnemos.next")
	if err := os.WriteFile(old, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replaceSelf(old, next); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(old)
	if string(got) != "v2" {
		t.Errorf("got %q want v2", got)
	}
	fi, _ := os.Stat(old)
	if fi.Mode()&0o111 == 0 {
		t.Errorf("replaced binary not executable: %s", fi.Mode())
	}
	// Source should be gone (renamed in place).
	if _, err := os.Stat(next); err == nil {
		t.Error("source should be consumed by rename")
	}
}

func TestConfirmAcceptsYes(t *testing.T) {
	cases := map[string]bool{
		"y\n":     true,
		"Y\n":     true,
		"yes\n":   true,
		"YES\n":   true,
		"n\n":     false,
		"\n":      false,
		"":        false, // EOF without newline is decline
		"maybe\n": false,
	}
	for in, want := range cases {
		got := confirm(strings.NewReader(in), io.Discard)
		if got != want {
			t.Errorf("confirm(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFetchLatestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/polyxmedia/mnemos/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name": "v0.2.3"}`))
	}))
	defer srv.Close()

	defer withOverride(&updateAPIOverride, srv.URL)()

	tag, err := fetchLatestVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.2.3" {
		t.Errorf("tag = %q", tag)
	}
}

func TestFetchLatestVersionBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	defer withOverride(&updateAPIOverride, srv.URL)()

	if _, err := fetchLatestVersion(context.Background()); err == nil {
		t.Error("expected error on 500")
	}
}

func TestDownloadAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/polyxmedia/mnemos/releases/download/v0.2.0/mnemos_0.2.0_Linux_x86_64.tar.gz"
		if r.URL.Path != want {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("archive-bytes"))
	}))
	defer srv.Close()
	defer withOverride(&updateBaseOverride, srv.URL)()

	dst := filepath.Join(t.TempDir(), "out.tar.gz")
	if err := downloadAsset(context.Background(), "v0.2.0",
		"mnemos_0.2.0_Linux_x86_64.tar.gz", dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "archive-bytes" {
		t.Errorf("got %q", got)
	}
}

func TestRunUpdateAlreadyLatest(t *testing.T) {
	// Pin version.Version to a concrete semver so isNewerVersion can do a
	// proper comparison (dev is always treated as older). runUpdate must
	// short-circuit before any download.
	origVersion := version.Version
	version.Version = "v0.5.0"
	defer func() { version.Version = origVersion }()

	out := captureStdout(t, func() {
		if err := runUpdate(context.Background(),
			[]string{"--yes", "--version", "v0.5.0"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "already up to date") {
		t.Errorf("expected short-circuit message, got: %s", out)
	}
}

func TestRunUpdateFullFlow(t *testing.T) {
	// End-to-end exercise of download → checksum verify → extract →
	// atomic replace. We point updateSelfPath at a stand-in binary in a
	// tempdir so the test runner itself is not overwritten.
	if runtime.GOOS == "windows" {
		t.Skip("windows rename semantics covered by replaceSelf test")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "mnemos")
	if err := os.WriteFile(fakeBin, []byte("old-version"), 0o755); err != nil {
		t.Fatal(err)
	}

	newBytes := []byte("new-binary-v999")
	tag := "v9.9.9"
	assetFile := assetName(tag, runtime.GOOS, runtime.GOARCH)
	archive := filepath.Join(dir, assetFile)
	writeTarGz(t, archive, map[string][]byte{
		"mnemos":  newBytes,
		"LICENSE": []byte("MIT"),
	})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/polyxmedia/mnemos/releases/latest",
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"tag_name": %q}`, tag)
		})
	mux.HandleFunc(fmt.Sprintf("/polyxmedia/mnemos/releases/download/%s/%s", tag, assetFile),
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(data)
		})
	mux.HandleFunc(fmt.Sprintf("/polyxmedia/mnemos/releases/download/%s/checksums.txt", tag),
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, "%s  %s\n", want, assetFile)
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer withOverride(&updateAPIOverride, srv.URL)()
	defer withOverride(&updateBaseOverride, srv.URL)()

	origSelf := updateSelfPath
	updateSelfPath = func() (string, error) { return fakeBin, nil }
	defer func() { updateSelfPath = origSelf }()

	out := captureStdout(t, func() {
		if err := runUpdate(context.Background(), []string{"--yes"}); err != nil {
			t.Fatalf("runUpdate: %v", err)
		}
	})
	if !strings.Contains(out, "installed mnemos v9.9.9") {
		t.Errorf("expected success message, got: %s", out)
	}

	got, err := os.ReadFile(fakeBin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBytes) {
		t.Errorf("binary not replaced: got %q want %q", got, newBytes)
	}
	fi, _ := os.Stat(fakeBin)
	if fi.Mode()&0o111 == 0 {
		t.Errorf("replaced binary not executable: %s", fi.Mode())
	}
}

func TestRunUpdateChecksumMismatchAborts(t *testing.T) {
	// If checksums.txt lists the wrong digest, runUpdate must refuse to
	// touch the target binary. This is the primary security property of
	// self-update: malicious asset substitution is detected before rename.
	if runtime.GOOS == "windows" {
		t.Skip("rename semantics differ on windows")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "mnemos")
	if err := os.WriteFile(fakeBin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	tag := "v9.9.9"
	assetFile := assetName(tag, runtime.GOOS, runtime.GOARCH)
	archive := filepath.Join(dir, assetFile)
	writeTarGz(t, archive, map[string][]byte{"mnemos": []byte("tampered")})
	data, _ := os.ReadFile(archive)
	bogus := strings.Repeat("0", 64)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/polyxmedia/mnemos/releases/latest",
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"tag_name": %q}`, tag)
		})
	mux.HandleFunc(fmt.Sprintf("/polyxmedia/mnemos/releases/download/%s/%s", tag, assetFile),
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(data)
		})
	mux.HandleFunc(fmt.Sprintf("/polyxmedia/mnemos/releases/download/%s/checksums.txt", tag),
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, "%s  %s\n", bogus, assetFile)
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer withOverride(&updateAPIOverride, srv.URL)()
	defer withOverride(&updateBaseOverride, srv.URL)()
	defer func(orig func() (string, error)) { updateSelfPath = orig }(updateSelfPath)
	updateSelfPath = func() (string, error) { return fakeBin, nil }

	err := runUpdate(context.Background(), []string{"--yes"})
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("expected checksum error, got: %v", err)
	}
	// The original binary must be untouched — this is what protects users.
	got, _ := os.ReadFile(fakeBin)
	if string(got) != "old" {
		t.Errorf("binary should be untouched on checksum failure, got %q", got)
	}
}

// ---- helpers ----

// withOverride stashes a package-level variable, installs newVal, and
// returns a closer that restores the original. Idiomatic for httptest
// interplay with package-scope URL overrides.
func withOverride(p *string, newVal string) func() {
	old := *p
	*p = newVal
	return func() { *p = old }
}

func writeTarGz(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gw := gzip.NewWriter(f)
	defer func() { _ = gw.Close() }()
	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
}

func writeZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
