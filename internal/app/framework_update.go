// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
)

type FrameworkUpdateView struct {
	Stage   string          `json:"stage"`
	Target  string          `json:"target"`
	Origin  string          `json:"origin"`
	Error   string          `json:"error"`
	Changes json.RawMessage `json:"changes,omitempty"`
}

type FrameworkUpdater struct {
	engine         *Engine
	updater        *Updater
	settings       *ServiceSettings
	mu             sync.Mutex
	requestMu      sync.Mutex
	client         *http.Client
	base           string
	manualBase     bool
	view           FrameworkUpdateView
	busy           bool
	closing        bool
	archive        []byte
	name, approval string
	approvalUntil  time.Time
	wg             sync.WaitGroup
}

func newFrameworkUpdater(e *Engine, u *Updater, settings *ServiceSettings) *FrameworkUpdater {
	jar, _ := cookiejar.New(nil)
	return &FrameworkUpdater{engine: e, updater: u, settings: settings, client: &http.Client{Jar: jar, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("框架接口发生跳转，请检查框架管理地址")
	}}}
}
func (f *FrameworkUpdater) View() FrameworkUpdateView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view
}
func (f *FrameworkUpdater) set(stage string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.view.Stage = stage
	f.view.Error = ""
	if err != nil {
		f.view.Error = err.Error()
	}
}
func (f *FrameworkUpdater) context(ctx context.Context) error {
	f.mu.Lock()

	if f.settings != nil {
		override := strings.TrimSpace(f.settings.Get().FrameworkAdminURL)
		if override != "" {
			if err := validateFrameworkAdminURL(override); err != nil {
				f.mu.Unlock()
				return err
			}
			u, _ := url.Parse(override)
			f.base = strings.TrimRight(override, "/")
			f.manualBase = true
			f.view.Origin = u.Scheme + "://" + u.Host
			f.mu.Unlock()
			return nil
		}
		if f.manualBase {
			f.base = ""
			f.manualBase = false
		}
	}
	base := f.base
	f.mu.Unlock()
	if base != "" {
		return nil
	}
	raw, err := f.engine.Client.Call(ctx, "get_plugin_context", M{})
	if err != nil {
		return errors.New("无法读取当前萌卡的管理地址，请确认插件连接已建立")
	}
	var data struct {
		Base string `json:"admin_base_url"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return errors.New("萌卡返回的管理上下文无效")
	}
	if strings.TrimSpace(data.Base) == "" {

		candidate := "http://127.0.0.1:7891"
		f.mu.Lock()
		f.base = candidate
		f.mu.Unlock()
		status, probe, probeErr := f.request(ctx, "GET", "/api/v1/auth/session", "", nil)
		valid := false
		if probeErr == nil {
			var envelope struct {
				Code *int            `json:"code"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(probe, &envelope) == nil && envelope.Code != nil {
				if status == http.StatusUnauthorized {
					valid = true
				} else if status >= 200 && status < 300 && *envelope.Code == 0 {
					var session struct {
						Authenticated *bool `json:"authenticated"`
					}
					valid = json.Unmarshal(envelope.Data, &session) == nil && session.Authenticated != nil
				}
			}
		}
		if !valid {
			f.mu.Lock()
			f.base = ""
			f.mu.Unlock()
			return errors.New("萌卡没有返回管理地址，默认接口 http://127.0.0.1:7891 也不可用；请在“连接与更新”填写本机管理接口地址")
		}
		f.mu.Lock()
		f.view.Origin = candidate
		f.mu.Unlock()
		return nil
	}
	u, err := url.Parse(data.Base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("当前萌卡未提供有效的管理地址，请在萌卡中设置本机的管理访问地址")
	}
	f.mu.Lock()
	f.base = strings.TrimRight(u.String(), "/")
	f.view.Origin = u.Scheme + "://" + u.Host
	f.mu.Unlock()
	return nil
}
func (f *FrameworkUpdater) request(ctx context.Context, method, path, contentType string, body io.Reader) (int, json.RawMessage, error) {
	if err := f.context(ctx); err != nil {
		return 0, nil, err
	}
	f.mu.Lock()
	base := f.base
	f.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return 0, nil, err
	}
	u, _ := url.Parse(base)
	req.Header.Set("Origin", u.Scheme+"://"+u.Host)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, c := range f.client.Jar.Cookies(u) {
		if c.Name == "XSRF-TOKEN" {
			value, _ := url.QueryUnescape(c.Value)
			req.Header.Set("X-XSRF-TOKEN", value)
		}
	}
	res, err := f.client.Do(req)
	if err != nil {
		return 0, nil, errors.New("无法连接当前萌卡的管理接口，请检查它的访问地址")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return res.StatusCode, nil, errors.New("框架响应读取失败或过大")
	}
	return res.StatusCode, raw, nil
}
func frameworkData(status int, raw json.RawMessage) (json.RawMessage, error) {
	var data struct {
		Code    *int            `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Code == nil {
		return nil, errors.New("框架没有返回有效的管理接口响应")
	}
	if status < 200 || status >= 300 || *data.Code != 0 {
		if data.Message == "" {
			data.Message = "框架未接受操作"
		}
		return data.Data, errors.New(data.Message)
	}
	return data.Data, nil
}
func (f *FrameworkUpdater) Session(ctx context.Context) (bool, error) {
	status, raw, err := f.request(ctx, "GET", "/api/v1/auth/session", "", nil)
	if err != nil {
		return false, err
	}
	if status == 401 {
		return false, nil
	}
	data, err := frameworkData(status, raw)
	if err != nil {
		return false, err
	}
	var v struct {
		Authenticated bool `json:"authenticated"`
	}
	err = json.Unmarshal(data, &v)
	return v.Authenticated, err
}
func (f *FrameworkUpdater) Captcha(ctx context.Context) (json.RawMessage, error) {
	f.requestMu.Lock()
	defer f.requestMu.Unlock()
	status, raw, err := f.request(ctx, "GET", "/api/v1/auth/captcha", "", nil)
	if err != nil {
		return nil, err
	}
	return frameworkData(status, raw)
}
func (f *FrameworkUpdater) Login(ctx context.Context, username, password, id, code string) error {
	if username == "" || password == "" || id == "" || code == "" || len(username) > 200 || len(password) > 512 || len(id) > 256 || len(code) > 16 {
		return errors.New("请填写当前萌卡的管理员账号、密码和图形验证码")
	}
	f.requestMu.Lock()
	defer f.requestMu.Unlock()
	body, _ := json.Marshal(M{"method": "password", "username": username, "password": password, "keepActive": true, "captchaId": id, "captchaCode": code})
	status, raw, err := f.request(ctx, "POST", "/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	_, err = frameworkData(status, raw)
	return err
}
func (f *FrameworkUpdater) Start(approve bool) error {
	if f.updater.ctx.Err() != nil {
		return errors.New("更新服务正在停止")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closing || f.updater.ctx.Err() != nil {
		return errors.New("更新服务正在停止")
	}
	if f.view.Stage == "installing" {
		return errors.New("更新已提交给萌卡，正在等待插件重启")
	}
	if !approve && f.view.Stage == "approval_required" {
		return errors.New("新版有权限变化，请先在页面确认")
	}
	if f.busy {
		return errors.New("在线更新正在进行")
	}
	if approve && (f.approval == "" || time.Now().After(f.approvalUntil)) {
		return errors.New("权限确认已过期，请重新发起更新")
	}
	if !approve {
		f.archive = nil
		f.approval = ""
		f.view.Changes = nil
	}
	f.busy = true
	f.view.Stage = "checking"
	f.view.Error = ""
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer func() { f.mu.Lock(); f.busy = false; f.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(f.updater.ctx, 8*time.Minute)
		defer cancel()
		if err := f.run(ctx, approve); err != nil {
			f.set("error", err)
		}
	}()
	return nil
}
func (f *FrameworkUpdater) run(ctx context.Context, approve bool) error {
	var release Release
	if !approve {
		var err error
		release, err = readSignedRelease(ctx)
		if err != nil {
			return err
		}
		f.mu.Lock()
		f.view.Target = release.Version
		f.updater.mu.Lock()
		f.updater.state.Latest = release.Version
		f.updater.state.Notes = release.Notes
		f.updater.state.Checked = time.Now().Format(time.RFC3339)
		f.updater.state.Status = releaseStatus(Version, release.Version)
		f.updater.mu.Unlock()
		f.mu.Unlock()
		if !versionNewer(release.Version, Version) {
			f.set("current", nil)
			return nil
		}
	}
	ok, err := f.Session(ctx)
	if err != nil {
		return err
	}
	if !ok {
		f.set("login_required", nil)
		return nil
	}
	policy, err := f.updater.refreshPolicy()
	if err != nil && policy.Revision == 0 {
		return err
	}
	if policy.Revision == 0 || time.Now().Unix() >= policy.Expires || policy.Paused {
		return errors.New("当前没有有效的更新信息")
	}
	if approve {
		target := f.View().Target
		if versionNewer(policy.Minimum, target) || (policy.Force && versionNewer(policy.Target, target)) {
			return errors.New("版本要求已变化，请重新检查更新")
		}
	}
	if !approve {
		if versionNewer(policy.Minimum, release.Version) || (policy.Force && versionNewer(policy.Target, release.Version)) {
			return errors.New("更新源还未同步到所需版本")
		}
		asset, name, err := releasePackage(release, runtime.GOOS+"-"+runtime.GOARCH)
		if err != nil {
			return err
		}
		f.set("downloading", nil)
		archive, err := f.updater.downloadAsset(ctx, asset)
		if err != nil {
			return err
		}
		f.mu.Lock()
		f.archive, f.name = archive, name
		f.mu.Unlock()
	}
	f.set("uploading", nil)
	f.mu.Lock()
	archive, name, token := f.archive, f.name, f.approval
	f.mu.Unlock()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("package", name)
	if err != nil {
		return err
	}
	if _, err = part.Write(archive); err != nil {
		return err
	}
	_ = writer.WriteField("trusted_source_ack", "true")
	_ = writer.WriteField("capability_approval", token)
	if err = writer.Close(); err != nil {
		return err
	}
	status, raw, err := f.request(ctx, "POST", "/api/v1/plugins/managed/import", writer.FormDataContentType(), &body)
	if err != nil {
		return err
	}
	data, err := frameworkData(status, raw)
	if err != nil {
		var reason struct {
			Reason   string `json:"reason"`
			Approval string `json:"approval_token"`
		}
		_ = json.Unmarshal(data, &reason)
		if reason.Reason == "capability_change" && reason.Approval != "" {
			var changes M
			_ = json.Unmarshal(data, &changes)
			delete(changes, "approval_token")
			safe, _ := json.Marshal(changes)
			f.mu.Lock()
			f.approval = reason.Approval
			f.approvalUntil = time.Now().Add(10 * time.Minute)
			f.view.Stage = "approval_required"
			f.view.Changes = safe
			f.mu.Unlock()
			return nil
		}
		return fmt.Errorf("框架未接收更新：%w", err)
	}
	f.mu.Lock()
	f.archive = nil
	f.approval = ""
	f.view.Changes = nil
	f.view.Stage = "installing"
	f.mu.Unlock()
	return nil
}

func (f *FrameworkUpdater) stop() { f.mu.Lock(); f.closing = true; f.mu.Unlock(); f.wg.Wait() }
