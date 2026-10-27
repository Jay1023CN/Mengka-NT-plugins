// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/distribution"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func policyFixture(t *testing.T) (ed25519.PrivateKey, Policy) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	previousFeedKey := distribution.FeedKey
	distribution.FeedKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	t.Cleanup(func() { distribution.FeedKey = previousFeedKey })
	old := PolicyPublicKey
	PolicyPublicKey = base64.StdEncoding.EncodeToString(public)
	t.Cleanup(func() { PolicyPublicKey = old })
	now := time.Now().Unix()
	return private, Policy{Revision: 1, Issued: now, Expires: now + 7*86400, Minimum: Version, Target: Version, Announcements: []Announcement{{ID: "a", Title: "公告", Body: "正文\n<script>alert(1)</script>"}}}
}
func signPolicy(t *testing.T, key ed25519.PrivateKey, p Policy) []byte {
	t.Helper()
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(SignedRelease{Payload: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := distribution.Seal("policy", raw)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestSelectNewestPolicyAcrossStaleMirrors(t *testing.T) {
	private, policy := policyFixture(t)
	now := time.Now()
	old := policy
	old.Revision = 10
	policy.Revision = 11
	olderBytes := signPolicy(t, private, old)
	newerBytes := signPolicy(t, private, policy)
	got, err := selectNewestPolicy([]feedCandidate{
		{base: "https://cdn.example/", raw: olderBytes},
		{base: "https://raw.example/", raw: newerBytes},
	}, now)
	if err != nil || string(got) != string(newerBytes) {
		t.Fatalf("stale CDN should not override the newer signed policy, err=%v", err)
	}
}

func TestPolicyOfflineSignatureReplay(t *testing.T) {
	private, p := policyFixture(t)
	dir := t.TempDir()
	manager := NewPolicyManager(dir)
	if manager.Allowed() {
		t.Fatal("first boot without verified policy must pause")
	}
	raw := signPolicy(t, private, p)
	if err := manager.accept(raw, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	if !manager.Allowed() {
		t.Fatal(manager.View())
	}
	if !NewPolicyManager(dir).Allowed() {
		t.Fatal("offline restart did not retain valid policy")
	}

	p.Revision++
	p.Message = "第二版公告"
	if err := manager.accept(signPolicy(t, private, p), time.Now(), true); err != nil {
		t.Fatal(err)
	}
	if err := manager.accept(raw, time.Now(), true); err == nil {
		t.Fatal("accepted rollback")
	}
	p.Message = "same revision, different payload"
	if err := manager.accept(signPolicy(t, private, p), time.Now(), true); err == nil {
		t.Fatal("accepted revision collision")
	}
	bad := append([]byte(nil), raw...)
	bad[len(bad)-8] ^= 1
	if err := manager.accept(bad, time.Now(), true); err == nil {
		t.Fatal("accepted tampering")
	}
	if !manager.Allowed() {
		t.Fatal("bad remote response disabled valid cached policy")
	}
	if err := os.WriteFile(filepath.Join(dir, "state-cache.dat"), bad, 0600); err != nil {
		t.Fatal(err)
	}
	if NewPolicyManager(dir).Allowed() {
		t.Fatal("tampered disk cache accepted")
	}
}

func TestPolicyMinimumForcePauseExpiry(t *testing.T) {
	private, p := policyFixture(t)
	manager := NewPolicyManager(t.TempDir())
	p.Minimum = "4294967295.0.0"
	p.Target = p.Minimum
	if err := manager.accept(signPolicy(t, private, p), time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if !manager.View().Required || manager.Allowed() {
		t.Fatal("minimum version not enforced")
	}
	p.Revision++
	p.Minimum = Version
	p.Force = true
	if err := manager.accept(signPolicy(t, private, p), time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if !manager.View().Required {
		t.Fatal("force target not enforced")
	}
	p.Revision++
	p.Force = false
	p.Paused = true
	if err := manager.accept(signPolicy(t, private, p), time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if !manager.Allowed() {
		t.Fatal("pause downloads must allow supported current version")
	}
	p.Revision++
	p.Issued = time.Now().Add(-48 * time.Hour).Unix()
	p.Expires = time.Now().Add(-time.Hour).Unix()
	if err := manager.accept(signPolicy(t, private, p), time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if manager.Allowed() {
		t.Fatal("expired policy accepted for running")
	}
}

func TestVersionValidation(t *testing.T) {
	for _, v := range []string{"2027.x.1", "2026.10.15.1", "2026.-1.2", "2026.10.4294967296", "1.2.", "2027.1/../../x.1", "+2027.1.1"} {
		if versionNewer(v, Version) {
			t.Fatalf("accepted invalid version %q", v)
		}
	}
	if !versionNewer("2026.10.1501", "2026.10.15") {
		t.Fatal("revision version ordering")
	}
}

func TestEngineGate(t *testing.T) {
	_, _ = policyFixture(t)
	engine := NewEngine(nil, nil, t.TempDir())
	engine.Policy = NewPolicyManager(t.TempDir())
	if _, err := engine.call(Account{}, "send_private_msg", nil); err == nil {
		t.Fatal("paused engine made call")
	}
	engine.Submit(json.RawMessage(`{"self_id":1,"client_type":"android"}`))
	engine.schedule()
}

func TestFailedUpdateCooldown(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if err := updateCooldown(dir, "2026.10.1502", now); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(UpdateFailure{Version: "2026.10.1502", RetryAfter: now.Add(3 * time.Hour).Unix()})
	if err := os.WriteFile(filepath.Join(dir, "update-failure.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if updateCooldown(dir, "2026.10.1502", now) == nil {
		t.Fatal("failed build retried immediately")
	}
	if err := updateCooldown(dir, "2026.10.1503", now); err != nil {
		t.Fatal("fixed release blocked", err)
	}
	if err := updateCooldown(dir, "2026.10.1502", now.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
}
