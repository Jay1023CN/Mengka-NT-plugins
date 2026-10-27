// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/mengka"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func managedAdminFixture(t *testing.T) (*Managed, *Engine, *Store, string, string) {
	t.Helper()
	root := t.TempDir()
	data, private := filepath.Join(root, "data"), filepath.Join(root, "private")
	for _, dir := range []string{data, private} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(private, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ws.token", strings.Repeat("c", 32))
	write("admin.token", strings.Repeat("d", 32))
	spec, err := json.Marshal(map[string]any{
		"schema":              1,
		"transport":           "websocket-v1",
		"instance_id":         "admin-test",
		"websocket_url":       "ws://127.0.0.1:1/",
		"token_file":          filepath.Join(private, "ws.token"),
		"data_directory":      data,
		"allowed_directories": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	write("connection.json", string(spec))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	port := addr[strings.LastIndex(addr, ":")+1:]
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	env := envMap{
		"MENGKA_MANAGED_V1":              "1",
		"MENGKA_PLUGIN_CONNECTION_FILE":  filepath.Join(private, "connection.json"),
		"MENGKA_PLUGIN_DATA_DIR":         data,
		"MENGKA_PLUGIN_ADMIN_HOST":       "127.0.0.1",
		"MENGKA_PLUGIN_ADMIN_PORT":       port,
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
	settings, err := LoadSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	client := mengka.New(io.Discard, Actions)
	managed.Client = client
	engine := NewEngine(client, store, data)
	updater := NewUpdater(settings, data, "http://"+managed.AdminAddr()+"/api/managed/health", func(string) {})
	forward := &mengka.Forward{Client: client, GetConnection: managed.Connection, Permissions: EventPermissions, Version: Version, ManagedGate: managed.Active}
	server, err := StartAdmin(engine, settings, forward, updater, managed.AdminAddr(), managed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		server.Close()
		updater.Stop()
		engine.Stop()
		store.Close()
	})
	return managed, engine, store, "http://" + managed.AdminAddr(), managed.adminToken
}

func managedRequest(t *testing.T, method, url, token, origin string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Mengka-Managed-Token", token)
	req.Header.Set("X-Mengka-Managed-Origin", origin)
	req.Header.Set("X-Catherina-UI", "1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestManagedInstallDelegatedToFramework(t *testing.T) {
	managed, _, _, base, token := managedAdminFixture(t)
	resp := managedRequest(t, http.MethodPost, base+"/api/update/install", token, managed.AdminOrigin(), nil)
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("托管安装应交给萌卡框架，实际 HTTP %d：%s", resp.StatusCode, body)
	}
}

func TestMonitorEndpointsScopedByAccount(t *testing.T) {
	managed, engine, store, base, token := managedAdminFixture(t)
	engine.accountsMu.Lock()
	engine.accounts["10001:android"] = Account{SelfID: 10001, ClientType: "android", Status: 1}
	engine.accountsMu.Unlock()
	if err := store.Log("10001:android", "group_message_received", "只属于这个号"); err != nil {
		t.Fatal(err)
	}
	if err := store.Log("10002:android", "group_message_received", "另一个号"); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"X-Catherina-Account": "10001:android"}
	resp := managedRequest(t, http.MethodGet, base+"/api/monitor", token, managed.AdminOrigin(), headers)
	if resp.StatusCode != 200 {
		t.Fatalf("读取监控记录失败：HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Items []MonitorRow `json:"items"`
	}
	if json.NewDecoder(resp.Body).Decode(&payload) != nil {
		t.Fatal("监控记录响应不是合法 JSON")
	}
	if len(payload.Items) != 1 || payload.Items[0].Body != "只属于这个号" {
		t.Fatalf("接口串号了：%v", payload.Items)
	}
	resp = managedRequest(t, http.MethodGet, base+"/api/monitor", token, managed.AdminOrigin(), map[string]string{"X-Catherina-Account": "99999:android"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知账号必须 400，实际 HTTP %d", resp.StatusCode)
	}
	resp = managedRequest(t, http.MethodPost, base+"/api/monitor/clear", token, managed.AdminOrigin(), headers)
	if resp.StatusCode != 200 {
		t.Fatalf("清空监控记录失败：HTTP %d", resp.StatusCode)
	}
	if rows, err := store.Monitor("10001:android", 10); err != nil || len(rows) != 0 {
		t.Fatalf("清空未生效：%v %v", rows, err)
	}
	other, err := store.Monitor("10002:android", 10)
	if err != nil || len(other) != 1 {
		t.Fatalf("清空影响了其他账号：%v %v", other, err)
	}
}
