package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
		wantErr bool
	}{
		{"0.3.1", "0.4.0", true, false},
		{"v0.3.1", "v0.4.0", true, false},
		{"0.3.1", "v0.4.0", true, false},
		{"v0.3.1", "0.4.0", true, false},
		{"0.3.1", "0.3.1", false, false},
		{"0.4.0", "0.3.1", false, false},
		{"0.9.9", "1.0.0", true, false},
		{"1.0.0", "0.9.9", false, false},
		{"0.1.0", "0.1.1", true, false},
		{"0.1.1", "0.1.0", false, false},
		{"dev", "0.4.0", true, false},
		{"dev", "v0.4.0", true, false},
		{"", "0.4.0", true, false},
		{"0.3.1", "bad", false, true},
		{"bad", "0.4.0", false, true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_vs_%s", tt.current, tt.latest), func(t *testing.T) {
			got, err := IsNewer(tt.current, tt.latest)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsNewer(%q, %q) error = %v, wantErr %v", tt.current, tt.latest, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("hello world")
	hash := sha256.Sum256(data)
	goodHex := hex.EncodeToString(hash[:])

	checksums := fmt.Sprintf("%s  wakeup_darwin_arm64.tar.gz\nabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890  wakeup_darwin_amd64.tar.gz\n", goodHex)

	t.Run("valid checksum", func(t *testing.T) {
		err := verifyChecksum(data, []byte(checksums), "wakeup_darwin_arm64.tar.gz")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		err := verifyChecksum([]byte("wrong data"), []byte(checksums), "wakeup_darwin_arm64.tar.gz")
		if err == nil {
			t.Error("expected checksum mismatch error")
		}
	})

	t.Run("missing entry", func(t *testing.T) {
		err := verifyChecksum(data, []byte(checksums), "wakeup_linux_amd64.tar.gz")
		if err == nil {
			t.Error("expected missing entry error")
		}
	})
}

func TestSelectAssets(t *testing.T) {
	makeRelease := func(names ...string) *Release {
		rel := &Release{TagName: "v1.0.0"}
		for _, n := range names {
			rel.Assets = append(rel.Assets, Asset{Name: n, BrowserDownloadURL: "https://example.com/" + n})
		}
		return rel
	}

	t.Run("finds correct assets", func(t *testing.T) {
		rel := makeRelease("wakeup_darwin_arm64.tar.gz", "wakeup_darwin_amd64.tar.gz", "checksums.txt")
		bin, cksum, err := selectAssets(rel)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cksum.Name != "checksums.txt" {
			t.Errorf("checksum asset = %q, want checksums.txt", cksum.Name)
		}
		// The binary should match one of the darwin assets (depends on test runner arch).
		if bin.Name != "wakeup_darwin_arm64.tar.gz" && bin.Name != "wakeup_darwin_amd64.tar.gz" {
			t.Errorf("unexpected binary asset: %s", bin.Name)
		}
	})

	t.Run("missing checksums", func(t *testing.T) {
		rel := makeRelease("wakeup_darwin_arm64.tar.gz", "wakeup_darwin_amd64.tar.gz")
		_, _, err := selectAssets(rel)
		if err == nil {
			t.Error("expected error for missing checksums.txt")
		}
	})

	t.Run("missing arch", func(t *testing.T) {
		rel := makeRelease("wakeup_linux_amd64.tar.gz", "checksums.txt")
		_, _, err := selectAssets(rel)
		if err == nil {
			t.Error("expected error for missing architecture")
		}
	})
}

func TestExtractBinary(t *testing.T) {
	// Create a tar.gz in memory containing a "wakeup" binary.
	wantContent := []byte("#!/bin/fake-binary\n")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: "wakeup",
		Mode: 0755,
		Size: int64(len(wantContent)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(wantContent); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gw.Close()

	t.Run("extracts binary", func(t *testing.T) {
		got, err := extractBinary(buf.Bytes())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, wantContent) {
			t.Errorf("got %q, want %q", got, wantContent)
		}
	})

	t.Run("missing binary in archive", func(t *testing.T) {
		// Create archive with a different file name.
		var buf2 bytes.Buffer
		gw2 := gzip.NewWriter(&buf2)
		tw2 := tar.NewWriter(gw2)
		hdr2 := &tar.Header{Name: "other-file", Mode: 0644, Size: 5}
		tw2.WriteHeader(hdr2)
		tw2.Write([]byte("hello"))
		tw2.Close()
		gw2.Close()

		_, err := extractBinary(buf2.Bytes())
		if err == nil {
			t.Error("expected error for missing binary")
		}
	})
}
