// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.
// Codex 修改：验证菜单预览、指令开关与群发排除名单。

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type menuPreview struct {
	Global     string                                `json:"global"`
	Categories []struct{ ID, Trigger, Reply string } `json:"categories"`
	Preview    struct {
		Global   string
		Features map[string]string
	} `json:"preview"`
}

func readMenuPreview(t *testing.T, managed *Managed, base, token string) menuPreview {
	t.Helper()
	res := managedRequest(t, http.MethodGet, base+"/api/menus/defaults", token, managed.AdminOrigin(), map[string]string{"X-Catherina-Account": "10001:android"})
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("menu preview HTTP %d", res.StatusCode)
	}
	var result menuPreview
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMenuPreviewTracksAliasesAndCommandSwitches(t *testing.T) {
	managed, engine, store, base, token := managedAdminFixture(t)
	engine.accounts["10001:android"] = Account{SelfID: 10001, ClientType: "android", Status: 1}
	_, err := store.Update("10001:android", -1, func(c *Config) error { c.Aliases["随机字母"] = "抽字母"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	first := readMenuPreview(t, managed, base, token)
	if len(first.Categories) != 6 || !strings.Contains(first.Global, "----\n群发菜单") || strings.Contains(first.Global, "初始化配置") {
		t.Fatalf("top level is not a short directory: %s", first.Global)
	}
	if !strings.Contains(first.Preview.Features["rand"], "抽字母 [长度]") {
		t.Fatalf("preview has stale triggers or placeholders: %s", first.Preview.Features["rand"])
	}
	_, err = store.Update("10001:android", -1, func(c *Config) error { c.CommandSwitch["随机字母"] = false; return nil })
	if err != nil {
		t.Fatal(err)
	}
	second := readMenuPreview(t, managed, base, token)
	for _, category := range second.Categories {
		if strings.Contains(category.Reply, "抽字母 [长度]") {
			t.Fatalf("disabled command still advertised: %s", category.Reply)
		}
	}
	state, err := store.Get("10001:android")
	if err != nil || state.Config.MenuContent != "" {
		t.Fatal("preview froze generated menu into account settings")
	}
}

func TestSharedEditedMenuMatchesActualQQReply(t *testing.T) {
	managed, engine, store, base, token := managedAdminFixture(t)
	replies := make(chan string, 1)
	attachUXFramework(t, engine, func(action string, p M) (any, error) {
		if action != "send_private_msg" {
			return nil, fmt.Errorf("unexpected action %s", action)
		}
		raw, _ := json.Marshal(p["message"])
		var segments []Segment
		_ = json.Unmarshal(raw, &segments)
		if len(segments) > 0 {
			replies <- text(segments[0].Data["text"])
		}
		return M{"message_id": 99}, nil
	})
	global, err := store.Global()
	if err != nil {
		t.Fatal(err)
	}
	custom := "【我的群发菜单】\n• 群发群 周末见\n• 取消发送"
	patch := map[string]json.RawMessage{}
	for k, v := range (M{"menu_content": "【私人助手】\n• 群发菜单", "menu_prefix": "你好，\n", "menu_suffix": "\n今天也开心", "replies": map[string]string{"qunfa": custom}}) {
		raw, _ := json.Marshal(v)
		patch[k] = raw
	}
	if _, err = store.PatchGlobal(global.Revision, patch); err != nil {
		t.Fatal(err)
	}
	preview := readMenuPreview(t, managed, base, token)
	for _, word := range []string{"菜单", "总菜单"} {
		m := Event{SelfID: 10001, UserID: 10001, ClientType: "android"}
		reply, err := engine.execute(m, defaults(), Command{Word: word}, "")
		if err != nil || expand(reply, m) != preview.Preview.Global {
			t.Fatalf("%s bypassed the shared menu body: %s %v", word, reply, err)
		}
	}
	var expected string
	for _, category := range preview.Categories {
		if category.ID == "群发菜单" {
			expected = category.Reply
		}
	}
	if !strings.Contains(expected, custom) || !strings.HasPrefix(expected, "你好，\n") {
		t.Fatalf("edited shared reply missing from preview: %s", expected)
	}
	engine.handle(Event{EventType: "private_message_received", SelfID: 10001, UserID: 10001, ClientType: "android", RawMessage: "群发菜单"})
	if actual := <-replies; actual != expected {
		t.Fatalf("preview and actual QQ payload differ\npreview: %s\nactual: %s", expected, actual)
	}
}

func TestMassMenuAndExclusionCommandsAgree(t *testing.T) {
	_, engine, store, _, _ := managedAdminFixture(t)
	m := Event{SelfID: 10001, UserID: 10001, ClientType: "android"}
	_, err := store.Update(m.Account().Key(), -1, func(c *Config) error {
		c.Mass["90001"], c.Mass["90002"] = true, true
		c.MassFriends["20001"], c.MassFriends["20002"] = true, true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ word, arg string }{{"群发排除群", "添加 90001"}, {"群发排除好友", "添加 20001"}} {
		state, err := store.Get(m.Account().Key())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := engine.execute(m, state.Config, Command{Word: tc.word, Feature: "qunfa"}, tc.arg); err != nil {
			t.Fatal(err)
		}
	}
	state, err := store.Get(m.Account().Key())
	if err != nil {
		t.Fatal(err)
	}
	if got := selectedMassIDs(state.Config, "group"); len(got) != 1 || got[0] != "90002" {
		t.Fatalf("group exclusions: %v", got)
	}
	if got := selectedMassIDs(state.Config, "friend"); len(got) != 1 || got[0] != "20002" {
		t.Fatalf("friend exclusions: %v", got)
	}
	menu := featureHelp(state.Config, m, "qunfa")
	if !strings.Contains(menu, "群发排除名单（名单中的对象不会收到群发）\n群发排除群 添加 群号") || !strings.Contains(menu, "群发排除好友 列表") {
		t.Fatalf("missing exclusion section: %s", menu)
	}
}
