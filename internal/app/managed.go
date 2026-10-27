// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/mengka"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	maxConnectionBytes = 98304
	maxAdminTokenBytes = 4096
	minSecretLength    = 24
)

var instancePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type Managed struct {
	Client      *mengka.Client
	connection  mengka.Connection
	transport   string
	tokenPath   string
	adminAddr   string
	adminOrigin string
	adminToken  string
	dataDir     string
	instanceID  string
	active      atomic.Bool
}

func ResolveManaged(getenv func(string) string, environ func() []string) (*Managed, error) {
	managedFlag := getenv("MENGKA_MANAGED_V1")
	connectionFile := getenv("MENGKA_PLUGIN_CONNECTION_FILE")
	if managedFlag == "0" || (managedFlag != "" && managedFlag != "1") {
		return nil, errors.New("托管环境变量无效，MENGKA_MANAGED_V1 必须为 1")
	}
	if managedFlag == "" && connectionFile == "" {
		if hasManagedVariables(environ) {
			return nil, errors.New("托管环境变量不完整，缺少 MENGKA_PLUGIN_CONNECTION_FILE")
		}
		return nil, nil
	}

	file := connectionFile
	if file == "" || !filepath.IsAbs(file) {
		return nil, errors.New("托管连接文件路径无效")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("读取托管连接文件失败：%w", err)
	}
	if len(raw) > maxConnectionBytes {
		return nil, errors.New("托管连接文件超过 96KiB 上限")
	}
	var spec struct {
		Schema             int      `json:"schema"`
		Transport          string   `json:"transport"`
		InstanceID         string   `json:"instance_id"`
		WebsocketURL       string   `json:"websocket_url"`
		TokenFile          string   `json:"token_file"`
		DataDirectory      string   `json:"data_directory"`
		AllowedDirectories []string `json:"allowed_directories"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("托管连接文件无效：%w", err)
	}
	if spec.Schema != 1 || (spec.Transport != "websocket-v1" && spec.Transport != "native-ipc-v1") || !instancePattern.MatchString(spec.InstanceID) {
		return nil, errors.New("托管连接契约不支持：schema、传输类型或实例 ID 无效")
	}
	if spec.Transport == "websocket-v1" && managedFlag != "1" {
		return nil, errors.New("托管 WebSocket 必须由 MENGKA_MANAGED_V1=1 启动")
	}
	fileReal, err := realPath(file)
	if err != nil {
		return nil, fmt.Errorf("托管连接目录解析失败：%w", err)
	}
	connection := mengka.Connection{Transport: spec.Transport}
	if spec.Transport == "websocket-v1" {
		u, parseErr := url.Parse(spec.WebsocketURL)
		if parseErr != nil || u.Scheme != "ws" || u.Hostname() != "127.0.0.1" || u.Port() == "" ||
			u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("托管 WebSocket 地址必须是 ws://127.0.0.1:端口/ 回环地址")
		}
		if spec.TokenFile == "" || !filepath.IsAbs(spec.TokenFile) {
			return nil, errors.New("托管令牌文件路径无效")
		}
		tokenReal, pathErr := realPath(spec.TokenFile)
		if pathErr != nil {
			return nil, fmt.Errorf("托管令牌文件读取失败：%w", pathErr)
		}
		if filepath.Dir(tokenReal) != filepath.Dir(fileReal) {
			return nil, errors.New("托管令牌文件必须与连接文件放在同一目录")
		}
		tokenRaw, readErr := os.ReadFile(spec.TokenFile)
		if readErr != nil {
			return nil, fmt.Errorf("读取托管令牌失败：%w", readErr)
		}
		if len(tokenRaw) > maxAdminTokenBytes {
			return nil, errors.New("托管令牌过长")
		}
		token := strings.TrimSpace(string(tokenRaw))
		if len(token) < minSecretLength {
			return nil, errors.New("托管令牌无效（长度不足）")
		}
		connection.URL, connection.Token = spec.WebsocketURL, token
	} else if spec.WebsocketURL != "" || spec.TokenFile != "" {
		return nil, errors.New("原生 IPC 托管连接不得包含 WebSocket 地址或令牌文件")
	}
	m := &Managed{
		connection: connection,
		transport:  spec.Transport,
		tokenPath:  spec.TokenFile,
		dataDir:    getenv("MENGKA_PLUGIN_DATA_DIR"),
		instanceID: spec.InstanceID,
	}
	if m.dataDir == "" || !filepath.IsAbs(m.dataDir) {
		return nil, errors.New("托管数据目录路径无效")
	}
	dataReal, err := realPath(m.dataDir)
	if err != nil {
		return nil, fmt.Errorf("托管数据目录读取失败：%w", err)
	}
	specDataReal, err := realPath(spec.DataDirectory)
	if err != nil {
		return nil, fmt.Errorf("连接文件数据目录读取失败：%w", err)
	}
	if dataReal != specDataReal {
		return nil, errors.New("托管数据目录与连接文件不一致")
	}
	adminFile := getenv("MENGKA_PLUGIN_ADMIN_TOKEN_FILE")
	if adminFile == "" || !filepath.IsAbs(adminFile) {
		return nil, errors.New("托管管理令牌路径无效")
	}
	adminReal, err := realPath(adminFile)
	if err != nil {
		return nil, fmt.Errorf("托管管理令牌读取失败：%w", err)
	}
	if filepath.Dir(adminReal) != filepath.Dir(fileReal) {
		return nil, errors.New("托管管理令牌必须与连接文件放在同一目录")
	}
	adminRaw, err := os.ReadFile(adminFile)
	if err != nil {
		return nil, fmt.Errorf("读取管理令牌失败：%w", err)
	}
	if len(adminRaw) > maxAdminTokenBytes {
		return nil, errors.New("托管管理令牌过长")
	}
	m.adminToken = strings.TrimSpace(string(adminRaw))
	if len(m.adminToken) < minSecretLength {
		return nil, errors.New("托管管理令牌无效（长度不足）")
	}
	if getenv("MENGKA_PLUGIN_ADMIN_HOST") != "127.0.0.1" {
		return nil, errors.New("托管管理服务只能监听 127.0.0.1")
	}
	port, err := strconv.Atoi(getenv("MENGKA_PLUGIN_ADMIN_PORT"))
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("托管管理端口无效")
	}
	m.adminAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	origin := getenv("MENGKA_PLUGIN_ADMIN_ORIGIN")
	if origin == "" {
		origin = "http://127.0.0.1"
	}
	originURL, err := url.Parse(origin)
	if err != nil || (originURL.Scheme != "http" && originURL.Scheme != "https") || originURL.Host == "" ||
		originURL.User != nil || (originURL.Path != "" && originURL.Path != "/") || originURL.RawQuery != "" || originURL.Fragment != "" {
		return nil, errors.New("托管管理来源地址无效")
	}
	m.adminOrigin = originURL.Scheme + "://" + originURL.Host
	return m, nil
}
func hasManagedVariables(environ func() []string) bool {
	for _, entry := range environ() {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(name, "MENGKA_PLUGIN_") && name != "MENGKA_PLUGIN_" {
			return true
		}
	}
	return false
}
func realPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		resolved = path
	}
	if clean, err := filepath.Abs(resolved); err == nil {
		resolved = clean
	}
	return resolved, nil
}

func (m *Managed) InstanceID() string { return m.instanceID }

func (m *Managed) DataDir() string { return m.dataDir }

func (m *Managed) AdminAddr() string { return m.adminAddr }

func (m *Managed) Connection() mengka.Connection { return m.connection }

func (m *Managed) Transport() string { return m.transport }

func (m *Managed) Active() bool { return m.active.Load() }

func (m *Managed) Activate(action string) (bool, error) {
	if m.Client == nil {
		return false, errors.New("托管客户端尚未接入")
	}
	switch action {
	case "activate":
		m.active.Store(true)
		m.Client.SetActive(true)
		if m.Client.OnLifecycle != nil {
			m.Client.OnLifecycle("activate")
		}
	case "stop":
		m.active.Store(false)
		m.Client.SetActive(false)
	default:
		return false, errors.New("不支持的托管控制指令")
	}
	active := m.Active()
	log.Printf("框架控制指令 %s 已生效，业务状态 active=%v", action, active)
	return active, nil
}

func (m *Managed) managedDeny(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host != "127.0.0.1" && host != "::1" && host != "::ffff:127.0.0.1" {
		return "来源不是回环地址"
	}
	got := r.Header.Get("X-Mengka-Managed-Token")
	switch {
	case got == "":
		return "请求没有携带内部管理令牌头"
	case m.adminToken == "":
		return "本进程未获得管理令牌"
	case len(got) != len(m.adminToken) || subtle.ConstantTimeCompare([]byte(got), []byte(m.adminToken)) != 1:
		return "内部管理令牌不匹配"
	}
	if origin := r.Header.Get("X-Mengka-Managed-Origin"); origin != "" && !validOrigin(origin) {
		return "网关注入的管理来源头格式无效"
	}
	return "凭据校验通过，路径要求的管理来源头缺失"
}

func (m *Managed) authorizeCore(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host != "127.0.0.1" && host != "::1" && host != "::ffff:127.0.0.1" {
		return false
	}
	if m.adminToken == "" {
		return false
	}
	got := r.Header.Get("X-Mengka-Managed-Token")
	if len(got) != len(m.adminToken) || subtle.ConstantTimeCompare([]byte(got), []byte(m.adminToken)) != 1 {
		return false
	}
	return true
}

func (m *Managed) authorizeControl(r *http.Request) bool {
	return m.authorizeCore(r)
}

func (m *Managed) authorize(r *http.Request) bool {
	if !m.authorizeCore(r) {
		return false
	}
	return validOrigin(r.Header.Get("X-Mengka-Managed-Origin"))
}

func (m *Managed) managedOriginDrift(r *http.Request) (string, bool) {
	got := r.Header.Get("X-Mengka-Managed-Origin")
	if got == "" {
		got = r.Header.Get("Origin")
	}
	return got, got != "" && got != m.adminOrigin
}

func validOrigin(raw string) bool {
	if raw == "" || len(raw) > 256 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	return u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func (m *Managed) AdminOrigin() string { return m.adminOrigin }

func (m *Managed) health() M {
	online := m.Client.Ready()
	return M{"ready": online, "connected": online, "configured": true}
}
