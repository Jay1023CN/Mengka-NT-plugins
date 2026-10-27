// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package mengka

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type Result struct {
	Data json.RawMessage
	Err  error
}
type Frame struct {
	Type           string          `json:"type"`
	ID             string          `json:"id"`
	OK             bool            `json:"ok"`
	Error          string          `json:"error"`
	Transport      string          `json:"transport"`
	APIVersion     string          `json:"api_version"`
	FrameworkAPI   string          `json:"framework_api"`
	ConfigRevision *int64          `json:"config_revision"`
	Revision       int64           `json:"revision"`
	Config         map[string]any  `json:"config"`
	Data           json.RawMessage `json:"data"`
	Event          string          `json:"event"`
}
type Client struct {
	writer      io.Writer
	writeMu     sync.Mutex
	mu          sync.Mutex
	pending     map[string]chan Result
	seq         atomic.Uint64
	ready       atomic.Bool
	active      atomic.Bool
	modernIPC   atomic.Bool
	allowed     map[string]bool
	done        chan struct{}
	closeOnce   sync.Once
	OnEvent     func(json.RawMessage)
	OnLifecycle func(string)
	OnConfig    func(map[string]any, int64)
}

func New(out io.Writer, actions []string) *Client {
	c := &Client{writer: out, pending: map[string]chan Result{}, allowed: map[string]bool{}, done: make(chan struct{})}
	for _, a := range actions {
		c.allowed[a] = true
	}
	return c
}
func (c *Client) Ready() bool     { return c.ready.Load() }
func (c *Client) Active() bool    { return c.active.Load() }
func (c *Client) ModernIPC() bool { return c.modernIPC.Load() }

func (c *Client) SetActive(v bool) { c.active.Store(v) }
func (c *Client) Write(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return errors.New("萌卡连接已关闭")
	default:
	}
	return json.NewEncoder(c.writer).Encode(v)
}
func (c *Client) Hello(id, version string) error {
	return c.Write(map[string]any{"type": "hello", "api_version": "1", "framework_api": "3.0.0", "plugin_id": id, "version": version})
}
func (c *Client) Close() {
	c.closeOnce.Do(func() { close(c.done); c.Disconnect() })
}
func (c *Client) Disconnect() {
	c.ready.Store(false)
	c.active.Store(false)
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- Result{Err: errors.New("萌卡连接已断开，操作结果请到 QQ 核对")}
		delete(c.pending, id)
	}
}
func (c *Client) Call(ctx context.Context, action string, params map[string]any) (json.RawMessage, error) {
	if !c.allowed[action] {
		return nil, fmt.Errorf("安装清单未声明接口 %s", action)
	}
	if !c.Ready() {
		return nil, errors.New("萌卡尚未就绪")
	}
	id := fmt.Sprintf("catherina_%d", c.seq.Add(1))
	ch := make(chan Result, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, errors.New("萌卡连接已关闭")
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.Write(map[string]any{"type": "action", "id": id, "action": action, "params": params}); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r.Data, r.Err
	case <-ctx.Done():
		return nil, fmt.Errorf("%s 等待超时或已取消；请核对结果，插件不会自动重发", action)
	case <-c.done:
		return nil, errors.New("萌卡连接已关闭")
	}
}

func (c *Client) Notify(action string, params map[string]any) error {
	if !c.Ready() || !c.allowed[action] {
		return errors.New("接口未就绪或未声明")
	}
	return c.Write(map[string]any{"type": "action", "id": fmt.Sprintf("catherina_notify_%d", c.seq.Add(1)), "action": action, "params": params})
}
func (c *Client) Run(ctx context.Context, in io.Reader) error {
	defer c.Close()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 65536), 8*1024*1024)
	revision := int64(-1)
	started := time.Now()
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var f Frame
		if err := json.Unmarshal(scanner.Bytes(), &f); err != nil {
			return fmt.Errorf("萌卡 IPC 消息无效: %w", err)
		}
		switch f.Type {
		case "ready":
			if c.Ready() || (f.Transport != "" && f.Transport != "native-ipc-v1") || (f.APIVersion != "" && f.APIVersion != "1") {
				return errors.New("萌卡 ready 契约不完整或版本不匹配")
			}
			legacyReady := f.FrameworkAPI != "3.0.0" && f.Transport == "native-ipc-v1" && f.APIVersion == "1" && f.ConfigRevision != nil && *f.ConfigRevision >= 0 && f.Config != nil
			if legacyReady {
				revision = *f.ConfigRevision
			} else {
				// Codex 修改：3.0 IPC 在 ready 后直接运行，不依赖 2.5.4 的配置与 activate 帧。
				c.modernIPC.Store(true)
			}
			c.ready.Store(true)
			if legacyReady && c.OnConfig != nil {
				c.OnConfig(f.Config, revision)
			}
			if !legacyReady {
				c.active.Store(true)
				if c.OnLifecycle != nil {
					c.OnLifecycle("activate")
				}
			}
		case "config":
			if !c.Ready() {
				return errors.New("收到配置时 IPC 尚未就绪")
			}
			if f.Revision > revision {
				var config map[string]any
				if err := json.Unmarshal(f.Data, &config); err != nil || config == nil {
					return errors.New("萌卡配置消息无效")
				}
				revision = f.Revision
				if c.OnConfig != nil {
					c.OnConfig(config, revision)
				}
			}
		case "action_result":
			c.mu.Lock()
			ch := c.pending[f.ID]
			delete(c.pending, f.ID)
			c.mu.Unlock()
			if ch != nil {
				r := Result{Data: f.Data}
				if !f.OK {
					r.Err = errors.New(f.Error)
					if f.Error == "" {
						r.Err = errors.New("萌卡接口返回失败")
					}
				}
				ch <- r
			}
		case "event":
			if c.Ready() && c.Active() && c.OnEvent != nil {
				c.OnEvent(f.Data)
			}
		case "lifecycle":
			if !c.Ready() {
				return errors.New("收到生命周期消息时 IPC 尚未就绪")
			}
			if f.Event == "activate" || f.Event == "started" {
				c.active.Store(true)
			}
			if f.Event == "stop" || f.Event == "stopping" || f.Event == "stopped" {
				c.active.Store(false)
			}
			if c.OnLifecycle != nil {
				c.OnLifecycle(f.Event)
			}
			if f.Event == "stop" && !c.ModernIPC() {
				return nil
			}
		case "action_stream":
		default:
			return fmt.Errorf("不支持的萌卡 IPC 消息类型 %q（连接已建立 %s）", f.Type, time.Since(started).Round(time.Second))
		}
	}
	return scanner.Err()
}
