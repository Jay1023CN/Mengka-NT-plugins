// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type webAuth struct {
	settings *ServiceSettings
	managed  *Managed
	mu       sync.Mutex
	sessions map[string]time.Time
	attempts map[string]time.Time
	probed   bool
	rejectAt time.Time
	rejected map[string]bool
}

func (a *webAuth) logManagedProbe(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.probed {
		return
	}
	a.probed = true
	log.Printf("框架控制平面已接通：%s %s", r.Method, r.URL.Path)
}

func (a *webAuth) logManagedReject(r *http.Request, status int, reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	first := !a.rejected[r.URL.Path]
	if !first && time.Now().Before(a.rejectAt) {
		return
	}
	if a.rejected == nil {
		a.rejected = map[string]bool{}
	}
	a.rejected[r.URL.Path] = true
	a.rejectAt = time.Now().Add(time.Minute)
	origin := r.Header.Get("X-Mengka-Managed-Origin")
	if origin == "" {
		origin = r.Header.Get("Origin")
	}
	if origin == "" {
		origin = "无"
	}
	log.Printf("托管管理请求被拒绝：%s %s（HTTP %d），原因：%s，来源头：%s", r.Method, r.URL.Path, status, reason, origin)
}

func newAuth(s *ServiceSettings) *webAuth {
	return &webAuth{settings: s, sessions: map[string]time.Time{}, attempts: map[string]time.Time{}}
}
func (a *webAuth) valid(r *http.Request) bool {
	cookie, err := r.Cookie("catherina_session")
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	until, ok := a.sessions[cookie.Value]
	return ok && time.Now().Before(until)
}
func (a *webAuth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if a.managed == nil {
			w.Header().Set("X-Frame-Options", "DENY")
		}
		if a.managed != nil {

			w.Header().Set("Content-Security-Policy", "frame-ancestors 'self' "+a.managed.AdminOrigin())
			if r.URL.Path == "/api/managed/health" {
				if r.Method != "GET" && r.Method != "HEAD" {
					w.WriteHeader(405)
					return
				}
				if !a.managed.authorizeControl(r) {
					a.logManagedReject(r, 401, a.managed.managedDeny(r))
					respond(w, 401, M{"error": "托管管理认证无效"})
					return
				}
				a.logManagedProbe(r)
				respond(w, 200, a.managed.health())
				return
			}
			if r.URL.Path == "/api/managed/control" {
				if r.Method != "POST" {
					w.WriteHeader(405)
					return
				}
				if !a.managed.authorizeControl(r) {
					a.logManagedReject(r, 401, a.managed.managedDeny(r))
					respond(w, 401, M{"error": "托管管理认证无效"})
					return
				}
				a.logManagedProbe(r)
				a.managedControl(w, r)
				return
			}

			if !a.managed.authorize(r) {
				a.logManagedReject(r, 401, a.managed.managedDeny(r))
				respond(w, 401, M{"error": "托管管理认证无效"})
				return
			}
		}
		if r.URL.Path == "/api/auth/status" {
			if a.managed != nil {
				respond(w, 200, M{"setup": false, "authenticated": true, "version": Version})
				return
			}
			respond(w, 200, M{"setup": a.settings.Get().PasswordHash == "", "authenticated": a.valid(r), "version": Version})
			return
		}
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			if r.Method != "GET" && r.Header.Get("X-Catherina-UI") != "1" {
				respond(w, 403, M{"error": "请求来源无效，请重新打开管理页面"})
				return
			}
			if r.URL.Path == "/api/auth/login" {
				if a.managed != nil {
					respond(w, 404, M{"error": "托管模式下由框架承担登录"})
					return
				}
				a.login(w, r)
				return
			}
			if a.managed == nil && !a.valid(r) {
				respond(w, 401, M{"error": "请先登录 Catherina"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *webAuth) managedControl(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if err := decode(w, r, &body); err != nil {
		return
	}
	active, err := a.managed.Activate(body.Action)
	if err != nil {
		respond(w, 400, M{"error": err.Error()})
		return
	}
	respond(w, 200, M{"active": active})
}
func (a *webAuth) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	until := a.attempts[host]
	if time.Now().Before(until) {
		a.mu.Unlock()
		respond(w, 429, M{"error": "操作太频繁，请稍等几秒"})
		return
	}
	a.attempts[host] = time.Now().Add(2 * time.Second)
	a.mu.Unlock()
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		return
	}
	if a.settings.Get().PasswordHash == "" {
		if err := a.settings.SetPassword(body.Password); err != nil {
			respond(w, 400, M{"error": err.Error()})
			return
		}
	} else if !a.settings.CheckPassword(body.Password) {
		respond(w, 401, M{"error": "管理密码不正确"})
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		respond(w, 500, M{"error": "生成登录凭据失败"})
		return
	}
	token := hex.EncodeToString(b)
	a.mu.Lock()
	for k, until := range a.sessions {
		if time.Now().After(until) {
			delete(a.sessions, k)
		}
	}
	a.sessions[token] = time.Now().Add(12 * time.Hour)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "catherina_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	respond(w, 200, M{"ok": true})
}
