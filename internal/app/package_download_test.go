// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/distribution"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSelectNewestSignedReleaseAcrossStaleMirrors(t *testing.T) {
	resetFeeds(t)
	oldFeedKey := distribution.FeedKey
	distribution.FeedKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	t.Cleanup(func() { distribution.FeedKey = oldFeedKey })
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seal := func(version string) []byte {
		payload, _ := json.Marshal(Release{Version: version})
		envelope, _ := json.Marshal(SignedRelease{Payload: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))})
		data, sealErr := distribution.Seal("release", envelope)
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		return data
	}
	got, err := selectNewestRelease([]feedCandidate{
		{base: "https://cdn.example/", raw: seal("1.0.9")},
		{base: "https://raw.example/", raw: seal("1.0.10")},
	}, public)
	if err != nil || got.Version != "1.0.10" {
		t.Fatalf("stale CDN should not override the newer signed version: got %q, err %v", got.Version, err)
	}
}

func testPackage(data string) ReleaseAsset {
	sum := sha256.Sum256([]byte(data))
	return ReleaseAsset{Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
}

func TestPackageDownloadAutomaticFallback(t *testing.T) {
	const expected = "complete-package"
	for _, failure := range []string{"unreachable", "bad-status", "wrong-hash", "truncated", "too-large", "timeout"} {
		t.Run(failure, func(t *testing.T) {
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch failure {
				case "bad-status":
					w.WriteHeader(503)
				case "wrong-hash":
					_, _ = w.Write([]byte(strings.Repeat("x", len(expected))))
				case "truncated":
					_, _ = w.Write([]byte("short"))
				case "too-large":
					_, _ = w.Write([]byte(expected + "extra"))
				case "timeout":
					<-r.Context().Done()
				}
			}))
			defer primary.Close()
			original := primary.URL
			if failure == "unreachable" {
				primary.Close()
			}
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(expected)) }))
			defer fallback.Close()
			var attempted []string
			data, err := fetchPackage(context.Background(), testPackage(expected), &http.Client{Timeout: 100 * time.Millisecond}, []string{original, fallback.URL}, func(addr string) { attempted = append(attempted, addr) })
			if err != nil || string(data) != expected {
				t.Fatalf("fallback failed: %q %v", data, err)
			}
			if !reflect.DeepEqual(attempted, []string{original, fallback.URL}) {
				t.Fatalf("wrong source order: %v", attempted)
			}
		})
	}
}

func TestPackageDownloadRejectsAllInvalidSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("bad")) }))
	defer server.Close()
	data, err := fetchPackage(context.Background(), testPackage("expected"), server.Client(), []string{server.URL, server.URL}, nil)
	if err == nil || data != nil {
		t.Fatalf("accepted invalid archive: %q %v", data, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	_, err = fetchPackage(ctx, testPackage("expected"), server.Client(), []string{server.URL}, func(string) { attempts++ })
	if err != context.Canceled || attempts != 0 {
		t.Fatalf("canceled download retried: %v, %d", err, attempts)
	}
}

func TestPackageDownloadKeepsGitHubFirst(t *testing.T) {
	original := releasePrefix + "v1.0.2/Catherina-Mengka-1.0.2-windows-amd64.zip"
	t.Setenv("CATHERINA_PACKAGE_MIRRORS", "")
	urls := packageURLs(original)
	if len(urls) != 1 || urls[0] != original {
		t.Fatalf("default download sources should contain only GitHub: %v", urls)
	}

	t.Setenv("CATHERINA_PACKAGE_MIRRORS", "https://mirror.example/")
	urls = packageURLs(original)
	if len(urls) != 2 || urls[0] != original || urls[1] != "https://mirror.example/"+original {
		t.Fatalf("local mirror configuration was not applied after GitHub: %v", urls)
	}
}
