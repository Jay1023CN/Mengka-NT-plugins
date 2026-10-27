// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/mengka"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
)

type ServiceConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`

	FrameworkAdminURL string `json:"framework_admin_url,omitempty"`
	PasswordSalt      string `json:"password_salt"`
	PasswordHash      string `json:"password_hash"`
	AutoUpdate        bool   `json:"auto_update"`
}
type ServiceSettings struct {
	store *Store
	mu    sync.RWMutex
	value ServiceConfig
}

func LoadSettings(store *Store) (*ServiceSettings, error) {
	v := ServiceConfig{AutoUpdate: true}
	var raw string
	err := store.db.QueryRow("SELECT data FROM service_settings WHERE id=1").Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
	}
	return &ServiceSettings{store: store, value: v}, nil
}
func (s *ServiceSettings) Get() ServiceConfig { s.mu.RLock(); defer s.mu.RUnlock(); return s.value }
func (s *ServiceSettings) Connection() mengka.Connection {
	v := s.Get()
	return mengka.Connection{URL: v.URL, Token: v.Token, Transport: "websocket-v1"}
}
func (s *ServiceSettings) Update(fn func(*ServiceConfig) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.value
	if err := fn(&v); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = s.store.db.Exec("INSERT INTO service_settings(id,data) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", string(raw)); err != nil {
		return err
	}
	s.value = v
	return nil
}
func (s *ServiceSettings) SetPassword(password string) error {
	if len([]rune(password)) < 8 || len(password) > 512 {
		return errors.New("管理密码应为 8–512 个字符")
	}
	salt := make([]byte, 24)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, 150000, 32)
	if err != nil {
		return err
	}
	return s.Update(func(v *ServiceConfig) error {
		if v.PasswordHash != "" {
			return errors.New("管理密码已经设置")
		}
		v.PasswordSalt = hex.EncodeToString(salt)
		v.PasswordHash = hex.EncodeToString(key)
		return nil
	})
}
func (s *ServiceSettings) CheckPassword(password string) bool {
	v := s.Get()
	salt, err := hex.DecodeString(v.PasswordSalt)
	if err != nil || v.PasswordHash == "" || len(password) > 512 {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, 150000, 32)
	return err == nil && subtle.ConstantTimeCompare([]byte(hex.EncodeToString(key)), []byte(v.PasswordHash)) == 1
}
func validateConnection(addr string) error {
	if addr == "" {
		return nil
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(addr, "\r\n") {
		return errors.New("请输入 ws:// 或 wss:// 地址，不要把令牌放进 URL")
	}
	return nil
}

func validateFrameworkAdminURL(addr string) error {
	if addr == "" {
		return nil
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(addr, "\r\n") {
		return errors.New("请输入萌卡管理接口的 HTTP 或 HTTPS 根地址")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("管理接口覆盖地址必须是本机回环地址，例如 http://127.0.0.1:7891")
	}
	return nil
}
