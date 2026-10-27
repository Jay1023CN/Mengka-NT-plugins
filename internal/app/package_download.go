// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/distribution"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

var packageMirrorPrefixes []string

func packageURLs(original string) []string {
	out := []string{original}
	prefixes := append([]string(nil), packageMirrorPrefixes...)
	for _, raw := range strings.Split(os.Getenv("CATHERINA_PACKAGE_MIRRORS"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if prefix, err := normalizeFeedBase(raw); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	for _, prefix := range prefixes {
		out = append(out, prefix+original)
	}
	return out
}

func packageHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 8 * time.Second
	transport.ResponseHeaderTimeout = 12 * time.Second
	return &http.Client{Transport: transport, Timeout: 2 * time.Minute}
}

func fetchPackage(ctx context.Context, asset ReleaseAsset, client *http.Client, sources []string, onAttempt func(string)) ([]byte, error) {
	var failures []string
	for _, addr := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if onAttempt != nil {
			onAttempt(addr)
		}
		data, err := fetchUsing(ctx, addr, asset.Size, client)
		if err == nil {
			sum := sha256.Sum256(data)
			if int64(len(data)) != asset.Size || hex.EncodeToString(sum[:]) != asset.SHA256 {
				err = errors.New("安装包大小或 SHA-256 不符")
			}
		}
		if err == nil {
			return data, nil
		}
		parsed, _ := url.Parse(addr)
		failures = append(failures, parsed.Hostname()+": "+err.Error())
	}
	return nil, fmt.Errorf("所有安装包下载线路均失败：%s", strings.Join(failures, "；"))
}

func readSignedRelease(ctx context.Context) (Release, error) {
	key, err := base64.StdEncoding.DecodeString(UpdatePublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return Release{}, errors.New("本程序未配置有效的发布公钥")
	}
	candidates := fetchFeedCandidates(ctx, "release.dat", 1024*1024)
	return selectNewestRelease(candidates, key)
}

func selectNewestRelease(candidates []feedCandidate, key ed25519.PublicKey) (Release, error) {
	var newest Release
	var newestBase string
	var failures []string
	for _, candidate := range candidates {
		release, verifyErr := parseSignedRelease(candidate.raw, key)
		if verifyErr != nil {
			failures = append(failures, candidate.base+": "+verifyErr.Error())
			continue
		}
		if newest.Version == "" {
			newest, newestBase = release, candidate.base
			continue
		}
		comparison, compareErr := compareVersions(release.Version, newest.Version)
		if compareErr != nil {
			failures = append(failures, candidate.base+": "+compareErr.Error())
			continue
		}
		if comparison > 0 {
			newest, newestBase = release, candidate.base
		}
	}
	if newest.Version == "" {
		return Release{}, fmt.Errorf("所有更新源的清单都未通过校验：%s", strings.Join(failures, "；"))
	}
	if preferFeedBase(newestBase) {
		log.Printf("已选择版本较新的更新源 %s", newestBase)
	}
	return newest, nil
}

func parseSignedRelease(raw []byte, key ed25519.PublicKey) (Release, error) {
	var release Release
	plain, err := distribution.Open("release", raw)
	if err != nil {
		return release, err
	}
	var signed SignedRelease
	if json.Unmarshal(plain, &signed) != nil {
		return release, errors.New("更新清单格式无效")
	}
	sig, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil || !ed25519.Verify(key, signed.Payload, sig) {
		return release, errors.New("更新清单签名不匹配，已拒绝下载")
	}
	if json.Unmarshal(signed.Payload, &release) != nil {
		return release, errors.New("更新清单内容无效")
	}
	_, err = parseVersion(release.Version)
	return release, err
}

func releasePackage(release Release, platform string) (ReleaseAsset, string, error) {
	name := "Catherina-Mengka-" + release.Version + "-" + platform + ".zip"
	asset, ok := release.Assets[platform]
	if !ok {
		return asset, name, fmt.Errorf("新版未提供 %s 安装包", platform)
	}
	expected := releasePrefix + "v" + release.Version + "/" + name
	if asset.URL != expected || len(asset.SHA256) != 64 || len(asset.BinarySHA256) != 64 || asset.Size < 1 || asset.Size > 100*1024*1024 {
		return asset, name, errors.New("更新地址、大小或哈希字段无效")
	}
	return asset, name, nil
}

func (u *Updater) downloadAsset(ctx context.Context, asset ReleaseAsset) ([]byte, error) {
	client := packageHTTPClient()
	defer client.CloseIdleConnections()
	return fetchPackage(ctx, asset, client, packageURLs(asset.URL), func(addr string) {
		parsed, _ := url.Parse(addr)
		u.mu.Lock()
		u.state.Status = "downloading"
		u.state.DownloadSource = parsed.Hostname()
		u.mu.Unlock()
		if addr != asset.URL {
			log.Printf("安装包下载切换备用线路：%s", parsed.Hostname())
		}
	})
}

func (u *Updater) DownloadLatest(ctx context.Context) (name string, data []byte, err error) {
	u.mu.Lock()
	if u.busy || u.ctx.Err() != nil {
		u.mu.Unlock()
		return "", nil, errors.New("更新任务正在进行，请稍后下载")
	}
	u.busy = true
	u.wg.Add(1)
	u.state.Error = ""
	u.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(u.ctx, cancel)
	defer func() {
		stop()
		cancel()
		u.mu.Lock()
		u.busy = false
		if err != nil {
			u.state.Status = "error"
			u.state.Error = err.Error()
		} else if u.state.Latest == Version {
			u.state.Status = "current"
		} else {
			u.state.Status = "available"
		}
		u.mu.Unlock()
		u.wg.Done()
	}()
	policy, policyErr := u.refreshPolicy()
	if policy.Revision == 0 {
		if policyErr == nil {
			policyErr = errors.New("尚未获取版本信息")
		}
		return "", nil, policyErr
	}
	if time.Now().Unix() >= policy.Expires || policy.Paused {
		return "", nil, errors.New("当前暂不可下载更新，请稍后检查")
	}
	release, err := readSignedRelease(ctx)
	if err != nil {
		return "", nil, err
	}
	if versionNewer(Version, release.Version) || versionNewer(policy.Minimum, release.Version) || (policy.Force && versionNewer(policy.Target, release.Version)) {
		return "", nil, errors.New("备用更新源仍是旧版本，请稍后检查")
	}
	asset, name, err := releasePackage(release, runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		return "", nil, err
	}
	u.mu.Lock()
	u.state.Latest = release.Version
	u.state.Notes = release.Notes
	u.mu.Unlock()
	data, err = u.downloadAsset(ctx, asset)
	return name, data, err
}
