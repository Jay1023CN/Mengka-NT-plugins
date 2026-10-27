// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"io"
	"strings"
	"testing"

	"catherina/internal/mengka"
)

func TestSelfAuthoredReceivedCommandWithSourceUpdatesAccountConfig(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	client := mengka.New(io.Discard, Actions)
	defer client.Close()
	engine := NewEngine(client, store, t.TempDir())
	defer engine.cancel()

	engine.handle(Event{
		EventType:  "private_message_received",
		SelfID:     123456,
		UserID:     123456,
		ClientType: "Android",
		Source:     "private_message",
		RawMessage: "关闭 群发",
	})

	state, err := store.Get("123456:Android")
	if err != nil {
		t.Fatal(err)
	}
	if state.Config.On("qunfa", 0) {
		t.Fatal("自己发出的收到消息事件没有执行‘关闭 群发’")
	}
}

func TestScheduleCommandExplainsMissingRecipientsAndConfirmsSavedTarget(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	client := mengka.New(io.Discard, Actions)
	defer client.Close()
	engine := NewEngine(client, store, t.TempDir())
	defer engine.cancel()
	message := Event{SelfID: 123456, UserID: 123456, ClientType: "Android"}
	command := Command{Word: "定时群发群", Feature: "timed", Usage: "定时群发群 每天晚上8点发 晚安"}

	state, err := store.Get(message.Account().Key())
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.execute(message, state.Config, command, "每天晚上8点发 晚安")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "任务未保存") || !strings.Contains(result, "问答与白名单") {
		t.Fatalf("无收件人时应给出可操作的原因，实际回复：%s", result)
	}
	if len(state.Config.Jobs) != 0 {
		t.Fatal("无收件人任务被保存")
	}

	state, err = store.Update(message.Account().Key(), -1, func(c *Config) error {
		c.Mass["987654"] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err = engine.execute(message, state.Config, command, "每天晚上8点发 晚安")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "共 1个群") || !strings.Contains(result, "每天 20:00") || !strings.Contains(result, "晚安") {
		t.Fatalf("保存成功时应确认对象、时间和内容，实际回复：%s", result)
	}
	state, err = store.Get(message.Account().Key())
	if err != nil {
		t.Fatal(err)
	}
	status := taskStatus(state.Config)
	if !strings.Contains(status, "当前收件人：1 个") || !strings.Contains(status, "尚未执行") {
		t.Fatalf("任务状态没有显示当前收件人与执行状态：%s", status)
	}
}

func TestMassFeatureHelpOmitsDisabledNestedOperation(t *testing.T) {
	c := defaults()
	c.CommandSwitch["添加群发群"] = false
	help := featureHelp(c, Event{SelfID: 123456, UserID: 123456}, "qunfa")
	if strings.Contains(help, "群发群 添加 ") {
		t.Fatalf("已关闭的添加指令仍出现在群发菜单：%s", help)
	}
	if !strings.Contains(help, "群发群 删除 ") {
		t.Fatalf("仍可用的删除指令不应被隐藏：%s", help)
	}
}

func TestSearchCommandsUsesConfiguredAlias(t *testing.T) {
	c := defaults()
	c.Aliases["反馈"] = "意见反馈"
	result := searchCommands(c, "意见反馈")
	if !strings.Contains(result, "意见反馈") {
		t.Fatalf("按已设置的指令别名搜索不到结果：%s", result)
	}
}
