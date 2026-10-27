// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package mengka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"log"
	"net/url"
	"os"
	"sync"
	"time"
)

type Connection struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	Transport string `json:"transport"`
}

var (
	wsAuthDeadline = 10 * time.Second
	wsIdleDeadline = 75 * time.Second
)

type wsWriter struct{ conn *websocket.Conn }

func (w wsWriter) Write(p []byte) (int, error) {
	_ = w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := w.conn.WriteMessage(websocket.TextMessage, bytes.TrimSpace(p))
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

type Forward struct {
	Client        *Client
	GetConnection func() Connection
	Permissions   []string
	Version       string

	ManagedGate func() bool
	mu          sync.Mutex
	conn        *websocket.Conn
}

func (f *Forward) Reconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn != nil {
		_ = f.conn.Close()
	}
}
func (f *Forward) Run(ctx context.Context) {
	defer f.Client.Close()
	go func() { <-ctx.Done(); f.Reconnect() }()
	delay := time.Second
	for ctx.Err() == nil {
		config := f.GetConnection()
		if config.URL == "" || config.Token == "" {
			if !pause(ctx, 2*time.Second) {
				return
			}
			continue
		}
		started := time.Now()
		err := f.connect(ctx, config)
		f.Client.Disconnect()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("萌卡连接断开：%v；稍后重新连接", err)
		}
		if time.Since(started) > time.Minute {
			delay = time.Second
		}
		if !pause(ctx, delay) {
			return
		}
		delay = min(delay*2, 30*time.Second)
	}
}
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
func (f *Forward) connect(ctx context.Context, config Connection) error {
	u, err := url.Parse(config.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("WebSocket 地址无效，令牌请填写在独立字段")
	}
	conn, _, err := (&websocket.Dialer{HandshakeTimeout: 10 * time.Second}).DialContext(ctx, config.URL, nil)
	if err != nil {
		return errors.New("WebSocket 连接未建立，请检查萌卡地址和服务是否启动")
	}
	defer conn.Close()
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		if f.conn == conn {
			f.conn = nil
		}
		f.mu.Unlock()
	}()
	f.Client.writeMu.Lock()
	f.Client.writer = wsWriter{conn}
	f.Client.writeMu.Unlock()
	permissions := map[string]bool{}
	for _, p := range f.Permissions {
		permissions[p] = true
	}
	if err = f.Client.Write(map[string]any{"type": "auth", "token": config.Token, "plugin_id": "catherina", "name": "Catherina", "version": f.Version, "author": "Catherina", "permissions": permissions}); err != nil {
		return errors.New("WebSocket 认证发送失败")
	}
	conn.SetReadLimit(8 * 1024 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(wsAuthDeadline))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(wsIdleDeadline)) })
	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()
	authenticated := false
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return errors.New("WebSocket 保活超时，萌卡长时间没有响应")
			}
			return errors.New("WebSocket 已关闭")
		}
		var frame Frame
		if json.Unmarshal(data, &frame) != nil {
			return errors.New("萌卡返回的消息不是有效 JSON")
		}
		if !authenticated {
			if frame.Type == "auth_failed" {
				return errors.New("服务令牌认证失败，请检查萌卡插件对接设置")
			}
			if frame.Type != "auth_ok" {
				continue
			}
			authenticated = true
			f.Client.ready.Store(true)
			_ = conn.SetReadDeadline(time.Now().Add(wsIdleDeadline))
			if f.ManagedGate != nil && !f.ManagedGate() {
				log.Print("托管连接已认证，等待框架 activate 指令")
			} else {
				f.Client.active.Store(true)
				if f.Client.OnLifecycle != nil {
					f.Client.OnLifecycle("activate")
				}
				log.Print("萌卡 WebSocket 已认证")
			}
			continue
		}
		switch frame.Type {
		case "event":
			if f.Client.OnEvent != nil {
				f.Client.OnEvent(frame.Data)
			}
		case "action_result":
			f.Client.mu.Lock()
			ch := f.Client.pending[frame.ID]
			delete(f.Client.pending, frame.ID)
			f.Client.mu.Unlock()
			if ch != nil {
				r := Result{Data: frame.Data}
				if !frame.OK {
					r.Err = errors.New(frame.Error)
					if frame.Error == "" {
						r.Err = errors.New("萌卡接口返回失败")
					}
				}
				ch <- r
			}
		case "pong":
			_ = conn.SetReadDeadline(time.Now().Add(wsIdleDeadline))
		case "action_stream":
		}
	}
}
