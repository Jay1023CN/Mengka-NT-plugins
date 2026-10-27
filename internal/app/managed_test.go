// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/mengka"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func emptyEnv(key string) string { return "" }

type envMap map[string]string

func (e envMap) getenv(key string) string { return e[key] }
func (e envMap) environ() []string {
	values := []string{}
	for k, v := range e {
		values = append(values, k+"="+v)
	}
	return values
}

func managedFixture(t *testing.T) (envMap, string) {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	private := filepath.Join(root, "private")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
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
	spec, err := json.Marshal(map[string]any{
		"schema":              1,
		"transport":           "websocket-v1",
		"instance_id":         "demo-instance",
		"websocket_url":       "ws://127.0.0.1:3001/",
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
		"MENGKA_PLUGIN_ADMIN_PORT":       "3003",
		"MENGKA_PLUGIN_ADMIN_ORIGIN":     "http://127.0.0.1:18443",
		"MENGKA_PLUGIN_ADMIN_TOKEN_FILE": filepath.Join(private, "admin.token"),
	}
	return env, root
}

func TestResolveManagedAcceptsFixture(t *testing.T) {
	env, _ := managedFixture(t)
	m, err := ResolveManaged(env.getenv, env.environ)
	if err != nil {
		t.Fatalf("托管配置解析失败：%v", err)
	}
	if m.InstanceID() != "demo-instance" || m.AdminAddr() != "127.0.0.1:3003" {
		t.Fatalf("实例或管理地址不符：%s %s", m.InstanceID(), m.AdminAddr())
	}
	if m.Connection().URL != "ws://127.0.0.1:3001/" || len(m.Connection().Token) < minSecretLength {
		t.Fatalf("连接信息无效：%s", m.Connection().URL)
	}
	if !strings.HasSuffix(m.DataDir(), filepath.Join("data", "")) && !strings.HasSuffix(m.DataDir(), "data") {
		t.Fatalf("数据目录不符：%s", m.DataDir())
	}
}

func TestResolveManagedRejectsInvalidRuntime(t *testing.T) {
	env, root := managedFixture(t)
	private := filepath.Join(root, "private")
	file := filepath.Join(private, "connection.json")
	reject := func(label string, mutate func(spec map[string]any)) {
		t.Helper()
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var spec map[string]any
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		mutate(spec)
		text, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, text, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveManaged(env.getenv, env.environ); err == nil {
			t.Fatalf("%s 未被拒绝", label)
		}
	}
	defer func() { t.Cleanup(func() {}) }()
	reject("外网 WebSocket", func(spec map[string]any) { spec["websocket_url"] = "ws://example.test:3001/" })
	reject("携带查询参数", func(spec map[string]any) { spec["websocket_url"] = "ws://127.0.0.1:3001/?token=secret" })
	reject("令牌文件越界", func(spec map[string]any) { spec["token_file"] = filepath.Join(root, "outside.token") })
	write := specWriter(t, private)
	write(`{"schema":2,"transport":"websocket-v1"}`)
	if _, err := ResolveManaged(env.getenv, env.environ); err == nil {
		t.Fatal("schema 不符未被拒绝")
	}
	write(string(mustMarshal(map[string]any{})))
	if _, err := ResolveManaged(emptyEnv, environFor(
		"MENGKA_MANAGED_V1=1", "MENGKA_PLUGIN_CONNECTION_FILE="+filepath.Join(private, "missing.json"),
	)); err == nil {
		t.Fatal("缺失连接文件未报错")
	}
	stray := func(key string) string {
		if key == "MENGKA_MANAGED_V1" {
			return ""
		}
		if strings.HasPrefix(key, "MENGKA_PLUGIN_") {
			return "1"
		}
		return ""
	}
	if _, err := ResolveManaged(stray, environFor("MENGKA_PLUGIN_X=1")); err == nil {
		t.Fatal("不完整托管环境未被拒绝")
	}
	partial := func(key string) string {
		if key == "MENGKA_MANAGED_V1" {
			return "0"
		}
		return ""
	}
	if _, err := ResolveManaged(partial, environFor("MENGKA_MANAGED_V1=0")); err == nil {
		t.Fatal("MENGKA_MANAGED_V1 不为 1 时未被拒绝")
	}
}

func specWriter(t *testing.T, private string) func(string) {
	return func(content string) {
		if err := os.WriteFile(filepath.Join(private, "connection.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
func mustMarshal(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
func environFor(values ...string) func() []string {
	return func() []string { return values }
}

func TestManagedAuthorizationMatrix(t *testing.T) {
	env, _ := managedFixture(t)
	m, err := ResolveManaged(env.getenv, env.environ)
	if err != nil {
		t.Fatal(err)
	}
	m.Client = mengka.New(io.Discard, Actions)
	req := func(url string, headers map[string]string, remote string) *http.Request {
		r := httptest.NewRequest("POST", url, strings.NewReader(`{"action":"activate"}`))
		r.RemoteAddr = remote
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}
	token := m.adminToken
	controlHeaders := map[string]string{"X-Mengka-Managed-Token": token, "X-Mengka-Managed-Origin": m.adminOrigin}
	remote := "127.0.0.1:40000"
	if !m.authorizeControl(req("/api/managed/health", map[string]string{"X-Mengka-Managed-Token": token}, remote)) {
		t.Fatal("健康探针只需令牌")
	}
	if !m.authorizeControl(req("/api/managed/control", map[string]string{"X-Mengka-Managed-Token": token, "Origin": m.adminOrigin}, remote)) {
		t.Fatal("控制指令的浏览器 Origin 与管理来源一致时允许")
	}
	if !m.authorizeControl(req("/api/managed/control", map[string]string{"X-Mengka-Managed-Token": token, "Origin": "https://other.example:18443"}, remote)) {
		t.Fatal("面板经另一个域名访问时，回环令牌仍是唯一凭据，控制路径不应因浏览器 Origin 而拒绝")
	}
	if m.authorizeControl(req("/api/managed/control", map[string]string{"X-Mengka-Managed-Token": token + "x", "Origin": m.adminOrigin}, remote)) {
		t.Fatal("管理令牌不符必须拒绝")
	}
	if !m.authorize(req("/api/status", controlHeaders, remote)) {
		t.Fatal("业务 API 需要令牌与来源头")
	}
	if m.authorize(req("/api/status", map[string]string{"X-Mengka-Managed-Token": token}, remote)) {
		t.Fatal("缺少来源头必须拒绝")
	}
	if !m.authorize(req("/api/status", map[string]string{"X-Mengka-Managed-Token": token, "X-Mengka-Managed-Origin": "https://panel.example:18443"}, remote)) {
		t.Fatal("面板经其他域名访问时，网关已注入合法来源头就必须放行")
	}
	if m.authorize(req("/api/status", map[string]string{"X-Mengka-Managed-Token": token, "X-Mengka-Managed-Origin": "javascript:alert(1)"}, remote)) {
		t.Fatal("非法来源头必须拒绝")
	}
	if m.authorize(req("/api/status", map[string]string{"X-Mengka-Managed-Token": m.Connection().Token, "X-Mengka-Managed-Origin": m.adminOrigin}, remote)) {
		t.Fatal("连接令牌不能当作管理令牌")
	}
	if m.authorize(req("/api/status", controlHeaders, "203.0.113.1:5000")) {
		t.Fatal("非回环来源必须拒绝")
	}
}

func TestManagedLifecycleGating(t *testing.T) {
	env, _ := managedFixture(t)
	m, err := ResolveManaged(env.getenv, env.environ)
	if err != nil {
		t.Fatal(err)
	}
	m.Client = mengka.New(io.Discard, Actions)
	if m.Active() || m.Client.Active() {
		t.Fatal("托管初始必须等待 activate")
	}
	if _, err := m.Activate("delete"); err == nil {
		t.Fatal("未知控制指令必须报错")
	}
	if m.Client.Active() {
		t.Fatal("失败指令不得改变状态")
	}
	if active, err := m.Activate("activate"); err != nil || !active {
		t.Fatalf("activate 失败：%v", err)
	}
	if !m.Client.Active() {
		t.Fatal("activate 必须恢复事件提交")
	}
	if _, err := m.Activate("stop"); err != nil {
		t.Fatal(err)
	}
	if m.Client.Active() {
		t.Fatal("stop 必须暂停事件提交")
	}
}
