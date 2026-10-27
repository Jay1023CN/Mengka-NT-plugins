// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"bufio"
	"catherina/internal/mengka"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSemanticVersionMigration(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		newer bool
	}{
		{"1.0.0", "2026.10.1701", true}, {"2026.10.1701", "1.0.0", false},
		{"1.0.10", "1.0.9", true}, {"1.1.0", "1.0.99", true}, {"2.0.0", "1.99.99", true},
		{"1.0.0", "1.0.0", false}, {"1.0.0", "1.0.1", false},
	} {
		if got := versionNewer(tc.a, tc.b); got != tc.newer {
			t.Errorf("%s > %s = %v", tc.a, tc.b, got)
		}
	}
}

func TestReleaseStatusClassification(t *testing.T) {
	for _, tc := range []struct {
		current, latest, want string
	}{
		{"1.0.2", "1.0.2", "current"},
		{"1.0.2", "1.0.0", "ahead"},
		{"1.0.2", "1.0.3", "available"},
		{"2026.10.1607", "2026.10.1701", "available"},

		{"2026.10.1701", "1.0.0", "available"},
	} {
		if got := releaseStatus(tc.current, tc.latest); got != tc.want {
			t.Errorf("current=%s latest=%s: status %q, want %q", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestDebugMengkaRoutesSelectedAccount(t *testing.T) {
	managed, engine, _, base, token := managedAdminFixture(t)
	incoming, frameworkOut := io.Pipe()
	frameworkIn, outgoing := io.Pipe()
	client := mengka.New(outgoing, Actions)
	engine.Client = client
	engine.accounts["10001:android"] = Account{SelfID: 10001, ClientType: "android", Status: 1}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		incoming.Close()
		outgoing.Close()
		frameworkIn.Close()
		frameworkOut.Close()
		client.Close()
	})
	go client.Run(ctx, incoming)
	enc := json.NewEncoder(frameworkOut)
	_ = enc.Encode(M{"type": "ready", "transport": "native-ipc-v1", "api_version": "1", "config_revision": 0, "config": M{}})
	_ = enc.Encode(M{"type": "lifecycle", "event": "activate"})
	deadline := time.Now().Add(time.Second)
	for !client.Active() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !client.Active() {
		t.Fatal("fixture did not activate")
	}
	received := make(chan M, 1)
	go func() {
		scanner := bufio.NewScanner(frameworkIn)
		if scanner.Scan() {
			var action M
			_ = json.Unmarshal(scanner.Bytes(), &action)
			received <- action
			_ = enc.Encode(M{"type": "action_result", "id": action["id"], "ok": true, "data": M{"nickname": "测试账号"}})
		}
	}()
	req, _ := http.NewRequest("POST", base+"/api/debug", strings.NewReader(`{"mode":"mengka","action":"get_bot_info","params":{"self_id":99999,"client_type":"linuxqq"}}`))
	req.Header.Set("X-Mengka-Managed-Token", token)
	req.Header.Set("X-Mengka-Managed-Origin", managed.AdminOrigin())
	req.Header.Set("X-Catherina-UI", "1")
	req.Header.Set("X-Catherina-Account", "10001:android")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(raw), "测试账号") {
		t.Fatalf("HTTP %d: %s", res.StatusCode, raw)
	}
	action := <-received
	params := action["params"].(map[string]any)
	if params["self_id"] != float64(10001) || params["client_type"] != "android" {
		t.Fatalf("account scope leaked: %v", params)
	}
}
