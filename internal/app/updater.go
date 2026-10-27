// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var UpdatePublicKey string

const releasePrefix = "https://github.com/Jay1023CN/Mengka-NT-plugins/releases/download/"

type ReleaseAsset struct {
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	BinarySHA256 string `json:"binary_sha256"`
}
type Release struct {
	Version   string                  `json:"version"`
	Published string                  `json:"published"`
	Notes     string                  `json:"notes"`
	Assets    map[string]ReleaseAsset `json:"assets"`
}
type SignedRelease struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}
type UpdateState struct {
	Current        string `json:"current"`
	Latest         string `json:"latest"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	Checked        string `json:"checked"`
	Notes          string `json:"notes"`
	DownloadURL    string `json:"download_url"`
	DownloadSource string `json:"download_source"`
}
type Updater struct {
	Policy    *PolicyManager
	settings  *ServiceSettings
	dir       string
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	state     UpdateState
	busy      bool
	checking  bool
	wg        sync.WaitGroup
	managed   atomic.Bool
	Shutdown  func(string)
	HealthURL string
	framework *FrameworkUpdater
}

func NewUpdater(s *ServiceSettings, dir, healthURL string, shutdown func(string)) *Updater {
	ctx, cancel := context.WithCancel(context.Background())
	return &Updater{Policy: NewPolicyManager(dir), settings: s, dir: dir, ctx: ctx, cancel: cancel, state: UpdateState{Current: Version, Status: "idle"}, Shutdown: shutdown, HealthURL: healthURL}
}
func (u *Updater) Start() {
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-u.ctx.Done():
				return
			case <-timer.C:
				u.CheckAsync(u.settings.Get().AutoUpdate)
				timer.Reset(time.Hour)
			}
		}
	}()
}

func (u *Updater) SetManaged(v bool) { u.managed.Store(v) }
func (u *Updater) Stop() {
	u.mu.Lock()
	u.cancel()
	u.mu.Unlock()
	u.wg.Wait()
	if u.framework != nil {
		u.framework.stop()
	}
}
func (u *Updater) State() UpdateState { u.mu.Lock(); defer u.mu.Unlock(); return u.state }
func (u *Updater) CheckAsync(install bool) bool {
	return u.startCheck(install, false)
}

// CheckOrJoin lets the admin UI reuse an update check already started in the background.
func (u *Updater) CheckOrJoin() bool {
	return u.startCheck(false, true)
}

func (u *Updater) startCheck(install, join bool) bool {
	u.mu.Lock()
	if u.busy {
		joined := join && u.checking
		u.mu.Unlock()
		return joined
	}
	if u.ctx.Err() != nil {
		u.mu.Unlock()
		return false
	}
	u.busy = true
	u.checking = true
	u.state.Status = "checking"
	u.state.Error = ""
	u.state.Checked = time.Now().Format(time.RFC3339)
	u.wg.Add(1)
	u.mu.Unlock()
	go func() {
		defer u.wg.Done()
		err := u.check(install)
		u.mu.Lock()
		u.busy = false
		u.checking = false
		if err != nil {
			u.state.Status = "error"
			u.state.Error = err.Error()
		}
		u.mu.Unlock()
		if err != nil {
			log.Printf("更新未完成：%v", err)
		}
	}()
	return true
}
func parseVersion(v string) ([3]uint64, error) {
	var out [3]uint64
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, errors.New("版本号必须由三段数字组成")
	}
	for i, part := range parts {
		if part == "" {
			return out, errors.New("版本号不能为空")
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return out, errors.New("版本号只能包含数字和点")
			}
		}
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return out, err
		}
		out[i] = n
	}
	return out, nil
}
func versionNewer(a, b string) bool {
	comparison, err := compareVersions(a, b)
	return err == nil && comparison > 0
}

func compareVersions(a, b string) (int, error) {
	aa, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	bb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}

	aLegacy, bLegacy := aa[0] >= 2020 && aa[0] <= 2099, bb[0] >= 2020 && bb[0] <= 2099
	if aLegacy != bLegacy {
		if aLegacy {
			return -1, nil
		}
		return 1, nil
	}
	for i := range aa {
		if aa[i] != bb[i] {
			if aa[i] > bb[i] {
				return 1, nil
			}
			return -1, nil
		}
	}
	return 0, nil
}

func releaseStatus(current, latest string) string {
	comparison, err := compareVersions(latest, current)
	if err != nil {
		return "error"
	}
	switch {
	case comparison < 0:
		return "ahead"
	case comparison > 0:
		return "available"
	default:
		return "current"
	}
}
func (u *Updater) check(install bool) error {
	policy, policyErr := u.refreshPolicy()
	if policy.Revision == 0 {
		return policyErr
	}
	if time.Now().Unix() >= policy.Expires {
		return errors.New("版本策略已过期")
	}
	if policy.Paused {
		return errors.New("发布者已暂停更新：" + policy.Message)
	}
	install = install || policy.Required
	release, err := readSignedRelease(u.ctx)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.state.Checked = time.Now().Format(time.RFC3339)
	u.state.Latest = release.Version
	u.state.Notes = release.Notes
	u.state.DownloadURL = releasePrefix + "v" + release.Version + "/Catherina-Mengka-" + release.Version + "-" + runtime.GOOS + "-" + runtime.GOARCH + ".zip"
	status := releaseStatus(Version, release.Version)
	u.state.Status = status
	u.mu.Unlock()
	if versionNewer(policy.Minimum, release.Version) || (policy.Force && versionNewer(policy.Target, release.Version)) {
		return errors.New("更新包尚未达到策略要求的版本，请等待发布者补齐安装包")
	}
	if status != "available" {
		return nil
	}
	if !install {
		return nil
	}
	if u.managed.Load() {
		if u.framework == nil {
			return errors.New("框架在线升级尚未初始化")
		}
		return u.framework.Start(false)
	}
	if err := updateCooldown(u.dir, release.Version, time.Now()); err != nil {
		return err
	}
	asset, _, err := releasePackage(release, runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	oldHash, err := hashFile(executable)
	if err != nil {
		return err
	}
	if probe, err := os.CreateTemp(filepath.Dir(executable), ".catherina-write-probe-*"); err != nil {
		return errors.New("程序目录不可写，请给运行用户更新权限")
	} else {
		name := probe.Name()
		probe.Close()
		_ = os.Remove(name)
	}
	u.mu.Lock()
	u.state.Status = "downloading"
	u.mu.Unlock()
	archive, err := u.downloadAsset(u.ctx, asset)
	if err != nil {
		return fmt.Errorf("下载更新失败：%w", err)
	}
	dir, err := os.MkdirTemp(filepath.Join(u.dir), "update-*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	archivePath := filepath.Join(dir, "package.zip")
	if err = os.WriteFile(archivePath, archive, 0600); err != nil {
		return err
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	binaryName := "Catherina"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	staged := filepath.Join(dir, binaryName)
	found := false
	for _, entry := range reader.File {
		if entry.Name != binaryName {
			continue
		}
		if found || entry.UncompressedSize64 > 150*1024*1024 {
			return errors.New("更新包程序重复或过大")
		}
		found = true
		in, err := entry.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(in, 150*1024*1024+1))
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if !found {
		return errors.New("安装包缺少当前平台程序")
	}
	newHash, err := hashFile(staged)
	if err != nil || newHash != asset.BinarySHA256 {
		return errors.New("新程序哈希不匹配")
	}
	if err = checkBinary(staged); err != nil {
		return err
	}
	marker := filepath.Join(dir, "parent-stopped")
	plan := UpdatePlan{Target: executable, OldHash: oldHash, NewHash: newHash, Ready: marker, Args: os.Args[1:], HealthURL: u.HealthURL, Version: release.Version}
	planPath := filepath.Join(dir, "plan.json")
	encoded, _ := json.Marshal(plan)
	if err = os.WriteFile(planPath, encoded, 0600); err != nil {
		return err
	}
	helper := exec.Command(staged, "--apply-update", planPath)
	helper.Stdout = os.Stderr
	helper.Stderr = os.Stderr
	if err = helper.Start(); err != nil {
		return fmt.Errorf("启动更新助手失败：%w", err)
	}
	_ = helper.Process.Release()
	keep = true
	u.mu.Lock()
	u.state.Status = "installing"
	u.mu.Unlock()
	u.Shutdown(marker)
	return nil
}
func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func checkBinary(path string) error {
	if runtime.GOOS == "windows" {
		file, err := pe.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			return errors.New("更新程序不是 Windows AMD64")
		}
		return nil
	}
	file, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	machine := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		machine = elf.EM_AARCH64
	}
	if file.Machine != machine {
		return errors.New("更新程序的 Linux 架构不匹配")
	}
	return nil
}

type UpdatePlan struct {
	Target    string   `json:"target"`
	OldHash   string   `json:"old_hash"`
	NewHash   string   `json:"new_hash"`
	Ready     string   `json:"ready"`
	Args      []string `json:"args"`
	HealthURL string   `json:"health_url"`
	Version   string   `json:"version"`
}

func ApplyUpdate(planPath string) error {
	if !filepath.IsAbs(planPath) {
		return errors.New("更新计划路径无效")
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	var plan UpdatePlan
	if json.Unmarshal(raw, &plan) != nil || !filepath.IsAbs(plan.Target) || !filepath.IsAbs(plan.Ready) {
		return errors.New("更新计划无效")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	hash, err := hashFile(self)
	if err != nil || hash != plan.NewHash {
		return errors.New("更新助手自身哈希不符")
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, err = os.Stat(plan.Ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("等待原进程退出超时，尚未替换程序")
		}
		time.Sleep(200 * time.Millisecond)
	}
	hash, err = hashFile(plan.Target)
	if err != nil || hash != plan.OldHash {
		return errors.New("原程序已变更，已取消替换")
	}
	backup := plan.Target + ".previous"
	_ = os.Remove(backup)
	for {
		err = os.Rename(plan.Target, backup)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("原程序仍被占用，尚未替换：%w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	rollback := func() {
		failure, _ := json.Marshal(UpdateFailure{Version: plan.Version, RetryAfter: time.Now().Add(3 * time.Hour).Unix()})
		if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(plan.Ready)), "update-failure.json"), failure, 0600); err != nil {
			log.Printf("记录失败版本失败：%v", err)
		}
		if err := os.Remove(plan.Target); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("回滚失败，旧版保留在 %s：%v", backup, err)
			return
		}
		if err := os.Rename(backup, plan.Target); err != nil {
			log.Printf("恢复旧程序失败，旧版保留在 %s：%v", backup, err)
			return
		}
		cmd := exec.Command(plan.Target, plan.Args...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err == nil {
			_ = cmd.Process.Release()
		}
	}
	in, err := os.Open(self)
	if err != nil {
		rollback()
		return err
	}
	out, err := os.OpenFile(plan.Target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		in.Close()
		rollback()
		return err
	}
	_, err = io.Copy(out, in)
	in.Close()
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	hash, hashErr := hashFile(plan.Target)
	if err != nil || hashErr != nil || hash != plan.NewHash {
		rollback()
		return errors.New("新程序写入不完整，已恢复旧版")
	}
	cmd := exec.Command(plan.Target, plan.Args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		rollback()
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	healthy := false
	until := time.Now().Add(40 * time.Second)
	for time.Now().Before(until) {
		resp, err := client.Get(plan.HealthURL)
		if err == nil {
			var status struct {
				Version string `json:"version"`
			}
			err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&status)
			resp.Body.Close()
			if err == nil && status.Version == plan.Version {
				healthy = true
				break
			}
		}
		time.Sleep(time.Second)
	}
	if !healthy {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		rollback()
		return errors.New("新版管理服务未启动，已恢复并启动旧版")
	}
	_ = cmd.Process.Release()
	log.Printf("Catherina 已更新到 %s", plan.Version)
	return nil
}

type UpdateFailure struct {
	Version    string `json:"version"`
	RetryAfter int64  `json:"retry_after"`
}

func updateCooldown(dir, version string, now time.Time) error {
	raw, err := os.ReadFile(filepath.Join(dir, "update-failure.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failed UpdateFailure
	if json.Unmarshal(raw, &failed) != nil {
		return errors.New("更新失败记录损坏，请在数据目录检查 update-failure.json")
	}
	if failed.Version == version && now.Unix() < failed.RetryAfter {
		return errors.New("该版本启动失败，已回滚；3 小时内暂停重试，等待修复版")
	}
	return nil
}
