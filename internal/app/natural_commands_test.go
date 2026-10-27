// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.
// Codex 修改：验证自然指令、问答、群发排除与群管理。

package app

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestScheduleClockAcceptsCommonInput(t *testing.T) {
	for input, want := range map[string]string{
		"8.00":   "08:00",
		"8:00":   "08:00",
		"20:00":  "20:00",
		"8：00":   "08:00",
		"晚上8.00": "20:00",
	} {
		got, ok := parseScheduleClock(input)
		if !ok || got != want {
			t.Errorf("parseScheduleClock(%q) = %q, %t; want %q", input, got, ok, want)
		}
	}
}

func TestCommandTextReadsMengkaMentionUserID(t *testing.T) {
	m := Event{Message: []Segment{
		{Type: "text", Data: M{"text": "成员查询 "}},
		{Type: "mention", Data: M{"user_id": "20001"}},
	}}
	if got := commandText(m, defaults()); got != "成员查询 20001" {
		t.Fatalf("mention target was lost: %q", got)
	}
}

func TestMemberQueryExecutesFromMengkaMentionSegment(t *testing.T) {
	_, engine, _, _, _ := managedAdminFixture(t)
	requests := make(chan struct{}, 1)
	attachUXFramework(t, engine, func(action string, params M) (any, error) {
		switch action {
		case "get_group_member_list":
			requests <- struct{}{}
			return M{"members": []M{{"uin": 20001, "nickname": "小满"}}}, nil
		case "send_group_msg":
			return M{"message_id": 123}, nil
		default:
			return nil, fmt.Errorf("unexpected action %s", action)
		}
	})
	m := Event{SelfID: 10001, UserID: 10001, GroupID: 90001, ClientType: "android", Message: []Segment{
		{Type: "text", Data: M{"text": "成员查询 "}},
		{Type: "mention", Data: M{"user_id": "20001"}},
	}}
	if !engine.command(m, defaults()) {
		t.Fatal("member query mention command was not recognized")
	}
	select {
	case <-requests:
	default:
		t.Fatal("member query did not call the member-list API")
	}
}

func TestAnswerRulesRespectScopeAndPreferExactMatch(t *testing.T) {
	c := defaults()
	c.AnswerRules = []AnswerRule{
		{Question: "早安", Answer: "模糊问答", Match: "fuzzy", Scope: "both"},
		{Question: "早安呀", Answer: "群聊精准问答", Match: "exact", Scope: "group"},
	}
	if got, ok := answerFor(c, Event{GroupID: 90001}, "早安呀"); !ok || got != "群聊精准问答" {
		t.Fatalf("group exact rule lost priority: %q, %t", got, ok)
	}
	if got, ok := answerFor(c, Event{}, "早安呀"); !ok || got != "模糊问答" {
		t.Fatalf("shared fuzzy rule did not match friend: %q, %t", got, ok)
	}
	if _, ok := answerFor(c, Event{}, "早安呀！"); !ok {
		t.Fatal("fuzzy matching did not find the keyword")
	}
}

func TestNaturalAnswerRuleSyntaxDefaultsToCurrentChat(t *testing.T) {
	group, err := parseAnswerRule("你好答你好呀", "exact", true)
	if err != nil || group.Scope != "group" || group.Question != "你好" || group.Answer != "你好呀" {
		t.Fatalf("group syntax: %+v, %v", group, err)
	}
	friend, err := parseAnswerRule("合并 在吗答我在", "fuzzy", false)
	if err != nil || friend.Scope != "both" || friend.Match != "fuzzy" {
		t.Fatalf("shared syntax: %+v, %v", friend, err)
	}
}

func TestAddAnswerCommandPersistsScopeAndMode(t *testing.T) {
	_, engine, store, _, _ := managedAdminFixture(t)
	m := Event{SelfID: 10001, UserID: 10001, GroupID: 90001, ClientType: "android"}
	_, err := engine.execute(m, defaults(), Command{Word: "添加模糊问", Feature: "qa"}, "群聊 早安答早呀")
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Get(m.Account().Key())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Config.AnswerRules) != 1 || state.Config.AnswerRules[0].Match != "fuzzy" || state.Config.AnswerRules[0].Scope != "group" {
		t.Fatalf("answer rule was not persisted as requested: %+v", state.Config.AnswerRules)
	}
}

func TestSelectedMassIDsSkipsExplicitExclusions(t *testing.T) {
	c := defaults()
	c.Mass = map[string]bool{"20001": true, "20002": true}
	c.MassExcluded = map[string]bool{"20001": true}
	got := selectedMassIDs(c, "group")
	if len(got) != 1 || got[0] != "20002" {
		t.Fatalf("excluded group still selected: %v", got)
	}
}

func TestCommandMenusDoNotRepeatGroupAssistant(t *testing.T) {
	help := featureHelp(defaults(), Event{SelfID: 10001, UserID: 10001}, "assist")
	if strings.Count(help, "群聊助理") != 1 || strings.Contains(help, "• 群聊助理") {
		t.Fatalf("group assistant menu repeats its own trigger: %s", help)
	}
}

func TestOneClickGroupSignUsesConfiguredWhitelist(t *testing.T) {
	_, engine, store, _, _ := managedAdminFixture(t)
	account := Account{SelfID: 10001, ClientType: "android", Status: 1}
	_, err := store.Update(account.Key(), -1, func(c *Config) error {
		c.SignGroups["90002"] = true
		c.SignGroups["90001"] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []int64
	attachUXFramework(t, engine, func(action string, params M) (any, error) {
		if action != "group_sign" {
			return nil, fmt.Errorf("unexpected action %s", action)
		}
		mu.Lock()
		got = append(got, number(params["group_id"]))
		mu.Unlock()
		return M{"signed": true}, nil
	})
	result := engine.runGroupSigns(account, []string{"90002", "90001"})
	if !strings.Contains(result, "成功：2 个群") {
		t.Fatalf("batch sign result: %s", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("group_sign called %d times", len(got))
	}
}
