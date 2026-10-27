// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type feedTestRoundTripper struct{ base http.RoundTripper }

func (rt feedTestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() == "raw.githubusercontent.com" {
		return nil, errors.New("network access disabled in feed test")
	}
	return rt.base.RoundTrip(req)
}

type feedEnv map[string]string

func (e feedEnv) get(name string) string { return e[name] }

func resetFeeds(t *testing.T) {
	t.Helper()
	mirrors := feedMirrors
	feedMu.Lock()
	feedBase, feedPinned = defaultFeedBase, false
	feedMu.Unlock()
	t.Cleanup(func() {
		feedMu.Lock()
		feedBase, feedPinned, feedMirrors = defaultFeedBase, false, mirrors
		feedMu.Unlock()
	})
}

func TestFeedBaseDefaults(t *testing.T) {
	resetFeeds(t)
	if err := InitFeeds(feedEnv{"CATHERINA_FEED_BASE": "   "}.get); err != nil {
		t.Fatalf("空值应当保持默认源：%v", err)
	}
	if FeedBase() != defaultFeedBase {
		t.Fatalf("未设置时必须保持默认源，实际 %s", FeedBase())
	}
	if got, want := feedURL("state.dat"), defaultFeedBase+"state.dat"; got != want {
		t.Fatalf("state.dat 地址不符：%s", got)
	}
	if got, want := feedURL("release.dat"), defaultFeedBase+"release.dat"; got != want {
		t.Fatalf("release.dat 地址不符：%s", got)
	}
	if len(FeedSources()) != len(feedMirrors)+1 {
		t.Fatalf("默认应当在 GitHub 之外自动带上镜像兜底，实际 %v", FeedSources())
	}
}

func TestFeedBaseMirror(t *testing.T) {
	resetFeeds(t)
	cases := []struct{ raw, want string }{
		{"https://cdn.jsdelivr.net/gh/Jay1023CN/Mengka-NT-plugins@main", "https://cdn.jsdelivr.net/gh/Jay1023CN/Mengka-NT-plugins@main/"},
		{"https://mirror.example.com/mengka/", "https://mirror.example.com/mengka/"},
		{"  https://mirror.example.com/mengka  ", "https://mirror.example.com/mengka/"},
		{"http://127.0.0.1:8080/feeds/", "http://127.0.0.1:8080/feeds/"},
		{"http://localhost:8080/feeds", "http://localhost:8080/feeds/"},
	}
	for _, c := range cases {
		if err := InitFeeds(feedEnv{"CATHERINA_FEED_BASE": c.raw}.get); err != nil {
			t.Fatalf("合法更新源被拒绝 %q：%v", c.raw, err)
		}
		if FeedBase() != c.want {
			t.Fatalf("%q 规范化为 %q，期望 %q", c.raw, FeedBase(), c.want)
		}
		InitFeeds(feedEnv{}.get)
		feedMu.Lock()
		feedBase, feedPinned = defaultFeedBase, false
		feedMu.Unlock()
	}
	if err := InitFeeds(feedEnv{"CATHERINA_FEED_BASE": "https://mirror.example.com/mengka/"}.get); err != nil {
		t.Fatal(err)
	}
	if feedURL("state.dat") != "https://mirror.example.com/mengka/state.dat" {
		t.Fatalf("镜像拼接错误：%s", feedURL("state.dat"))
	}
	if got := FeedSources(); len(got) != 1 || got[0] != "https://mirror.example.com/mengka/" {
		t.Fatalf("手工指定更新源后不应再自动兜底，实际 %v", got)
	}
}

func TestFeedSourcesKeepDefaultAfterMirrorAndDeduplicate(t *testing.T) {
	resetFeeds(t)
	preferred := "https://mirror.example.com/preferred/"
	other := "https://mirror.example.com/other/"
	feedMu.Lock()
	feedBase = preferred
	feedMirrors = []string{preferred, defaultFeedBase, other, other}
	feedMu.Unlock()

	got := FeedSources()
	want := []string{preferred, defaultFeedBase, other}
	if len(got) != len(want) {
		t.Fatalf("候选源应保留首选镜像和默认源并去重：got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("候选源顺序错误：got %v, want %v", got, want)
		}
	}
}

func TestFeedBaseRejectsUnsafe(t *testing.T) {
	resetFeeds(t)
	for _, raw := range []string{
		"http://mirror.example.com/mengka/",
		"ftp://mirror.example.com/mengka/",
		"https://mirror.example.com/mengka/?token=secret",
		"https://mirror.example.com/mengka/#frag",
		"https://user:pass@mirror.example.com/mengka/",
		"https://mirror.example.com/../secrets/",
		"https:///mengka/",
		"not a url",
		"https://" + strings.Repeat("a", 600) + "/mengka/",
	} {
		if err := InitFeeds(feedEnv{"CATHERINA_FEED_BASE": raw}.get); err == nil {
			t.Fatalf("无效更新源被接受：%q", raw)
		}
		if FeedBase() != defaultFeedBase {
			t.Fatalf("无效更新源不应改变当前源：%s", FeedBase())
		}
	}
}

func TestFeedFallsBackToMirror(t *testing.T) {
	resetFeeds(t)
	oldClient := httpClient
	httpClient = &http.Client{Transport: feedTestRoundTripper{base: http.DefaultTransport}}
	t.Cleanup(func() { httpClient = oldClient })
	var goodHits, badHits int
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/state.dat" {
			goodHits++
			_, _ = w.Write([]byte("GOOD"))
			return
		}
		http.NotFound(w, r)
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badHits++
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer bad.Close()

	feedMu.Lock()
	feedBase = bad.URL + "/"
	feedMirrors = []string{good.URL + "/"}
	feedMu.Unlock()

	ctx := context.Background()
	raw, err := fetchFeed(ctx, "state.dat", 1024)
	if err != nil {
		t.Fatalf("GitHub 不可达时应当自动改用镜像：%v", err)
	}
	if string(raw) != "GOOD" {
		t.Fatalf("取到了错误内容：%s", raw)
	}
	if badHits != 1 || goodHits != 1 {
		t.Fatalf("尝试次数不符：坏源 %d 次，镜像 %d 次", badHits, goodHits)
	}
	if FeedBase() != good.URL+"/" {
		t.Fatalf("成功源应成为后续首选，实际 %s", FeedBase())
	}
	if _, err = fetchFeed(ctx, "state.dat", 1024); err != nil {
		t.Fatal(err)
	}
	if badHits != 1 {
		t.Fatalf("切换成功后不应再回头打坏源，实际又打了 %d 次", badHits)
	}
}

func TestFeedAllSourcesFailed(t *testing.T) {
	resetFeeds(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()
	feedMu.Lock()
	feedBase = bad.URL + "/"
	feedMirrors = []string{"http://127.0.0.1:1/"}
	feedMu.Unlock()
	if _, err := fetchFeed(context.Background(), "release.dat", 1024); err == nil {
		t.Fatal("全部源失败必须报错")
	} else if !strings.Contains(err.Error(), "更新源") {
		t.Fatalf("错误信息应说明更新源不可用：%v", err)
	}
}
