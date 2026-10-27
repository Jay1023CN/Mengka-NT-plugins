// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"catherina/internal/mengka"
)

func attachUXFramework(t *testing.T, engine *Engine, handle func(string, M) (any, error)) {
	t.Helper()
	incoming, frameworkOut := io.Pipe()
	frameworkIn, outgoing := io.Pipe()
	client := mengka.New(outgoing, Actions)
	engine.Client = client
	engine.accounts["10001:android"] = Account{SelfID: 10001, ClientType: "android", Status: 1}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		client.Close()
		incoming.Close()
		outgoing.Close()
		frameworkIn.Close()
		frameworkOut.Close()
	})
	go client.Run(ctx, incoming)
	enc := json.NewEncoder(frameworkOut)
	_ = enc.Encode(M{"type": "ready", "transport": "native-ipc-v1", "api_version": "1", "config_revision": 0, "config": M{}})
	_ = enc.Encode(M{"type": "lifecycle", "event": "activate"})
	for deadline := time.Now().Add(time.Second); !client.Active(); {
		if time.Now().After(deadline) {
			t.Fatal("SDK did not activate")
		}
		time.Sleep(time.Millisecond)
	}
	go func() {
		scanner := bufio.NewScanner(frameworkIn)
		for scanner.Scan() {
			var frame struct {
				ID     string `json:"id"`
				Action string `json:"action"`
				Params M      `json:"params"`
			}
			if json.Unmarshal(scanner.Bytes(), &frame) != nil {
				continue
			}
			data, err := handle(frame.Action, frame.Params)
			result := M{"type": "action_result", "id": frame.ID, "ok": err == nil, "data": data}
			if err != nil {
				result["error"] = err.Error()
			}
			if enc.Encode(result) != nil {
				return
			}
		}
	}()
}

func TestNamedMassPreviewSendsOriginalIDsAndKeepsFailuresVisible(t *testing.T) {
	_, engine, store, _, _ := managedAdminFixture(t)
	m := Event{SelfID: 10001, UserID: 10001, ClientType: "android"}
	_, err := store.Update(m.Account().Key(), -1, func(c *Config) error { c.Mass = map[string]bool{"20001": true, "20002": true}; return nil })
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var recipients []int64
	var replies []string
	attachUXFramework(t, engine, func(action string, params M) (any, error) {
		if number(params["self_id"]) != 10001 || params["client_type"] != "android" {
			return nil, fmt.Errorf("wrong account: %v", params)
		}
		switch action {
		case "get_group_list":
			return M{"groups": []M{{"group_id": 20001, "group_name": "家人群"}, {"group_id": 20002, "group_name": "读书会"}}}, nil
		case "send_group_msg":
			mu.Lock()
			recipients = append(recipients, number(params["group_id"]))
			mu.Unlock()
			if number(params["group_id"]) == 20002 {
				return nil, fmt.Errorf("当前群已禁言")
			}
			return M{"success": true, "message_id": 123}, nil
		case "send_private_msg":
			raw, _ := json.Marshal(params["message"])
			var segments []Segment
			_ = json.Unmarshal(raw, &segments)
			mu.Lock()
			for _, s := range segments {
				replies = append(replies, text(s.Data["text"]))
			}
			mu.Unlock()
			return M{"message_id": 456}, nil
		}
		return nil, fmt.Errorf("unexpected action %s", action)
	})
	preview, err := engine.prepareMass(m, "今晚8点集合", "group")
	if err != nil || !strings.Contains(preview, "家人群（20001）") {
		t.Fatalf("preview: %s %v", preview, err)
	}
	other := m
	other.GroupID = 999
	if reply, _ := engine.confirmMass(other); !strings.Contains(reply, "没有待确认") {
		t.Fatalf("another conversation confirmed a preview: %s", reply)
	}
	if _, err = engine.confirmMass(m); err != nil {
		t.Fatal(err)
	}
	select {
	case work := <-engine.work:
		work()
	case <-time.After(time.Second):
		t.Fatal("confirmation did not queue a send")
	}
	mu.Lock()
	actualIDs := fmt.Sprint(recipients)
	reply := strings.Join(replies, "\n")
	mu.Unlock()
	if actualIDs != "[20001 20002]" {
		t.Fatalf("named preview corrupted actual recipients: %s", actualIDs)
	}
	if !strings.Contains(reply, "成功：1个群") || !strings.Contains(reply, "失败：1个群") || !strings.Contains(reply, "20002：当前群已禁言") {
		t.Fatalf("partial failure was hidden: %s", reply)
	}
	if reply, _ := engine.confirmMass(m); !strings.Contains(reply, "没有待确认") {
		t.Fatalf("confirmation could send twice: %s", reply)
	}

	_, err = engine.prepareMass(m, "这条不发送", "group")
	if err != nil {
		t.Fatal(err)
	}
	if reply := engine.cancelMass(m); !strings.Contains(reply, "已取消发送") {
		t.Fatalf("cancel: %s", reply)
	}
	if reply, _ := engine.confirmMass(m); !strings.Contains(reply, "没有待确认") {
		t.Fatalf("cancelled preview was still confirmable: %s", reply)
	}
}

func TestCancellingFriendTaskLeavesGroupTaskEnabled(t *testing.T) {
	_, engine, store, _, _ := managedAdminFixture(t)
	m := Event{SelfID: 10001, UserID: 10001, ClientType: "android"}
	state, err := store.Update(m.Account().Key(), -1, func(c *Config) error {
		c.Jobs = []Job{{ID: "groups", Kind: "timed", Target: "group", Time: "20:00", Text: "群消息", Enabled: true}, {ID: "friends", Kind: "timed", Target: "friend", Time: "21:00", Text: "好友消息", Enabled: true}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.execute(m, state.Config, Command{Word: "定时群发好友", Feature: "timed"}, "取消")
	if err != nil {
		t.Fatal(err)
	}
	state, err = store.Get(m.Account().Key())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Config.Jobs[0].Enabled || state.Config.Jobs[1].Enabled {
		t.Fatalf("friend cancellation changed wrong task: %+v", state.Config.Jobs)
	}
}

func TestMentionMemberCommandUsesDurationAndReadableQuery(t *testing.T) {
	_, engine, _, _, _ := managedAdminFixture(t)
	muted := make(chan M, 1)
	attachUXFramework(t, engine, func(action string, p M) (any, error) {
		switch action {
		case "set_group_mute":
			muted <- p
			return M{"group_id": 90001, "target_uin": 20001, "duration_sec": 600}, nil
		case "get_group_member_list":
			return M{"members": []M{{"uin": 20001, "nickname": "小满", "card": "周末领队", "level": 12, "join_time": 1700000000}}}, nil
		case "get_bot_info":
			return M{"self_id": 10001, "status": 1, "nickname": "我的QQ", "friend_count": 12, "group_count": 3}, nil
		case "send_group_msg":
			return M{"success": true, "message_id": 321}, nil
		}
		return nil, fmt.Errorf("unexpected action %s", action)
	})
	m := Event{SelfID: 10001, UserID: 10001, GroupID: 90001, ClientType: "android", Message: []Segment{{Type: "at", Data: M{"uin": "10001"}}, {Type: "text", Data: M{"text": " 禁言 "}}, {Type: "at", Data: M{"uin": "20001"}}, {Type: "text", Data: M{"text": " 10分钟"}}}}
	if !engine.command(m, defaults()) {
		t.Fatal("member mention command was not recognized")
	}
	select {
	case p := <-muted:
		if number(p["target_uin"]) != 20001 || number(p["duration_sec"]) != 600 {
			t.Fatalf("wrong moderation target/duration: %v", p)
		}
	case <-time.After(time.Second):
		t.Fatal("no moderation action")
	}
	reply, err := engine.execute(m, defaults(), Command{Word: "成员查询"}, "20001")
	if err != nil || !strings.Contains(reply, "群名片：周末领队") || strings.Contains(reply, "\"uin\"") {
		t.Fatalf("member result is not readable: %s %v", reply, err)
	}
	reply, err = engine.execute(m, defaults(), Command{Word: "账号状态"}, "")
	if err != nil || !strings.Contains(reply, "状态：在线") || !strings.Contains(reply, "好友数量：12") {
		t.Fatalf("account result: %s %v", reply, err)
	}
}

func TestJobSaveKeepsHistoryAndResumesFromFutureTime(t *testing.T) {
	managed, engine, store, base, token := managedAdminFixture(t)
	engine.accounts["10001:android"] = Account{SelfID: 10001, ClientType: "android", Status: 1}
	key := "10001:android"
	state, err := store.Update(key, -1, func(c *Config) error {
		c.Mass["20001"] = true
		c.Enabled["timed"] = false
		c.Jobs = []Job{{ID: "saved-task", Kind: "timed", Target: "group", Time: "20:00", Text: "晚安", Next: 1, Enabled: false, LastRun: 1700000000, LastResult: "成功 1 个群"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	save := func(job Job, revision int64) State {
		t.Helper()
		body, _ := json.Marshal(M{"revision": revision, "job": job})
		req, _ := http.NewRequest("POST", base+"/api/jobs", strings.NewReader(string(body)))
		req.Header.Set("X-Mengka-Managed-Token", token)
		req.Header.Set("X-Mengka-Managed-Origin", managed.AdminOrigin())
		req.Header.Set("X-Catherina-UI", "1")
		req.Header.Set("X-Catherina-Account", key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("save HTTP %d: %s", res.StatusCode, raw)
		}
		var result State
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	job := state.Config.Jobs[0]
	job.Enabled = true
	job.LastRun = 0
	job.LastResult = ""
	state = save(job, state.Revision)
	job = state.Config.Jobs[0]
	if job.Next <= time.Now().Unix() || job.LastRun != 1700000000 || job.LastResult != "成功 1 个群" || !state.Config.Enabled["timed"] {
		t.Fatalf("resume lost history or stayed blocked: %+v", job)
	}
	next := job.Next
	job.Text = "早点休息"
	state = save(job, state.Revision)
	if state.Config.Jobs[0].Next != next || state.Config.Jobs[0].LastResult != "成功 1 个群" {
		t.Fatalf("editing content reset next run/history: %+v", state.Config.Jobs[0])
	}
}

func TestContactListUsesSelectedAccountAndVisibleNames(t *testing.T) {
	managed, engine, _, base, token := managedAdminFixture(t)
	attachUXFramework(t, engine, func(action string, p M) (any, error) {
		if number(p["self_id"]) != 10001 || p["client_type"] != "android" {
			return nil, fmt.Errorf("wrong account")
		}
		if action == "get_group_list" {
			return M{"groups": []M{{"group_id": 20001, "group_name": "", "group_memo": "家人群"}, {"group_id": 20001, "group_name": "重复"}, {"group_id": 0}}}, nil
		}
		return M{"friends": []M{{"uin": 30001, "remark": "", "nickname": "小满"}, {"user_id": 30002, "remark": "同事", "nickname": "阿澄"}}}, nil
	})
	for kind, want := range map[string]string{"groups": "家人群", "friends": "小满"} {
		res := managedRequest(t, "GET", base+"/api/contacts?kind="+kind, token, managed.AdminOrigin(), map[string]string{"X-Catherina-Account": "10001:android"})
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(string(raw), want) {
			t.Fatalf("%s list: HTTP %d %s", kind, res.StatusCode, raw)
		}
		var body struct {
			Items []M `json:"items"`
		}
		_ = json.Unmarshal(raw, &body)
		if kind == "groups" && len(body.Items) != 1 {
			t.Fatalf("invalid/duplicate contacts remained selectable: %s", raw)
		}
	}
}
