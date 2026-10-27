// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/mengka"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var assets embed.FS

func StartAdmin(e *Engine, settings *ServiceSettings, forward *mengka.Forward, updater *Updater, address string, managed *Managed) (*http.Server, error) {
	host, portText, err := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	if host != "127.0.0.1" || err != nil || port < 1 || port > 65535 {
		return nil, errors.New("管理页面只能监听 127.0.0.1，端口应为 1–65535")
	}
	auth := newAuth(settings)
	auth.managed = managed
	static, _ := fs.Sub(assets, "web")
	files := http.FileServer(http.FS(static))
	mux := http.NewServeMux()
	var framework *FrameworkUpdater
	if managed != nil {
		framework = newFrameworkUpdater(e, updater, settings)
		updater.framework = framework
	}
	mux.HandleFunc("GET /api/framework/session", func(w http.ResponseWriter, r *http.Request) {
		if framework == nil {
			respond(w, 404, M{"error": "当前不是托管模式"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		ok, err := framework.Session(ctx)
		if err != nil {
			respond(w, 502, M{"error": err.Error()})
			return
		}
		respond(w, 200, M{"authenticated": ok, "origin": framework.View().Origin})
	})
	mux.HandleFunc("GET /api/framework/captcha", func(w http.ResponseWriter, r *http.Request) {
		if framework == nil {
			respond(w, 404, M{"error": "当前不是托管模式"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		data, err := framework.Captcha(ctx)
		if err != nil {
			respond(w, 502, M{"error": err.Error()})
			return
		}
		respond(w, 200, M{"captcha": data, "origin": framework.View().Origin})
	})
	mux.HandleFunc("POST /api/framework/login", func(w http.ResponseWriter, r *http.Request) {
		if framework == nil {
			respond(w, 404, M{"error": "当前不是托管模式"})
			return
		}
		var body struct {
			Username    string `json:"username"`
			Password    string `json:"password"`
			CaptchaID   string `json:"captcha_id"`
			CaptchaCode string `json:"captcha_code"`
		}
		if err := decode(w, r, &body); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		err := framework.Login(ctx, body.Username, body.Password, body.CaptchaID, body.CaptchaCode)
		body.Password = ""
		if err != nil {
			respond(w, 400, M{"error": err.Error()})
			return
		}
		respond(w, 200, M{"ok": true})
	})
	mux.HandleFunc("POST /api/framework/update/approve", func(w http.ResponseWriter, r *http.Request) {
		if framework == nil {
			respond(w, 404, M{"error": "当前不是托管模式"})
			return
		}
		var body struct {
			Approve bool `json:"approve"`
		}
		if err := decode(w, r, &body); err != nil {
			return
		}
		if !body.Approve {
			respond(w, 400, M{"error": "需要明确确认权限变更"})
			return
		}
		if err := framework.Start(true); err != nil {
			respond(w, 409, M{"error": err.Error()})
			return
		}
		respond(w, 202, M{"ok": true})
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		accounts := e.Accounts()
		for i := range accounts {
			accounts[i].Enabled = e.AccountEnabled(accounts[i].Key())
		}
		respond(w, http.StatusOK, M{"version": Version, "name": "Catherina", "ready": e.Client.Ready(), "active": e.Client.Active(), "managed": managed != nil, "accounts": accounts, "features": Features, "commands": visibleCommands(), "actions": Actions, "database": e.Store.Path, "dropped": e.Dropped.Load()})
	})
	mux.HandleFunc("GET /api/global", func(w http.ResponseWriter, r *http.Request) {
		v, err := e.Store.Global()
		if err != nil {
			respond(w, 500, M{"error": err.Error()})
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("PATCH /api/global", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Revision int64                      `json:"revision"`
			Patch    map[string]json.RawMessage `json:"patch"`
		}
		if err := decode(w, r, &body); err != nil {
			return
		}
		if body.Revision < 0 || body.Patch == nil {
			respond(w, 400, M{"error": "缺少配置版本或修改内容"})
			return
		}
		state, err := e.Store.PatchGlobal(body.Revision, body.Patch)
		if err != nil {
			status := 400
			if errors.Is(err, ErrConflict) {
				status = 409
			}
			respond(w, status, M{"error": err.Error()})
			return
		}
		respond(w, 200, state)
	})
	mux.HandleFunc("POST /api/global/init", func(w http.ResponseWriter, r *http.Request) {
		state, err := e.Store.Global()
		if err != nil {
			respond(w, 500, M{"error": err.Error()})
			return
		}
		if strings.TrimSpace(state.Config.MenuContent) == "" {
			raw, _ := json.Marshal(buildGlobalMenu())
			state, err = e.Store.PatchGlobal(state.Revision, map[string]json.RawMessage{"menu_content": raw})
			if err != nil {
				respond(w, 500, M{"error": err.Error()})
				return
			}
		}
		respond(w, 200, state)
	})
	account := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		key := r.Header.Get("X-Catherina-Account")
		if !accountPattern.MatchString(key) || !e.Known(key) {
			respond(w, http.StatusBadRequest, M{"error": "请先选择框架中的 QQ 账号"})
			return "", false
		}
		return key, true
	}
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		key, ok := account(w, r)
		if !ok {
			return
		}
		v, err := e.Store.Get(key)
		if err != nil {
			respond(w, 500, M{"error": err.Error()})
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET /api/contacts", func(w http.ResponseWriter, r *http.Request) {
		key, ok := account(w, r)
		if !ok {
			return
		}
		kind := r.URL.Query().Get("kind")
		action, field := "get_group_list", "groups"
		if kind == "friends" {
			action, field = "get_friend_list", "friends"
		} else if kind != "groups" {
			respond(w, 400, M{"error": "联系人类型应为 groups 或 friends"})
			return
		}
		current, _ := e.Account(key)
		raw, err := e.call(current, action, M{})
		if err != nil {
			respond(w, 502, M{"error": "读取当前 QQ 联系人失败：" + err.Error()})
			return
		}
		items, err := contactItems(raw, field)
		if err != nil {
			respond(w, 502, M{"error": "萌卡返回的联系人数据无法识别：" + err.Error()})
			return
		}
		respond(w, 200, M{"items": items, "kind": kind, "count": len(items)})
	})
	mux.HandleFunc("GET /api/menus/defaults", func(w http.ResponseWriter, r *http.Request) {
		c := defaults()
		m := Event{}
		if r.Header.Get("X-Catherina-Account") != "" {
			key, ok := account(w, r)
			if !ok {
				return
			}
			state, err := e.Store.Get(key)
			if err != nil {
				respond(w, 500, M{"error": err.Error()})
				return
			}
			c = state.Config
			current, _ := e.Account(key)
			m.SelfID, m.ClientType, m.ProtocolID, m.AccountKey = current.SelfID, current.ClientType, current.ProtocolID, current.Key()
		}
		global, err := e.Store.Global()
		if err != nil {
			respond(w, 500, M{"error": err.Error()})
			return
		}
		if c.Owner == 0 {
			c.Owner = global.Config.Owner
		}
		if c.Owner == 0 {
			c.Owner = 1
		}
		m.Sender.UserID = m.SelfID
		if m.SelfID == 0 {
			m.Sender.UserID = c.Owner
		}
		featureDefaults := M{}
		featurePreviews := M{}
		c.MenuPrefix, c.MenuSuffix = global.Config.MenuPrefix, global.Config.MenuSuffix
		for id, reply := range global.Config.Replies {
			if reply != "" {
				c.Replies[id] = reply
			} else {
				delete(c.Replies, id)
			}
		}
		for _, feature := range Features {
			featureDefaults[feature.ID] = featureHelp(defaults(), Event{SelfID: 1, UserID: 1}, feature.ID)
			featurePreviews[feature.ID] = expand(c.MenuPrefix+featureHelp(c, m, feature.ID)+c.MenuSuffix, m)
		}
		categories := []M{}
		for _, group := range commandMenuGroups {
			reply, _ := commandCategoryHelp(c, m, group.Trigger)
			categories = append(categories, M{"id": group.Trigger, "trigger": group.Trigger, "title": strings.TrimSuffix(group.Trigger, "菜单"), "features": group.Features, "reply": expand(c.MenuPrefix+reply+c.MenuSuffix, m)})
		}
		globalReply := expand(global.Config.MenuPrefix+global.Config.MenuContent+global.Config.MenuSuffix, m)
		respond(w, 200, M{"global": buildGlobalMenu(), "account": buildGlobalMenu(), "features": featureDefaults, "categories": categories, "preview": M{"global": globalReply, "account": globalReply, "features": featurePreviews}})
	})
	mux.HandleFunc("PATCH /api/config", func(w http.ResponseWriter, r *http.Request) {
		key, ok := account(w, r)
		if !ok {
			return
		}
		var body struct {
			Revision int64                      `json:"revision"`
			Patch    map[string]json.RawMessage `json:"patch"`
		}
		if err := decode(w, r, &body); err != nil {
			return
		}
		if body.Revision < 0 || body.Patch == nil {
			respond(w, 400, M{"error": "缺少配置版本或修改内容"})
			return
		}
		state, err := e.Store.Patch(key, body.Revision, body.Patch)
		if err != nil {
			status := 400
			if errors.Is(err, ErrConflict) {
				status = 409
			}
			respond(w, status, M{"error": err.Error()})
			return
		}
		respond(w, 200, state)
	})
	mux.HandleFunc("GET /api/monitor", func(w http.ResponseWriter, r *http.Request) {
		key, ok := account(w, r)
		if !ok {
			return
		}
		limit := 200
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 500 {
				respond(w, 400, M{"error": "记录条数应为 1–500"})
				return
			}
			limit = n
		}
		items, err := e.Store.Monitor(key, limit)
		if err != nil {
			respond(w, 500, M{"error": err.Error()})
			return
		}
		respond(w, 200, M{"items": items})
	})
	mux.HandleFunc("POST /api/monitor/clear", func(w http.ResponseWriter, r *http.Request) {
		key, ok := account(w, r)
		if !ok {
			return
		}
		if err := e.Store.ClearMonitor(key); err != nil {
			respond(w, 500, M{"error": err.Error()})
			return
		}
		respond(w, 200, M{"ok": true})
	})
	mux.HandleFunc("POST /api/refresh", func(w http.ResponseWriter, r *http.Request) {
		if !e.background("refresh", e.refresh) {
			respond(w, 409, M{"error": "账号列表正在刷新，稍后重试"})
			return
		}
		respond(w, 202, M{"ok": true})
	})
	mux.HandleFunc("GET /api/service", func(w http.ResponseWriter, r *http.Request) {
		if managed != nil {
			connection := managed.Connection()
			config := settings.Get()
			respond(w, 200, M{"managed": true, "transport": managed.Transport(), "url": connection.URL, "token_set": connection.Token != "", "framework_admin_url": config.FrameworkAdminURL, "auto_update": config.AutoUpdate, "framework_update": framework.View(), "repository": "https://github.com/Jay1023CN/Mengka-NT-plugins", "feed_base": FeedBase(), "feed_sources": FeedSources(), "update": updater.State(), "policy": updater.Policy.View()})
			return
		}
		v := settings.Get()
		respond(w, 200, M{"transport": "websocket-v1", "url": v.URL, "token_set": v.Token != "", "auto_update": v.AutoUpdate, "repository": "https://github.com/Jay1023CN/Mengka-NT-plugins", "feed_base": FeedBase(), "feed_sources": FeedSources(), "update": updater.State(), "policy": updater.Policy.View()})
	})
	mux.HandleFunc("PATCH /api/service", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URL               *string `json:"url"`
			Token             *string `json:"token"`
			FrameworkAdminURL *string `json:"framework_admin_url"`
			AutoUpdate        *bool   `json:"auto_update"`
		}
		if err := decode(w, r, &body); err != nil {
			return
		}
		if managed != nil && (body.URL != nil || body.Token != nil) {
			respond(w, 400, M{"error": "托管连接由框架管理，不能在这里更改"})
			return
		}
		if managed == nil && body.FrameworkAdminURL != nil {
			respond(w, 400, M{"error": "只有萌卡托管插件需要设置框架管理接口地址"})
			return
		}

		err := settings.Update(func(v *ServiceConfig) error {
			if body.URL != nil {
				if err := validateConnection(*body.URL); err != nil {
					return err
				}
				v.URL = *body.URL
			}
			if body.Token != nil && *body.Token != "" {
				if len(*body.Token) > 4096 {
					return errors.New("服务令牌过长")
				}
				v.Token = *body.Token
			}
			if body.FrameworkAdminURL != nil {
				if err := validateFrameworkAdminURL(strings.TrimSpace(*body.FrameworkAdminURL)); err != nil {
					return err
				}
				v.FrameworkAdminURL = strings.TrimRight(strings.TrimSpace(*body.FrameworkAdminURL), "/")
			}
			if body.AutoUpdate != nil {
				v.AutoUpdate = *body.AutoUpdate
			}
			return nil
		})
		if err != nil {
			respond(w, 400, M{"error": err.Error()})
			return
		}
		if body.URL != nil || body.Token != nil {
			forward.Reconnect()
		}
		respond(w, 200, M{"ok": true})
	})
	mux.HandleFunc("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) {
		if !updater.CheckOrJoin() {
			respond(w, 409, M{"error": "更新任务正在进行"})
			return
		}
		respond(w, 202, M{"ok": true})
	})
	mux.HandleFunc("GET /api/update/package", func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(8 * time.Minute))
		ctx, cancel := context.WithTimeout(r.Context(), 7*time.Minute)
		defer cancel()
		name, data, err := updater.DownloadLatest(ctx)
		if err != nil {
			respond(w, 502, M{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("POST /api/update/install", func(w http.ResponseWriter, r *http.Request) {
		if framework != nil {
			if err := framework.Start(false); err != nil {
				respond(w, 409, M{"error": err.Error()})
				return
			}
			respond(w, 202, M{"ok": true})
			return
		}
		if !updater.CheckAsync(true) {
			respond(w, 409, M{"error": "更新任务正在进行"})
			return
		}
		respond(w, 202, M{"ok": true})
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("catherina_session")
		if err == nil {
			auth.mu.Lock()
			delete(auth.sessions, cookie.Value)
			auth.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "catherina_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		respond(w, 200, M{"ok": true})
	})
	mux.HandleFunc("POST /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		key, ok := account(w, r)
		if !ok {
			return
		}
		var body struct {
			Revision int64 `json:"revision"`
			Job      Job   `json:"job"`
		}
		if err := decode(w, r, &body); err != nil {
			return
		}
		if body.Revision < 0 {
			respond(w, 400, M{"error": "配置版本无效"})
			return
		}
		j := body.Job
		if j.ID == "" {
			j.ID = newID()
		}
		if j.Kind == "loop" {
			j.Next = time.Now().Add(time.Duration(j.Interval) * time.Second).Unix()
		} else {
			if !timePattern.MatchString(j.Time) {
				respond(w, 400, M{"error": "请选择每天执行的时间，例如 20:00"})
				return
			}
			j.Next = nextDaily(j.Time, time.Now()).Unix()
		}
		state, err := e.Store.Update(key, body.Revision, func(c *Config) error {
			if err := validateJobRecipients(*c, j); err != nil {
				return err
			}
			if j.Enabled {
				c.Enabled[j.Kind] = true
			}
			for i := range c.Jobs {
				if c.Jobs[i].ID == j.ID {
					old := c.Jobs[i]
					j.LastRun, j.LastResult = old.LastRun, old.LastResult
					if old.Enabled && old.Kind == j.Kind && old.Time == j.Time && old.Interval == j.Interval {
						j.Next = old.Next
					}
					c.Jobs[i] = j
					return nil
				}
			}
			c.Jobs = append(c.Jobs, j)
			return nil
		})
		if err != nil {
			status := 400
			if errors.Is(err, ErrConflict) {
				status = 409
			}
			respond(w, status, M{"error": err.Error()})
			return
		}
		respond(w, 200, state)
	})
	mux.HandleFunc("POST /api/debug", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mode    string            `json:"mode"`
			Action  string            `json:"action"`
			Params  M                 `json:"params"`
			Headers map[string]string `json:"headers"`
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Body    string            `json:"body"`
		}
		if err := decode(w, r, &body); err != nil {
			return
		}
		if body.Mode == "mengka" {
			key, ok := account(w, r)
			if !ok {
				return
			}
			if !e.allowed() || !e.Client.Active() {
				respond(w, 409, M{"error": "插件未激活或当前版本不可运行，请先检查连接与更新"})
				return
			}
			if body.Action == "" || len(body.Action) > 100 || strings.IndexFunc(body.Action, func(c rune) bool { return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') }) >= 0 {
				respond(w, 400, M{"error": "请输入有效的接口名称"})
				return
			}
			current, _ := e.Account(key)
			if body.Params == nil {
				body.Params = M{}
			}
			body.Params["self_id"], body.Params["client_type"] = current.SelfID, current.ClientType
			started := time.Now()
			// Codex 修改：接口调试也走账号适配层，按框架版本注入正确的账号标识。
			result, err := e.call(current, body.Action, body.Params)
			if err != nil {
				respond(w, 502, M{"error": err.Error()})
				return
			}
			respond(w, 200, M{"result": result, "milliseconds": time.Since(started).Milliseconds(), "account": key, "action": body.Action})
			return
		}
		if body.Mode != "" && body.Mode != "http" {
			respond(w, 400, M{"error": "不支持的调试方式"})
			return
		}
		switch body.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			respond(w, 400, M{"error": "不支持的 HTTP 请求方法"})
			return
		}
		if !strings.HasPrefix(body.URL, "http://") && !strings.HasPrefix(body.URL, "https://") {
			respond(w, 400, M{"error": "请输入 HTTP(S) URL"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, body.Method, body.URL, strings.NewReader(body.Body))
		if err != nil {
			respond(w, 400, M{"error": err.Error()})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		for name, value := range body.Headers {
			req.Header.Set(name, value)
		}
		started := time.Now()
		res, err := httpClient.Do(req)
		if err != nil {
			respond(w, 502, M{"error": err.Error()})
			return
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 256*1024+1))
		if err != nil {
			respond(w, 502, M{"error": err.Error()})
			return
		}
		truncated := len(data) > 256*1024
		if truncated {
			data = data[:256*1024]
		}
		respond(w, 200, M{"status": res.StatusCode, "headers": res.Header, "body": string(data), "milliseconds": time.Since(started).Milliseconds(), "truncated": truncated})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		files.ServeHTTP(w, r)
	})
	protected := auth.middleware(mux)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if managed == nil && r.Host != address && r.Host != "localhost:"+portText {
			respond(w, 403, M{"error": "管理入口不匹配"})
			return
		}
		protected.ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}

	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 8 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768}
	go func() { _ = server.Serve(listener) }()
	return server, nil
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		respond(w, 400, M{"error": "请求格式不正确：" + err.Error()})
		return err
	}
	var rest any
	if err := decoder.Decode(&rest); err != io.EOF {
		respond(w, 400, M{"error": "请求只能包含一个 JSON 对象"})
		return errors.New("extra JSON")
	}
	return nil
}

func contactItems(raw json.RawMessage, field string) ([]M, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	for depth := 0; depth < 4; depth++ {
		switch v := value.(type) {
		case []any:
			items := make([]M, 0, len(v))
			seen := map[int64]bool{}
			for _, entry := range v {
				row, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				id := number(row["group_id"])
				name := firstText(row, "group_name", "group_memo", "name")
				if field == "friends" {
					id = number(row["user_id"])
					if id <= 0 {
						id = number(row["uin"])
					}
					name = firstText(row, "remark", "nickname", "name")
				}
				if id <= 0 || seen[id] {
					continue
				}
				seen[id] = true
				items = append(items, M{"id": strconv.FormatInt(id, 10), "name": name, "nickname": firstText(row, "nickname"), "remark": firstText(row, "remark")})
			}
			if len(v) > 0 && len(items) == 0 {
				return nil, errors.New("列表中没有有效的联系人号码，请更新萌卡后重试")
			}
			return items, nil
		case map[string]any:
			if next, ok := v[field]; ok {
				value = next
				continue
			}
			if next, ok := v["data"]; ok {
				value = next
				continue
			}
			return nil, errors.New("缺少联系人数组")
		default:
			return nil, errors.New("联系人数据不是数组")
		}
	}
	return nil, errors.New("联系人数据嵌套层级过深")
}

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
