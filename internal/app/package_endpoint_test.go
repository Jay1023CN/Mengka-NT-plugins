// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/distribution"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
)

func TestManagedPackageDownloadFallsBackWithoutGitHub(t *testing.T) {
	resetFeeds(t)
	private, policy := policyFixture(t)
	oldKey, oldPrefixes, oldTransport := UpdatePublicKey, packageMirrorPrefixes, http.DefaultTransport
	t.Cleanup(func() {
		UpdatePublicKey = oldKey
		packageMirrorPrefixes = oldPrefixes
		http.DefaultTransport = oldTransport
	})
	UpdatePublicKey = base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey))
	const contents = "verified-install-package"
	platform := runtime.GOOS + "-" + runtime.GOARCH
	filename := "Catherina-Mengka-" + Version + "-" + platform + ".zip"
	asset := testPackage(contents)
	asset.URL = releasePrefix + "v" + Version + "/" + filename
	asset.BinarySHA256 = strings.Repeat("a", 64)
	payload, _ := json.Marshal(Release{Version: Version, Assets: map[string]ReleaseAsset{platform: asset}})
	envelope, _ := json.Marshal(SignedRelease{Payload: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))})
	releaseBytes, err := distribution.Seal("release", envelope)
	if err != nil {
		t.Fatal(err)
	}
	policyBytes := signPolicy(t, private, policy)
	mirrorHits, githubHits := 0, 0
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/state.dat":
			_, _ = w.Write(policyBytes)
		case r.URL.Path == "/release.dat":
			_, _ = w.Write(releaseBytes)
		case strings.HasPrefix(r.URL.Path, "/proxy/"):
			mirrorHits++
			_, _ = w.Write([]byte(contents))
		default:
			http.NotFound(w, r)
		}
	}))
	defer feed.Close()

	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { githubHits++; w.WriteHeader(502) }))
	defer blocked.Close()
	proxyURL, _ := url.Parse(blocked.URL)
	transport := oldTransport.(*http.Transport).Clone()
	transport.Proxy = func(r *http.Request) (*url.URL, error) {
		if r.URL.Hostname() == "github.com" {
			return proxyURL, nil
		}
		return nil, nil
	}
	http.DefaultTransport = transport
	defer transport.CloseIdleConnections()
	feedMu.Lock()
	feedBase = feed.URL + "/"
	feedPinned = true
	feedMu.Unlock()
	packageMirrorPrefixes = []string{feed.URL + "/proxy/"}
	managed, _, _, base, token := managedAdminFixture(t)
	res := managedRequest(t, http.MethodGet, base+"/api/update/package", token, managed.AdminOrigin(), nil)
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(data) != contents {
		t.Fatalf("HTTP %d: %s", res.StatusCode, data)
	}
	if githubHits != 1 || mirrorHits != 1 {
		t.Fatalf("did not fail over: github %d mirror %d", githubHits, mirrorHits)
	}
	if !strings.Contains(res.Header.Get("Content-Disposition"), filename) || res.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("not a downloadable package: %v", res.Header)
	}
}
