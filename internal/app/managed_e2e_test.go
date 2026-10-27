// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/mengka"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestManagedActivationEndToEnd(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	private := filepath.Join(root, "private")
	for _, dir := range []string{data, private} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	wsToken := strings.Repeat("a", 32)
	adminToken := strings.Repeat("b", 32)
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(private, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ws.token", wsToken)
	write("admin.token", adminToken)

	authenticated := make(chan struct{})
	framework := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("框架升级失败：%v", err)
			return
		}
		defer conn.Close()
		var auth struct {
			Type  string `json:"type"`
			Token string `json:"token"`
		}
		if err = conn.ReadJSON(&auth); err != nil {
			t.Errorf("读取 auth 失败：%v", err)
			return
		}
		if auth.Type != "auth" || auth.Token != wsToken {
			t.Errorf("握手不符：type=%q token 匹配=%v", auth.Type, auth.Token == wsToken)
			return
		}
		if err = conn.WriteJSON(map[string]any{"type": "auth_ok"}); err != nil {
			t.Errorf("回 auth_ok 失败：%v", err)
			return
		}
		close(authenticated)
		_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer framework.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	adminPort := strings.TrimPrefix(listener.Addr().(*net.TCPAddr).String()[strings.LastIndex(listener.Addr().(*net.TCPAddr).String(), ":"):], ":")
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}

	spec, err := json.Marshal(map[string]any{
		"schema":              1,
		"transport":           "websocket-v1",
		"instance_id":         "e2e-instance",
		"websocket_url":       "ws" + strings.TrimPrefix(framework.URL, "http") + "/",
		"token_file":          filepath.Join(private, "ws.token"),
		"data_directory":      data,
		"allowed_directories": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	write("connection.json", string(spec))

	env := envMap{
		"MENGKA_MANAGED_V1":              "1",
		"MENGKA_PLUGIN_CONNECTION_FILE":  filepath.Join(private, "connection.json"),
		"MENGKA_PLUGIN_DATA_DIR":         data,
		"MENGKA_PLUGIN_ADMIN_HOST":       "127.0.0.1",
		"MENGKA_PLUGIN_ADMIN_PORT":       adminPort,
		"MENGKA_PLUGIN_ADMIN_ORIGIN":     "http://127.0.0.1:18443",
		"MENGKA_PLUGIN_ADMIN_TOKEN_FILE": filepath.Join(private, "admin.token"),
	}
	managed, err := ResolveManaged(env.getenv, env.environ)
	if err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, err := LoadSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	client := mengka.New(io.Discard, Actions)
	managed.Client = client
	engine := NewEngine(client, store, data)
	defer engine.Stop()
	client.OnEvent = engine.Submit
	client.OnLifecycle = func(event string) {
		if event == "activate" {
			engine.Start()
		}
	}
	updater := NewUpdater(settings, data, "http://"+managed.AdminAddr()+"/api/managed/health", func(string) {})
	defer updater.Stop()
	forward := &mengka.Forward{Client: client, GetConnection: managed.Connection, Permissions: EventPermissions, Version: Version, ManagedGate: managed.Active}
	server, err := StartAdmin(engine, settings, forward, updater, managed.AdminAddr(), managed)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go forward.Run(ctx)

	select {
	case <-authenticated:
	case <-time.After(10 * time.Second):
		t.Fatal("插件没有完成托管 WebSocket 认证")
	}

	base := "http://" + managed.AdminAddr()
	httpClient := &http.Client{Timeout: 3 * time.Second}

	var health map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest("GET", base+"/api/managed/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Mengka-Managed-Token", adminToken)
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("健康探测失败：%v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("健康探测 HTTP %d：%s", resp.StatusCode, body)
		}
		health = mustJSON(t, body)
		if health["ready"] == true {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if health["ready"] != true {
		t.Fatalf("框架探测不到就绪，插件永远不会被 activate：%v", health)
	}

	req, err := http.NewRequest("POST", base+"/api/managed/control", strings.NewReader(`{"action":"activate"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Mengka-Managed-Token", adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("activate 请求失败：%v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || mustJSON(t, body)["active"] != true {
		t.Fatalf("activate 未生效：HTTP %d %s", resp.StatusCode, body)
	}
	if !managed.Active() || !client.Active() {
		t.Fatalf("activate 后状态未放行：managed=%v client=%v", managed.Active(), client.Active())
	}
	if !engine.started.Load() {
		t.Fatal("activate 后引擎必须开始处理事件")
	}

	resp, err = httpClient.Get(base + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("预期 /api/auth/status 在无托管头时返回 401，实际 HTTP %d", resp.StatusCode)
	}
}

func mustJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("响应不是 JSON：%s", body)
	}
	return out
}
