// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import "testing"

func TestMonitorRecordsAreScopedPerAccount(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, row := range []struct{ account, body string }{{"1:android", "甲"}, {"2:linuxqq", "乙"}} {
		if err = store.Log(row.account, "group_message_received", row.body); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.Monitor("1:android", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Body != "甲" {
		t.Fatalf("监控记录必须按账号隔离：%v", rows)
	}
	if err = store.ClearMonitor("1:android"); err != nil {
		t.Fatal(err)
	}
	if rows, err = store.Monitor("1:android", 10); err != nil || len(rows) != 0 {
		t.Fatalf("清空未生效：%v %v", rows, err)
	}
	if rows, err = store.Monitor("2:linuxqq", 10); err != nil || len(rows) != 1 {
		t.Fatalf("清空不应影响其他账号：%v %v", rows, err)
	}
}

func TestXuhuoTextFallsBackForLegacyConfig(t *testing.T) {
	if got := xuhuoText(Config{}); got != "今天也来聊两句吧。" {
		t.Fatalf("旧配置缺少 xuhuo_text 时应沿用内置话术，实际 %q", got)
	}
	if got := xuhuoText(Config{XuhuoText: "  "}); got != "今天也来聊两句吧。" {
		t.Fatalf("空白文案应回退内置话术，实际 %q", got)
	}
	if got := xuhuoText(Config{XuhuoText: "记得喝水"}); got != "记得喝水" {
		t.Fatalf("自定义文案未生效：%q", got)
	}
	if defaults().XuhuoText == "" {
		t.Fatal("默认配置必须带续火文案")
	}
}
