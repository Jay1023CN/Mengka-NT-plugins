// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/mengka"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Codex 修改：安装权限同时包含 2.5.4 与 3.0 的 API 名称；WebSocket 认证仍使用旧权限集。
var Actions = []string{"get_plugin_context", "get_bot_list", "get_bot_info", "send_group_msg", "send_private_msg", "send_friend_msg", "send_group_temp_msg", "get_group_list", "get_friend_list", "get_group_member_list", "get_summary_card", "get_user_profile", "like_summary_card", "like_user_profile", "group_sign", "sign_group", "set_friend_remark", "set_group_special_title", "set_group_mute_all", "set_group_mute", "kick_group_member", "send_group_notice", "set_qq_profile", "profile_operation", "get_level_task_panel", "get_level_tasks", "execute_level_tasks", "publish_qzone_feed", "get_qzone_friend_feeds", "like_qzone_feed", "comment_qzone_feed", "get_music_ark", "get_red_packet_info", "grab_red_packet", "get_flash_image", "set_msg_emoji_like", "set_friend_add_request", "get_friend_requests", "set_friend_request", "approve_group_apply", "approve_group_invite"}
var EventPermissions = []string{"group_message", "friend_message", "request", "group_event", "friend_event", "system_event", "bot_offline"}
var NativeEventPermissions = []string{"group_message", "friend_message", "request", "group_event", "friend_event", "system_event", "bot_offline", "bot_online"}

type Engine struct {
	Policy      *PolicyManager
	Client      *mengka.Client
	Store       *Store
	DataDir     string
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	lanes       []chan Event
	work        chan func()
	started     atomic.Bool
	protocolV3  atomic.Bool
	protocolSet atomic.Bool
	accountsMu  sync.RWMutex
	accounts    map[string]Account
	mu          sync.Mutex
	cooldowns   map[string]time.Time
	polling     map[string]time.Time
	busy        map[string]bool
	pendingMass map[string]PendingMass
	feedBase    map[string]int64
	outgoing    map[string]time.Time
	Dropped     atomic.Uint64
}

type PendingMass struct {
	Target  string
	Text    string
	IDs     []string
	Author  int64
	Created time.Time
}

func NewEngine(c *mengka.Client, s *Store, dir string) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{Client: c, Store: s, DataDir: dir, ctx: ctx, cancel: cancel, accounts: map[string]Account{}, cooldowns: map[string]time.Time{}, polling: map[string]time.Time{}, busy: map[string]bool{}, pendingMass: map[string]PendingMass{}, feedBase: map[string]int64{}, work: make(chan func(), 256)}
	for i := 0; i < 8; i++ {
		e.lanes = append(e.lanes, make(chan Event, 128))
	}
	return e
}
func (e *Engine) Start() {
	if !e.started.CompareAndSwap(false, true) {
		return
	}
	for _, lane := range e.lanes {
		e.wg.Add(1)
		go func(ch chan Event) {
			defer e.wg.Done()
			for {
				select {
				case <-e.ctx.Done():
					return
				case event := <-ch:
					e.safe(func() { e.handle(event) })
				}
			}
		}(lane)
	}
	for i := 0; i < 4; i++ {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			for {
				select {
				case <-e.ctx.Done():
					return
				case fn := <-e.work:
					e.safe(fn)
				}
			}
		}()
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.refresh()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		n := 0
		for {
			select {
			case <-e.ctx.Done():
				return
			case <-tick.C:
				n++
				e.schedule()
				if n%6 == 0 {
					e.background("refresh", e.refresh)
				}
				if n%720 == 0 {
					e.background("prune", func() {
						if err := e.Store.Prune(); err != nil {
							log.Printf("清理过期记录失败：%v", err)
						}
					})
				}
			}
		}
	}()
}
func (e *Engine) Stop() { e.cancel(); e.Client.Close(); e.wg.Wait() }
func (e *Engine) safe(fn func()) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("任务异常：%v", p)
		}
	}()
	fn()
}
func (e *Engine) background(key string, fn func()) bool {
	e.mu.Lock()
	if e.busy[key] {
		e.mu.Unlock()
		return false
	}
	e.busy[key] = true
	e.mu.Unlock()
	wrapped := func() { defer func() { e.mu.Lock(); delete(e.busy, key); e.mu.Unlock() }(); fn() }
	select {
	case e.work <- wrapped:
		return true
	default:
		e.mu.Lock()
		delete(e.busy, key)
		e.mu.Unlock()
		e.Dropped.Add(1)
		log.Printf("后台队列已满，任务 %s 未入队", key)
		return false
	}
}
func (e *Engine) Submit(raw json.RawMessage) {
	if !e.allowed() {
		return
	}
	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		log.Printf("事件格式无效：%v", err)
		return
	}
	normalizeEvent(&event)
	if event.SelfID > 0 {
		matched := false
		e.accountsMu.RLock()
		for _, account := range e.accounts {
			if account.SelfID == event.SelfID && account.ProtocolID == event.ProtocolID && (event.ClientType == "" || account.ClientType == normalizeClientType(event.ClientType)) {
				event.ClientType = account.ClientType
				event.AccountKey = account.Key()
				matched = true
				break
			}
		}
		e.accountsMu.RUnlock()
		if e.protocolV3.Load() && !matched {
			if e.started.Load() {
				e.background("refresh", e.refresh)
			}
			return
		}
	}
	if event.SelfID <= 0 || (event.ClientType != "android" && event.ClientType != "linuxqq") {
		if e.started.Load() {
			e.background("refresh", e.refresh)
		}
		return
	}
	if !e.started.Load() {
		return
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(event.Account().Key()))
	lane := e.lanes[int(h.Sum32())%len(e.lanes)]
	select {
	case lane <- event:
	default:
		e.Dropped.Add(1)
		log.Printf("账号 %s 的消息队列已满，本次事件未处理", event.Account().Key())
	}
}

// Codex 修改：把 3.0 post_type/sub_type 事件归一到现有业务事件，并读出 msg 序列号。
func normalizeEvent(event *Event) {
	if event.Body == nil {
		event.Body = M{}
	}
	if event.PostType == "" || event.EventType != "" {
		return
	}
	if event.Msg != nil {
		if event.MessageSeq == 0 {
			event.MessageSeq = number(event.Msg["seq"])
		}
		if event.MsgSeq == 0 {
			event.MsgSeq = number(event.Msg["seq"])
		}
	}
	switch event.PostType {
	case "group_message":
		event.EventType = "group_message_received"
	case "friend_message", "group_temp_message":
		event.EventType = "private_message_received"
		if event.PostType == "group_temp_message" {
			event.GroupID = 0
		}
	case "bot_online":
		event.EventType = "account_online"
	case "bot_offline":
		event.EventType = "account_offline"
	case "friend_notice":
		switch event.SubType {
		case "recall":
			event.EventType = "friend_message_recalled"
		case "apply":
			event.EventType = "friend_request_received"
		}
	case "group_notice":
		switch event.SubType {
		case "recall":
			event.EventType = "group_message_recalled"
		case "apply":
			event.EventType = "group_request_received"
		case "poke":
			event.EventType = "user_poked"
		}
	}
	if event.EventType == "group_message_recalled" || event.EventType == "friend_message_recalled" {
		if event.MsgSeq == 0 {
			event.MsgSeq = number(event.Body["msg_seq"])
		}
		if event.MessageSeq == 0 {
			event.MessageSeq = number(event.Body["message_seq"])
		}
	}
	if event.EventType == "group_request_received" {
		if number(event.Body["msg_seq"]) <= 0 && number(event.Msg["seq"]) > 0 {
			event.Body["msg_seq"] = event.Msg["seq"]
		}
		if _, ok := event.Body["msg_seq"]; !ok {
			if seq := number(event.Body["msgseq"]); seq > 0 {
				event.Body["msg_seq"] = seq
			}
		}
	}
}
func (e *Engine) Accounts() []Account {
	e.accountsMu.RLock()
	defer e.accountsMu.RUnlock()
	out := make([]Account, 0, len(e.accounts))
	for _, a := range e.accounts {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}
func (e *Engine) AccountEnabled(key string) bool {
	if e.Store == nil {
		return true
	}
	global, err := e.Store.Global()
	if err != nil {
		log.Printf("读取账号接入设置失败：%v", err)
		return true
	}
	for _, disabled := range global.Config.DisabledAccounts {
		if disabled == key {
			return false
		}
	}
	return true
}
func (e *Engine) Known(key string) bool {
	e.accountsMu.RLock()
	defer e.accountsMu.RUnlock()
	_, ok := e.accounts[key]
	return ok
}

// Codex 修改：管理接口按稳定账号键读取完整路由信息，支持同 QQ 多协议实例。
func (e *Engine) Account(key string) (Account, bool) {
	e.accountsMu.RLock()
	defer e.accountsMu.RUnlock()
	a, ok := e.accounts[key]
	return a, ok
}
func (e *Engine) refresh() {
	ctx, cancel := context.WithTimeout(e.ctx, 20*time.Second)
	defer cancel()
	// Codex 修改：通过 IPC ready 类型或上下文账号选择器识别 3.0，旧版继续使用 client_type。
	if e.Client.ModernIPC() {
		e.protocolV3.Store(true)
		e.protocolSet.Store(true)
	} else if !e.protocolSet.Load() {
		if rawContext, contextErr := e.Client.Call(ctx, "get_plugin_context", M{}); contextErr == nil {
			var framework struct {
				AccountSelector string `json:"account_selector"`
			}
			if json.Unmarshal(rawContext, &framework) == nil {
				e.protocolV3.Store(framework.AccountSelector == "self_id+protocol_id")
				e.protocolSet.Store(true)
			}
		}
	}
	raw, err := e.Client.Call(ctx, "get_bot_list", M{})
	if err != nil {
		log.Printf("读取账号列表失败：%v", err)
		return
	}
	var bots []Account
	if err = json.Unmarshal(raw, &bots); err != nil {
		log.Printf("账号列表不符合 SDK 数组契约：%v", err)
		return
	}
	assigned, err := e.Store.AssignAccountKeys(bots)
	if err != nil {
		log.Printf("分配协议账号键失败：%v", err)
		return
	}
	if len(assigned) != len(bots) {
		log.Printf("账号列表中有 %d 个协议实例无法唯一识别，已跳过", len(bots)-len(assigned))
	}
	out := make(map[string]Account, len(assigned))
	for _, a := range assigned {
		out[a.Key()] = a
	}
	e.accountsMu.Lock()
	previous := e.accounts
	e.accounts = out
	e.accountsMu.Unlock()
	for key, a := range out {
		if a.Status == 1 && previous[key].Status != 1 {
			state, err := e.Store.Get(key)
			if err == nil && state.Config.Notify && state.Config.On("online_notify", 0) {
				e.notifyOnline(Event{SelfID: a.SelfID, ClientType: a.ClientType, ProtocolID: a.ProtocolID, AccountKey: a.Key()}, state.Config)
			}
		}
	}
}
func (e *Engine) call(a Account, action string, p M) (json.RawMessage, error) {
	if !e.AccountEnabled(a.Key()) {
		return nil, errors.New("这个 QQ 已停用 Catherina 接入，请先在全局设置中启用")
	}
	if !e.allowed() {
		return nil, errors.New("版本策略暂停了机器人功能，请打开管理页面升级")
	}
	if !e.Client.Active() {
		return nil, errors.New("插件尚未激活")
	}
	if p == nil {
		p = M{}
	} else {
		// Codex 修改：复制请求参数，避免版本适配改写命令层用于显示的原始参数。
		params := make(M, len(p)+2)
		for key, value := range p {
			params[key] = value
		}
		p = params
	}
	// Codex 修改：框架上下文和账号目录是全局接口，不附加账号路由字段。
	globalAction := action == "get_plugin_context" || action == "get_bot_list" || action == "get_protocol_list"
	if globalAction {
		delete(p, "self_id")
		delete(p, "client_type")
		delete(p, "protocol_id")
	} else {
		p["self_id"] = a.SelfID
	}
	if e.protocolV3.Load() && !globalAction {
		e.accountsMu.RLock()
		if current, ok := e.accounts[a.Key()]; ok && current.SelfID == a.SelfID {
			a.ProtocolID = current.ProtocolID
		}
		e.accountsMu.RUnlock()
		p["protocol_id"] = a.ProtocolID
		delete(p, "client_type")
	} else if !globalAction {
		p["client_type"] = a.ClientType
	}
	requestedAction := action
	if e.protocolV3.Load() {
		action, p = adaptV3Action(action, p)
	}
	ctx, cancel := context.WithTimeout(e.ctx, actionTimeout(action))
	defer cancel()
	raw, err := e.Client.Call(ctx, action, p)
	if err == nil && (requestedAction == "send_private_msg" || requestedAction == "send_group_msg" || requestedAction == "send_group_temp_msg") {
		var result struct {
			MessageID int64 `json:"message_id"`
		}
		if json.Unmarshal(raw, &result) == nil && result.MessageID > 0 {
			e.rememberOutputID(a, number(p["group_id"]), number(p["user_id"]), result.MessageID)
		}
	}
	return raw, err
}

// Codex 修改：把旧版业务动作和参数映射到 3.0 API，同时保持旧框架调用名不变。
func adaptV3Action(action string, params M) (string, M) {
	move := func(from, to string) {
		if value, ok := params[from]; ok {
			params[to] = value
			delete(params, from)
		}
	}
	switch action {
	case "send_private_msg":
		action = "send_friend_msg"
	case "get_summary_card":
		action = "get_user_profile"
		move("target_uin", "user_id")
	case "like_summary_card":
		action = "like_user_profile"
		move("target_uin", "user_id")
	case "group_sign":
		action = "sign_group"
	case "get_level_task_panel":
		action = "get_level_tasks"
		delete(params, "refresh")
	case "set_qq_profile":
		action = "profile_operation"
		params["operation"] = "signature.set"
		params["request"] = M{"long_nick": params["personal_note"]}
		delete(params, "nickname")
		delete(params, "personal_note")
	case "approve_group_invite":
		move("msgseq", "msg_seq")
		delete(params, "flag")
	}
	return action, params
}
func (e *Engine) send(a Account, group, user int64, segments []Segment) error {
	if len(segments) == 0 {
		return nil
	}
	action := "send_private_msg"
	p := M{"message": segments, "user_id": user}
	if group > 0 {
		action = "send_group_msg"
		delete(p, "user_id")
		p["group_id"] = group
	}
	if group <= 0 && user <= 0 {
		return errors.New("接收 QQ 无效")
	}
	raw, err := e.call(a, action, p)
	if err != nil {
		return err
	}
	if group > 0 {
		var r struct {
			Success bool `json:"success"`
		}
		if json.Unmarshal(raw, &r) != nil || !r.Success {
			return errors.New("群消息接口没有确认发送成功")
		}
	} else {
		var r struct {
			MessageID int64 `json:"message_id"`
		}
		if json.Unmarshal(raw, &r) != nil || r.MessageID <= 0 {
			return errors.New("好友消息接口没有返回有效消息 ID")
		}
	}
	return nil
}
func (e *Engine) reply(m Event, s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	segs := []Segment{{Type: "text", Data: M{"text": expand(s, m)}}}
	err := e.send(m.Account(), m.GroupID, m.Peer(), segs)
	if err != nil {
		log.Printf("账号 %s 回复失败：%v", m.Account().Key(), err)
	}
	return err
}
func (e *Engine) owner(m Event, c Config, s string) {
	id := c.Owner
	if id == 0 {
		id = m.SelfID
	}
	err := e.send(m.Account(), 0, id, []Segment{{Type: "text", Data: M{"text": expand(s, m)}}})
	if err != nil {
		log.Printf("账号 %s 通知主人失败：%v", m.Account().Key(), err)
	}
}
func (e *Engine) cool(key string, seconds int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if now.Before(e.cooldowns[key]) {
		return false
	}
	if len(e.cooldowns) > 10000 {
		for k, t := range e.cooldowns {
			if now.After(t) {
				delete(e.cooldowns, k)
			}
		}
	}
	e.cooldowns[key] = now.Add(time.Duration(seconds) * time.Second)
	return true
}
func (e *Engine) handle(m Event) {
	if !e.allowed() {
		return
	}
	if m.IsSelf() && e.isOutput(m) {
		return
	}
	key := m.Account().Key()
	if m.EventType == "account_online" || m.EventType == "account_offline" {
		e.accountsMu.Lock()
		a := e.accounts[key]
		a.SelfID = m.SelfID
		a.ClientType = m.ClientType
		a.ProtocolID = m.ProtocolID
		if m.AccountKey != "" {
			a.AccountKey = m.AccountKey
		}
		if m.EventType == "account_online" {
			a.Status = 1
		} else {
			a.Status = 0
		}
		e.accounts[key] = a
		e.accountsMu.Unlock()
	}
	if !e.AccountEnabled(key) {
		return
	}
	s, err := e.Store.Get(key)
	if err != nil {
		log.Printf("账号 %s 配置读取失败：%v", key, err)
		return
	}
	c := s.Config
	global, globalErr := e.Store.Global()
	if globalErr == nil {
		c.Menus = append(append([]CustomMenu{}, global.Config.Menus...), c.Menus...)
		c.MenuPrefix, c.MenuSuffix = global.Config.MenuPrefix, global.Config.MenuSuffix
		for id, reply := range global.Config.Replies {
			if reply != "" {
				c.Replies[id] = reply
			} else {
				delete(c.Replies, id)
			}
		}
	}
	if globalErr == nil && c.Owner == 0 {
		c.Owner = global.Config.Owner
	}
	dedup := m.EventID
	if m.IsSelf() && m.MessageID > 0 {
		dedup = fmt.Sprintf("self:%s:%d", conversationKey(m.Account(), m.GroupID, m.Peer()), m.MessageID)
	}
	if dedup == "" && m.MessageID > 0 {
		dedup = fmt.Sprintf("%s:%d:%d", m.EventType, m.GroupID, m.MessageID)
	}
	if dedup != "" {
		seen, err := e.Store.Seen(key, dedup)
		if err != nil {
			log.Printf("去重记录失败：%v", err)
			return
		}
		if seen {
			return
		}
	}
	if c.On("monitor", m.GroupID) {
		kind, body := monitorEntry(m)
		if err = e.Store.Log(key, kind, body); err != nil {
			log.Printf("监控写入失败：%v", err)
		}
	}
	switch m.EventType {
	case "account_online":
		if c.On("online_notify", 0) && c.Notify {
			e.notifyOnline(m, c)
		}
	case "group_message_received", "private_message_received":
		if m.Author() <= 0 || (m.IsSelf() && strings.TrimSpace(m.Text()) == "") {
			return
		}
		if m.MessageID > 0 {
			if err = e.Store.Cache(m); err != nil {
				log.Printf("消息缓存写入失败：%v", err)
			}
		}
		if !m.IsSelf() && c.On("xuhuo", m.GroupID) {
			if err = e.Store.Touch(m); err != nil {
				log.Printf("续火记账失败：%v", err)
			}
		}
		if !m.IsSelf() {
			e.passive(m, c)
		}
		if c.Allow(m, "custom_menu") {
			for _, menu := range c.Menus {
				if m.Text() == menu.Trigger && (menu.Global || m.GroupID == 0) && (len(menu.Permit) == 0 || contains(menu.Permit, m.Author()) || c.IsOwner(m)) {
					_ = e.reply(m, c.MenuPrefix+menu.Reply+c.MenuSuffix)
					return
				}
			}
		}
		if e.command(m, c) {
			return
		}
		if c.On("qa", m.GroupID) {
			if answer, ok := answerFor(c, m, m.Text()); ok {
				_ = e.reply(m, answer)
			}
		}
	case "group_message_recalled", "friend_message_recalled":
		actor := number(m.Operator["user_id"])
		if actor == 0 {
			actor = m.Author()
		}
		if !c.On("antirec", m.GroupID) || actor == m.SelfID || (c.Owner > 0 && actor == c.Owner) {
			return
		}
		old, err := e.Store.Recalled(m)
		if err != nil {
			return
		}
		if old.Author() == m.SelfID {
			return
		}
		_ = e.reply(old, fmt.Sprintf("撤回消息 · %d\n%s", old.Author(), old.Text()))
	case "user_poked":
		if c.On("poke", m.GroupID) && number(m.Target["user_id"]) == m.SelfID && e.cool(key+":poke:"+strconv.FormatInt(m.Author(), 10), c.Cooldown) {
			_ = e.reply(m, c.PokeText)
		}
	case "friend_request_received":
		if c.On("auto", 0) && (m.Flag != "" || e.protocolV3.Load()) {
			if e.protocolV3.Load() {
				err = e.approveFriendRequest(m)
			} else {
				_, err = e.call(m.Account(), "set_friend_add_request", M{"flag": m.Flag, "approve": true})
			}
			if err != nil {
				log.Printf("好友申请处理失败：%v", err)
			}
		}
	case "group_request_received":
		if !c.On("auto", m.GroupID) {
			return
		}
		if eventAllows(m, "approve_group_invite") || (number(m.Body["msg_seq"]) > 0 && (m.RequestID <= 0 || m.RequestType <= 0)) {
			var result json.RawMessage
			result, err = e.call(m.Account(), "approve_group_invite", M{"group_id": m.GroupID, "msgseq": m.Body["msg_seq"], "flag": m.Flag})
			if err == nil && e.protocolV3.Load() {
				err = confirmedSuccess(result)
			}
		} else if m.RequestID > 0 && m.RequestType > 0 {
			var result json.RawMessage
			result, err = e.call(m.Account(), "approve_group_apply", M{"group_id": m.GroupID, "request_id": m.RequestID, "request_type": m.RequestType, "request_extra": m.RequestExtra})
			if err == nil && e.protocolV3.Load() {
				err = confirmedSuccess(result)
			}
		} else if number(m.Body["msg_seq"]) > 0 || number(m.Body["msgseq"]) > 0 {
			seq := m.Body["msg_seq"]
			if number(seq) <= 0 {
				seq = m.Body["msgseq"]
			}
			var result json.RawMessage
			result, err = e.call(m.Account(), "approve_group_invite", M{"group_id": m.GroupID, "msgseq": seq, "flag": m.Flag})
			if err == nil && e.protocolV3.Load() {
				err = confirmedSuccess(result)
			}
		}
		if err != nil {
			log.Printf("群申请处理失败：%v", err)
		}
	}
}

func eventAllows(event Event, action string) bool {
	for _, allowed := range event.Actions {
		if allowed == action {
			return true
		}
	}
	return false
}

// Codex 修改：同时检查 3.0 Action data 中的业务 success，避免把未通过的审批记成成功。
func confirmedSuccess(raw json.RawMessage) error {
	var response struct {
		Success *bool `json:"success"`
	}
	if json.Unmarshal(raw, &response) == nil && response.Success != nil && !*response.Success {
		return errors.New("萌卡接口未确认操作成功")
	}
	return nil
}

// Codex 修改：3.0 要求使用当前好友申请列表中的 request_key，且必须唯一匹配申请人。
func (e *Engine) approveFriendRequest(event Event) error {
	account := event.Account()
	if account.SelfID <= 0 || event.Author() <= 0 {
		return errors.New("好友申请缺少账号或申请人信息，未自动处理")
	}
	raw, err := e.call(account, "get_friend_requests", M{"count": 100})
	if err != nil {
		return err
	}
	var requests []struct {
		RequestKey string `json:"request_key"`
		UserID     int64  `json:"user_id"`
		UserUID    string `json:"user_uid"`
	}
	if err := json.Unmarshal(raw, &requests); err != nil {
		return errors.New("好友申请列表回包格式不正确，未自动处理")
	}
	keys := map[string]bool{}
	for _, request := range requests {
		if request.RequestKey == "" || request.UserID != event.Author() {
			continue
		}
		if event.UserUID != "" && request.UserUID != "" && request.UserUID != event.UserUID {
			continue
		}
		keys[request.RequestKey] = true
	}
	if len(keys) != 1 {
		return fmt.Errorf("当前列表中找到 %d 条匹配好友申请，未自动处理", len(keys))
	}
	var requestKey string
	for key := range keys {
		requestKey = key
	}
	raw, err = e.call(account, "set_friend_request", M{"request_key": requestKey, "approve": true})
	if err != nil {
		return err
	}
	return confirmedSuccess(raw)
}

func answerFor(c Config, m Event, message string) (string, bool) {
	if answer, ok := c.QA[message]; ok {
		return answer, true
	}
	scope := "friend"
	if m.GroupID > 0 {
		scope = "group"
	}
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return "", false
	}
	rules := append([]AnswerRule(nil), c.AnswerRules...)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Match != rules[j].Match {
			return rules[i].Match == "exact"
		}
		return len([]rune(rules[i].Question)) > len([]rune(rules[j].Question))
	})
	for _, rule := range rules {
		if rule.Scope != scope && rule.Scope != "both" {
			continue
		}
		question := strings.ToLower(strings.TrimSpace(rule.Question))
		if question == "" {
			continue
		}
		if rule.Match == "exact" && message == question || rule.Match == "fuzzy" && strings.Contains(message, question) {
			return rule.Answer, true
		}
	}
	return "", false
}

func monitorEntry(m Event) (string, string) {
	name := strings.TrimSpace(m.Sender.Card)
	if name == "" {
		name = strings.TrimSpace(m.Sender.Remark)
	}
	if name == "" {
		name = strings.TrimSpace(m.Sender.Nickname)
	}
	if name == "" {
		name = "未命名"
	}
	var kind, location, sender string
	switch m.EventType {
	case "group_message_received":
		kind, location = "群消息", fmt.Sprintf("群 %d", m.GroupID)
		sender = fmt.Sprintf("%s [QQ %d]", name, m.Author())
	case "private_message_received":
		kind, location = "好友消息", "私聊"
		sender = fmt.Sprintf("%s [QQ %d]", name, m.Author())
	case "message_sent":
		kind, sender = "本人发出", fmt.Sprintf("QQ %d", m.SelfID)
		if m.GroupID > 0 {
			location = fmt.Sprintf("群 %d", m.GroupID)
		} else {
			location = fmt.Sprintf("好友 %d", m.Peer())
		}
	default:
		return m.EventType, m.Text()
	}
	return kind, fmt.Sprintf("%s · %s\n内容：%s", location, sender, m.Text())
}
func (e *Engine) notifyOnline(m Event, c Config) {
	day := time.Now().Format("2006-01-02")
	if !e.cool(m.Account().Key()+":online:"+day, 60) {
		return
	}
	e.background(m.Account().Key()+":notify", func() {
		for _, id := range c.NotifyTargets {
			if err := e.send(m.Account(), 0, id, []Segment{{Type: "text", Data: M{"text": expand(c.NotifyText, m)}}}); err != nil {
				log.Printf("上线通知发送失败：%v", err)
			}
		}
	})
}
func (e *Engine) passive(m Event, c Config) {
	key := m.Account().Key()
	if m.GroupID > 0 && c.On("at", m.GroupID) {
		for _, s := range m.Message {
			if s.Type == "at" && (text(s.Data["uin"]) == strconv.FormatInt(m.SelfID, 10) || number(s.Data["user_id"]) == m.SelfID) && e.cool(key+":at:"+strconv.FormatInt(m.GroupID, 10), c.Cooldown) {
				_ = e.reply(m, c.AtText)
				break
			}
		}
	}
	if m.GroupID > 0 && c.On("emoji", m.GroupID) && c.Emoji != "" && e.cool(key+":emoji:"+strconv.FormatInt(m.GroupID, 10), c.Cooldown) {
		e.background(fmt.Sprintf("%s:emoji:%d", key, m.MessageID), func() {
			_, err := e.call(m.Account(), "set_msg_emoji_like", M{"message_id": m.MessageID, "emoji_id": c.Emoji, "set": true})
			if err != nil {
				log.Printf("表情回应失败：%v", err)
			}
		})
	}
	flashIndex := 0
	for _, s := range m.Message {
		if s.Type == "flash_image" {
			idx := flashIndex
			flashIndex++
			if c.On("antiflash", m.GroupID) && m.MessageID > 0 {
				e.background(fmt.Sprintf("%s:flash:%d:%d", key, m.MessageID, idx), func() { e.saveFlash(m, c, idx) })
			}
		}
		if s.Type == "red_packet" && m.GroupID > 0 && c.On("qiangmi", m.GroupID) {
			packet := s.Data
			e.background(key+":red:"+text(packet["listid"]), func() { e.grab(m, c, packet) })
		}
	}
}
func (e *Engine) grab(m Event, c Config, packet M) {
	if text(packet["listid"]) == "" || text(packet["authkey"]) == "" {
		return
	}
	seen, err := e.Store.Seen(m.Account().Key(), "red:"+text(packet["listid"]))
	if err != nil || seen {
		return
	}
	p := M{}
	for k, v := range packet {
		p[k] = v
	}
	p["group_id"] = m.GroupID
	p["sender_uin"] = m.Author()
	p["red_packet"] = packet
	raw, err := e.call(m.Account(), "get_red_packet_info", p)
	if err != nil {
		e.owner(m, c, "红包查询失败："+err.Error())
		return
	}
	var info M
	if json.Unmarshal(raw, &info) != nil || text(info["pre_grap_token"]) == "" {
		e.owner(m, c, "红包查询未返回预领取令牌，未提交领取")
		return
	}
	p["pre_grap_token"] = info["pre_grap_token"]
	raw, err = e.call(m.Account(), "grab_red_packet", p)
	if err != nil {
		e.owner(m, c, "红包领取返回错误："+err.Error())
		return
	}
	e.owner(m, c, "红包领取回执：\n"+string(raw))
}
func (e *Engine) schedule() {
	if !e.allowed() {
		return
	}
	if !e.Client.Active() {
		return
	}
	now := time.Now()
	for _, a := range e.Accounts() {
		if a.Status != 1 {
			continue
		}
		state, err := e.Store.Get(a.Key())
		if err != nil {
			continue
		}
		c := state.Config
		for _, job := range c.Jobs {
			j := job
			if !j.Enabled || !c.On(j.Kind, 0) || j.Next > now.Unix() {
				continue
			}
			e.background(a.Key()+":job:"+j.ID, func() {
				var run Job
				claimed := false
				_, err := e.Store.Update(a.Key(), -1, func(current *Config) error {
					for i := range current.Jobs {
						p := &current.Jobs[i]
						if p.ID == j.ID && p.Enabled && current.On(p.Kind, 0) && p.Next <= time.Now().Unix() {
							run = *p
							if p.Kind == "loop" {
								p.Next = time.Now().Add(time.Duration(p.Interval) * time.Second).Unix()
							} else {
								p.Next = nextDaily(p.Time, time.Now()).Unix()
							}
							claimed = true
						}
					}
					return nil
				})
				if err != nil || !claimed {
					return
				}
				var resultText string
				if run.Kind == "timedss" {
					_, err = e.call(a, "publish_qzone_feed", M{"content": expand(run.Text, Event{SelfID: a.SelfID, ClientType: a.ClientType}), "visibility": 1})
					if err != nil {
						resultText = "执行失败：" + err.Error()
						log.Printf("定时说说失败：%v", err)
					} else {
						resultText = "已提交到 QQ 空间。"
					}
				} else {
					target := run.Target
					if target == "" {
						target = "group"
					}
					targets := []string{target}
					if target == "both" {
						targets = []string{"group", "friend"}
					}
					var summaries []string
					var failures []string
					for _, destination := range targets {
						summary, sendErr := e.massTo(a, run.Text, destination)
						if sendErr != nil {
							name := "群聊"
							if destination == "friend" {
								name = "好友私聊"
							}
							failures = append(failures, name+"执行失败："+sendErr.Error())
							log.Printf("%s 群发任务失败：%v", destination, sendErr)
							continue
						}
						summaries = append(summaries, summary)
					}
					resultText = strings.Join(append(summaries, failures...), "\n")
					if resultText == "" {
						resultText = "任务没有可执行的发送对象。请检查群发名单和功能开关。"
					}
				}
				_, recordErr := e.Store.Update(a.Key(), -1, func(current *Config) error {
					for i := range current.Jobs {
						if current.Jobs[i].ID == run.ID {
							current.Jobs[i].LastRun = time.Now().Unix()
							current.Jobs[i].LastResult = resultText
							break
						}
					}
					return nil
				})
				if recordErr != nil {
					log.Printf("自动任务结果保存失败：%v", recordErr)
				}
			})
		}
		signDate := now.Format("2006-01-02")
		if c.SignEnabled && c.On("daka", 0) && now.Format("15:04") >= c.SignTime && c.SignLastRun != signDate {
			e.background(a.Key()+":scheduled-group-sign", func() {
				var ids []string
				claimed := false
				_, updateErr := e.Store.Update(a.Key(), -1, func(current *Config) error {
					if !current.SignEnabled || !current.On("daka", 0) || current.SignLastRun == signDate || now.Format("15:04") < current.SignTime {
						return nil
					}
					ids = selectedSignIDs(*current)
					if len(ids) == 0 {
						return nil
					}
					current.SignLastRun = signDate
					claimed = true
					return nil
				})
				if updateErr != nil || !claimed {
					return
				}
				result := e.runGroupSigns(a, ids)
				_, saveErr := e.Store.Update(a.Key(), -1, func(current *Config) error {
					if current.SignLastRun == signDate {
						current.SignLastResult = result
					}
					return nil
				})
				if saveErr != nil {
					log.Printf("定时群打卡结果保存失败：%v", saveErr)
				}
				log.Printf("账号 %s 定时群打卡：%s", a.Key(), strings.ReplaceAll(result, "\n", "；"))
			})
		}
		e.mu.Lock()
		pollDue := !now.Before(e.polling[a.Key()])
		if c.On("miao", 0) && pollDue {
			e.polling[a.Key()] = now.Add(time.Duration(c.PollSeconds) * time.Second)
		}
		if !c.On("miao", 0) {
			delete(e.feedBase, a.Key())
			delete(e.polling, a.Key())
		}
		e.mu.Unlock()
		if c.On("miao", 0) && pollDue {
			e.background(a.Key()+":feeds", func() { e.pollFeeds(a, c) })
		}
		if c.On("xuhuo", 0) && now.Format("15:04") >= c.XuhuoTime {
			e.background(a.Key()+":xuhuo", func() {
				ids, err := e.Store.Reminders(a.Key())
				if err != nil {
					return
				}
				for _, id := range ids {
					if err = e.Store.MarkReminded(a.Key(), id); err != nil {
						continue
					}
					if err = e.send(a, 0, id, []Segment{{Type: "text", Data: M{"text": expand(xuhuoText(c), Event{SelfID: a.SelfID, ClientType: a.ClientType})}}}); err != nil {
						log.Printf("续火提醒失败：%v", err)
					}
				}
			})
		}
	}
}

func xuhuoText(c Config) string {
	if strings.TrimSpace(c.XuhuoText) == "" {
		return "今天也来聊两句吧。"
	}
	return c.XuhuoText
}
func nextDaily(clock string, now time.Time) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", now.Format("2006-01-02")+" "+clock, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func (e *Engine) runGroupSigns(a Account, ids []string) string {
	if len(ids) == 0 {
		return "【群打卡结果】\n名单中没有已启用的群。"
	}
	type signResult struct {
		id  string
		err error
	}
	results := make([]signResult, len(ids))
	jobs := make(chan int)
	workers := 3
	if len(ids) < workers {
		workers = len(ids)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				id := ids[index]
				var raw json.RawMessage
				raw, err := e.call(a, "group_sign", M{"group_id": number(id)})
				// Codex 修改：定时打卡也检查 3.0 回包的业务结果。
				if err == nil && e.protocolV3.Load() {
					err = confirmedSuccess(raw)
				}
				results[index] = signResult{id: id, err: err}
			}
		}()
	}
	for i := range ids {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	succeeded := 0
	var failures []string
	for _, result := range results {
		if result.err == nil {
			succeeded++
			continue
		}
		if len(failures) < 8 {
			failures = append(failures, result.id+"："+result.err.Error())
		}
	}
	lines := []string{"【群打卡结果】", fmt.Sprintf("成功：%d 个群\n失败：%d 个群", succeeded, len(ids)-succeeded)}
	if len(failures) > 0 {
		lines = append(lines, "失败群：\n"+strings.Join(failures, "\n"))
		if len(ids)-succeeded > len(failures) {
			lines = append(lines, fmt.Sprintf("另有 %d 个失败群。", len(ids)-succeeded-len(failures)))
		}
	}
	return strings.Join(lines, "\n")
}

func (e *Engine) mass(a Account, body string) (string, error) {
	return e.massTo(a, body, "group")
}

func (e *Engine) massTo(a Account, body, target string) (string, error) {
	state, err := e.Store.Get(a.Key())
	if err != nil {
		return "", err
	}
	groupTarget := target == "group"
	if !groupTarget && target != "friend" {
		return "", errors.New("群发对象设置无效")
	}
	ids := selectedMassIDs(state.Config, target)
	return e.massToIDs(a, body, target, ids)
}

func (e *Engine) massToIDs(a Account, body, target string, ids []string) (string, error) {
	groupTarget := target == "group"
	if !groupTarget && target != "friend" {
		return "", errors.New("群发对象设置无效")
	}
	sent := 0
	failed := 0
	skipped := 0
	var failures []string
	for _, id := range ids {
		select {
		case <-e.ctx.Done():
			return "", e.ctx.Err()
		default:
		}
		fresh, err := e.Store.Get(a.Key())
		if err != nil {
			return "", err
		}
		gid := number(id)
		stillEnabled := fresh.Config.MassFriends[id] && !fresh.Config.MassFriendsExcluded[id] && fresh.Config.On("qunfa", 0)
		if groupTarget {
			stillEnabled = fresh.Config.Mass[id] && !fresh.Config.MassExcluded[id] && fresh.Config.On("qunfa", gid)
		}
		if !stillEnabled {
			skipped++
			continue
		}
		var groupID, userID int64
		if groupTarget {
			groupID = gid
		}
		if !groupTarget {
			userID = gid
		}
		m := Event{SelfID: a.SelfID, ClientType: a.ClientType, GroupID: groupID}
		if err = e.send(a, groupID, userID, []Segment{{Type: "text", Data: M{"text": expand(body, m)}}}); err != nil {
			failed++
			if len(failures) < 5 {
				failures = append(failures, id+"："+err.Error())
			}
			log.Printf("群发对象 %s (%s) 发送失败：%v", id, target, err)
		} else {
			sent++
		}
		timer := time.NewTimer(350 * time.Millisecond)
		select {
		case <-timer.C:
		case <-e.ctx.Done():
			timer.Stop()
			return "", e.ctx.Err()
		}
	}
	if len(ids) == 0 {
		if groupTarget {
			return "", errors.New("还没有选择要接收群消息的群；到“问答与白名单”添加群，或发送“添加群发群 群号”")
		}
		return "", errors.New("还没有选择要接收私聊消息的好友；到“问答与白名单”添加好友，或发送“添加群发好友 QQ号”")
	}
	title, unit := "群消息发送结果", "个群"
	if !groupTarget {
		title, unit = "好友私聊发送结果", "位好友"
	}
	lines := []string{"【" + title + "】", fmt.Sprintf("成功：%d%s\n失败：%d%s\n跳过：%d%s", sent, unit, failed, unit, skipped, unit)}
	if skipped > 0 {
		lines = append(lines, "已跳过发送前被移除、停用或加入排除名单的对象。")
	}
	if failed > 0 {
		lines = append(lines, "\n失败对象：", strings.Join(failures, "\n"))
		if failed > len(failures) {
			lines = append(lines, fmt.Sprintf("另有 %d 个失败对象，详情见运行日志。", failed-len(failures)))
		}
		lines = append(lines, "接口超时的对象请先核对 QQ 消息，再决定是否重发。")
	}
	return strings.Join(lines, "\n"), nil
}

func (e *Engine) massNames(a Account, target string) map[string]string {
	action, field := "get_group_list", "groups"
	if target == "friend" {
		action, field = "get_friend_list", "friends"
	}
	raw, err := e.call(a, action, M{})
	if err != nil {
		return nil
	}
	items, err := contactItems(raw, field)
	if err != nil {
		return nil
	}
	names := make(map[string]string, len(items))
	for _, item := range items {
		id, name := strings.TrimSpace(fmt.Sprint(item["id"])), strings.TrimSpace(fmt.Sprint(item["name"]))
		if id != "" && id != "<nil>" && name != "" && name != "<nil>" {
			names[id] = name
		}
	}
	return names
}

func (e *Engine) prepareMass(m Event, body, target string) (string, error) {
	state, err := e.Store.Get(m.Account().Key())
	if err != nil {
		return "", err
	}
	configured := state.Config.Mass
	targetName, unit := "群聊", "个群"
	if target == "friend" {
		configured, targetName, unit = state.Config.MassFriends, "好友私聊", "位好友"
	}
	ids := selectedMassIDs(state.Config, target)
	if len(ids) == 0 {
		if len(configured) > 0 {
			kind, numberLabel := "群", "群号"
			if target == "friend" {
				kind, numberLabel = "好友", "QQ号"
			}
			return "名单中的" + kind + "都已停用或加入排除名单。可发“群发排除" + kind + " 删除 " + numberLabel + "”恢复接收。", nil
		}
		if target == "friend" {
			return "还没有选择接收私聊消息的好友。可发“添加群发好友 QQ号”，再发这条指令。", nil
		}
		return "还没有选择接收群消息的群。可发“添加群发群 群号”，再发这条指令。", nil
	}
	preview := append([]string(nil), ids...)
	if len(preview) > 5 {
		preview = preview[:5]
	}
	names := e.massNames(m.Account(), target)
	for i, id := range preview {
		if name := names[id]; name != "" {
			preview[i] = name + "（" + id + "）"
		}
	}
	more := ""
	if len(ids) > len(preview) {
		more = " 等"
	}
	e.mu.Lock()
	for key, pending := range e.pendingMass {
		if time.Since(pending.Created) > 2*time.Minute {
			delete(e.pendingMass, key)
		}
	}
	e.pendingMass[pendingMassKey(m)] = PendingMass{Target: target, Text: body, IDs: append([]string(nil), ids...), Author: m.Author(), Created: time.Now()}
	e.mu.Unlock()
	listHint := ""
	if len(ids) > len(preview) {
		listCommand := "群发群列表"
		if target == "friend" {
			listCommand = "群发好友列表"
		}
		listHint = "\n完整名单可先发“" + listCommand + "”查看。"
	}
	return fmt.Sprintf("【发送预览】\n对象：%s\n数量：%d%s\n收件人：%s%s%s\n\n发送内容：\n%s\n\n2 分钟内回复“确认发送”开始，回复“取消发送”放弃。", targetName, len(ids), unit, strings.Join(preview, "、"), more, listHint, body), nil
}

func (e *Engine) cancelMass(m Event) string {
	// Codex 修改：群发确认回复只保留当前状态和必要结果。
	e.mu.Lock()
	defer e.mu.Unlock()
	pending, ok := e.pendingMass[pendingMassKey(m)]
	if !ok || pending.Author != m.Author() || time.Since(pending.Created) > 2*time.Minute {
		return "没有待确认的发送预览。已开始的发送不会受影响。"
	}
	delete(e.pendingMass, pendingMassKey(m))
	return "已取消，刚才预览的消息没有发送。"
}

func pendingMassKey(m Event) string {
	return fmt.Sprintf("%s:%d:%d:%d", m.Account().Key(), m.Author(), m.GroupID, m.Peer())
}

func (e *Engine) confirmMass(m Event) (string, error) {
	key := pendingMassKey(m)
	e.mu.Lock()
	pending, ok := e.pendingMass[key]
	if ok && (time.Since(pending.Created) > 2*time.Minute || pending.Author != m.Author()) {
		delete(e.pendingMass, key)
		ok = false
	}
	if ok {
		delete(e.pendingMass, key)
	}
	e.mu.Unlock()
	if !ok {
		return "没有待确认的群发预览，或预览已过期。请重新发“群发群 内容”或“群发好友 内容”。", nil
	}
	if !e.background(m.Account().Key()+":manual-mass", func() {
		result, err := e.massToIDs(m.Account(), pending.Text, pending.Target, pending.IDs)
		if err != nil {
			if result != "" {
				result += "\n失败原因：" + err.Error()
			} else {
				result = err.Error()
			}
		}
		e.replyCommand(m, result)
	}) {
		e.mu.Lock()
		if _, exists := e.pendingMass[key]; !exists {
			e.pendingMass[key] = pending
		}
		e.mu.Unlock()
		return "本账号已有发送任务或后台队列繁忙，这次没有发送。预览已保留，可稍后再次确认。", nil
	}
	title := "好友私聊"
	if pending.Target == "group" {
		title = "群消息"
	}
	return "已确认，开始发送" + title + "。完成后会回复确认数量和失败数量。", nil
}

func (e *Engine) allowed() bool { return e.Policy == nil || e.Policy.Allowed() }
