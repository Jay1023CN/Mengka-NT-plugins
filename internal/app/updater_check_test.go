// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"testing"
)

func TestCheckOrJoinReusesAnActiveCheck(t *testing.T) {
	u := &Updater{busy: true, checking: true, state: UpdateState{Status: "checking"}}
	if !u.CheckOrJoin() {
		t.Fatal("active background check should be accepted by the admin endpoint")
	}
	if u.CheckAsync(false) {
		t.Fatal("a second check must not start while the first check is active")
	}
}

func TestCheckOrJoinRejectsAnActiveInstall(t *testing.T) {
	u := &Updater{busy: true, state: UpdateState{Status: "downloading"}}
	if u.CheckOrJoin() {
		t.Fatal("manual check must not join an active installation")
	}
}
