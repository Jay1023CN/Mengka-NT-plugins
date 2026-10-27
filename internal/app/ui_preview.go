//go:build catherina_ui_preview

// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"bytes"
	"catherina/internal/mengka"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func RunUIPreview(ctx context.Context, root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, ".build", "ui-preview-data")
	store, err := OpenStore(dir)
	if err != nil {
		return err
	}
	defer store.Close()
	settings, err := LoadSettings(store)
	if err != nil {
		return err
	}
	if err = settings.Update(func(c *ServiceConfig) error {
		c.PasswordHash, c.PasswordSalt, c.URL, c.Token = "", "", "", ""
		c.AutoUpdate = false
		return nil
	}); err != nil {
		return err
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return err
	}
	password := hex.EncodeToString(secret[:])
	if err = settings.SetPassword(password); err != nil {
		return err
	}
	engine := NewEngine(mengka.New(io.Discard, Actions), store, dir)
	defer engine.Stop()
	for _, account := range []Account{{SelfID: 100000001, ClientType: "android", Nickname: "预览账号一"}, {SelfID: 100000002, ClientType: "linuxqq", Nickname: "预览账号二"}} {
		engine.accounts[account.Key()] = account
	}
	forward := &mengka.Forward{Client: engine.Client, GetConnection: settings.Connection, Permissions: EventPermissions, Version: Version}
	updater := NewUpdater(settings, dir, "", func(string) {})
	defer updater.Stop()
	const internalAddress = "127.0.0.1:18797"
	admin, err := StartAdmin(engine, settings, forward, updater, internalAddress, nil)
	if err != nil {
		return err
	}
	defer admin.Close()
	body, _ := json.Marshal(M{"password": password})
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+internalAddress+"/api/auth/login", bytes.NewReader(body))
	req.Header.Set("X-Catherina-UI", "1")
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || len(res.Cookies()) == 0 {
		return fmt.Errorf("preview session unavailable: %d", res.StatusCode)
	}
	cookies := res.Cookies()
	staticRoot := filepath.Join(root, "internal", "app", "web")
	files := http.FileServer(http.Dir(staticRoot))
	previewEpoch := time.Now().UnixNano()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1:18796" && r.Host != "localhost:18796" {
			http.Error(w, "preview host mismatch", 403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/api/auth/status" {
			respond(w, 200, M{"authenticated": true, "setup": false})
			return
		}
		if r.URL.Path == "/api/status" {
			respond(w, 200, M{"version": Version, "preview": true, "ready": false, "active": false, "managed": false, "accounts": engine.Accounts(), "features": Features, "commands": visibleCommands(), "actions": Actions, "database": "独立预览配置", "dropped": 0})
			return
		}
		if r.URL.Path == "/api/contacts" && r.Method == "GET" {
			if r.URL.Query().Get("kind") == "friends" {
				respond(w, 200, M{"kind": "friends", "count": 4, "items": []M{{"id": "10001", "nickname": "小满", "remark": "小满"}, {"id": "10002", "nickname": "阿澄", "remark": "同事·阿澄"}, {"id": "10003", "nickname": "小夏", "remark": ""}, {"id": "10004", "nickname": "Catherine", "remark": "Catherine"}}})
			} else {
				respond(w, 200, M{"kind": "groups", "count": 3, "items": []M{{"id": "20001", "name": "周末出游计划"}, {"id": "20002", "name": "家人群"}, {"id": "20003", "name": "读书会"}}})
			}
			return
		}
		if r.URL.Path == "/api/dev/revision" {
			h := sha256.New()
			_, _ = fmt.Fprint(h, previewEpoch)
			for _, name := range []string{"index.html", "app.css", "app.js", "favicon.svg"} {
				data, _ := os.ReadFile(filepath.Join(staticRoot, name))
				_, _ = h.Write(data)
			}
			respond(w, 200, M{"revision": hex.EncodeToString(h.Sum(nil))})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			files.ServeHTTP(w, r)
			return
		}
		allowed := r.Method == "GET" && (r.URL.Path == "/api/service" || r.URL.Path == "/api/monitor")
		for _, path := range []string{"/api/config", "/api/global", "/api/global/init", "/api/menus/defaults", "/api/jobs", "/api/monitor/clear"} {
			allowed = allowed || r.URL.Path == path
		}
		if !allowed {
			respond(w, 409, M{"error": "当前是界面预览，可保存配置；QQ 收发与更新请在正式插件中操作。"})
			return
		}
		clone := r.Clone(r.Context())
		clone.Host = internalAddress
		clone.Header = r.Header.Clone()
		clone.Header.Del("Cookie")
		for _, cookie := range cookies {
			clone.AddCookie(cookie)
		}
		admin.Handler.ServeHTTP(w, clone)
	})
	server := &http.Server{Addr: "127.0.0.1:18796", Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = server.Close() }()
	if err = server.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
