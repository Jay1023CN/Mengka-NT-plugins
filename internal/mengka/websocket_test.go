// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package mengka

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func fakeFramework(t *testing.T) (url string, authed <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("框架升级 WebSocket 失败：%v", err)
			return
		}
		defer conn.Close()
		var frame struct {
			Type string `json:"type"`
		}
		if err = conn.ReadJSON(&frame); err != nil || frame.Type != "auth" {
			t.Errorf("未收到 auth 握手，实际收到 %q（%v）", frame.Type, err)
			return
		}
		if err = conn.WriteJSON(map[string]any{"type": "auth_ok"}); err != nil {
			t.Errorf("框架回 auth_ok 失败：%v", err)
			return
		}
		close(done)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), done
}

func TestManagedGateKeepsConnectionPastAuthDeadline(t *testing.T) {
	oldAuth, oldIdle := wsAuthDeadline, wsIdleDeadline
	wsAuthDeadline, wsIdleDeadline = 200*time.Millisecond, 10*time.Second
	t.Cleanup(func() { wsAuthDeadline, wsIdleDeadline = oldAuth, oldIdle })

	wsURL, authed := fakeFramework(t)
	var mu sync.Mutex
	lifecycle := []string{}
	client := New(io.Discard, []string{"get_bot_list"})
	client.OnLifecycle = func(event string) {
		mu.Lock()
		lifecycle = append(lifecycle, event)
		mu.Unlock()
	}
	forward := &Forward{
		Client:        client,
		GetConnection: func() Connection { return Connection{URL: wsURL, Token: strings.Repeat("t", 24)} },
		Permissions:   []string{"group_message"},
		Version:       "test",
		ManagedGate:   func() bool { return false },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go forward.Run(ctx)

	select {
	case <-authed:
	case <-time.After(3 * time.Second):
		t.Fatal("插件没有完成 WebSocket 认证")
	}
	time.Sleep(800 * time.Millisecond)
	if !client.Ready() {
		t.Fatal("等待框架 activate 期间连接被误判为断开")
	}
	if client.Active() {
		t.Fatal("托管模式下框架尚未 activate，不应放行业务")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lifecycle) != 0 {
		t.Fatalf("托管模式下不应自动发出生命周期事件：%v", lifecycle)
	}
}

func TestManagedGateActivatesWhenFrameworkAlreadyActive(t *testing.T) {
	oldAuth, oldIdle := wsAuthDeadline, wsIdleDeadline
	wsAuthDeadline, wsIdleDeadline = 200*time.Millisecond, 10*time.Second
	t.Cleanup(func() { wsAuthDeadline, wsIdleDeadline = oldAuth, oldIdle })

	wsURL, authed := fakeFramework(t)
	var mu sync.Mutex
	lifecycle := []string{}
	client := New(io.Discard, []string{"get_bot_list"})
	client.OnLifecycle = func(event string) {
		mu.Lock()
		lifecycle = append(lifecycle, event)
		mu.Unlock()
	}
	forward := &Forward{
		Client:        client,
		GetConnection: func() Connection { return Connection{URL: wsURL, Token: strings.Repeat("t", 24)} },
		Permissions:   []string{"group_message"},
		Version:       "test",
		ManagedGate:   func() bool { return true },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go forward.Run(ctx)

	select {
	case <-authed:
	case <-time.After(3 * time.Second):
		t.Fatal("插件没有完成 WebSocket 认证")
	}
	time.Sleep(800 * time.Millisecond)
	if !client.Ready() || !client.Active() {
		t.Fatalf("重连后应恢复已激活状态：ready=%v active=%v", client.Ready(), client.Active())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lifecycle) != 1 || lifecycle[0] != "activate" {
		t.Fatalf("期望一次 activate 生命周期回调，实际 %v", lifecycle)
	}
}
