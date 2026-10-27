// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultFeedBase = "https://raw.githubusercontent.com/Jay1023CN/Mengka-NT-plugins/main/"

var feedMirrors []string

var (
	feedMu     sync.RWMutex
	feedBase   = defaultFeedBase
	feedPinned bool
)

const feedAttemptTimeout = 12 * time.Second

func InitFeeds(getenv func(string) string) error {
	raw := strings.TrimSpace(getenv("CATHERINA_FEED_BASE"))
	if raw == "" {
		return nil
	}
	base, err := normalizeFeedBase(raw)
	if err != nil {
		return err
	}
	feedMu.Lock()
	feedBase, feedPinned = base, true
	feedMu.Unlock()
	log.Printf("更新源已固定为 %s", base)
	return nil
}

func feedSources() []string {
	feedMu.RLock()
	defer feedMu.RUnlock()
	if feedPinned {
		return []string{feedBase}
	}
	configured := make([]string, 0)
	for _, raw := range strings.Split(os.Getenv("CATHERINA_FEED_MIRRORS"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if base, err := normalizeFeedBase(raw); err == nil {
			configured = append(configured, base)
		}
	}
	out := make([]string, 0, len(feedMirrors)+len(configured)+2)
	seen := make(map[string]struct{}, len(feedMirrors)+len(configured)+2)
	add := func(base string) {
		if base == "" {
			return
		}
		if _, exists := seen[base]; exists {
			return
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}

	add(feedBase)
	add(defaultFeedBase)
	for _, mirror := range feedMirrors {
		add(mirror)
	}
	for _, mirror := range configured {
		add(mirror)
	}
	return out
}

func fetchFeed(ctx context.Context, name string, limit int64) ([]byte, error) {
	sources := feedSources()
	var lastErr error
	for _, base := range sources {
		attemptCtx, cancel := context.WithTimeout(ctx, feedAttemptTimeout)
		raw, err := fetch(attemptCtx, base+name, limit)
		cancel()
		if err == nil {
			if preferFeedBase(base) {
				log.Printf("GitHub 直连不可用，更新源已切换到 %s", base)
			}
			return raw, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("%d 个更新源都无法访问，最后错误：%w", len(sources), lastErr)
}

type feedCandidate struct {
	base string
	raw  []byte
}

func fetchFeedCandidates(ctx context.Context, name string, limit int64) []feedCandidate {
	sources := feedSources()
	var candidates []feedCandidate
	for _, base := range sources {
		if err := ctx.Err(); err != nil {
			break
		}
		attemptCtx, cancel := context.WithTimeout(ctx, feedAttemptTimeout)
		raw, err := fetch(attemptCtx, base+name, limit)
		cancel()
		if err == nil {
			candidates = append(candidates, feedCandidate{base: base, raw: raw})
		}
	}
	return candidates
}

func preferFeedBase(base string) bool {
	feedMu.Lock()
	defer feedMu.Unlock()
	if feedPinned || feedBase == base {
		return false
	}
	feedBase = base
	return true
}

func feedURL(name string) string { return FeedBase() + name }

func FeedBase() string {
	feedMu.RLock()
	defer feedMu.RUnlock()
	return feedBase
}

func FeedSources() []string { return feedSources() }

func normalizeFeedBase(raw string) (string, error) {
	if len(raw) > 512 {
		return "", errors.New("更新源地址过长")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("更新源地址无效：" + err.Error())
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("更新源地址不能带账号、查询串或片段，请直接填写目录地址并以 / 结尾")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", errors.New("更新源必须是 HTTPS 地址，只有本机回环允许 HTTP")
		}
	default:
		return "", errors.New("更新源只能是 http 或 https 地址")
	}
	if u.Host == "" {
		return "", errors.New("更新源地址缺少主机名")
	}
	if strings.Contains(u.Path, "..") {
		return "", errors.New("更新源地址不能包含上级目录")
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/"
	return u.Scheme + "://" + u.Host + u.Path, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
