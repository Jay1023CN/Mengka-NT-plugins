// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"catherina/internal/distribution"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var PolicyPublicKey string

type Announcement struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Important bool   `json:"important"`
	Pinned    bool   `json:"pinned"`
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
}

type Policy struct {
	Revision      int64          `json:"revision"`
	Issued        int64          `json:"issued"`
	Expires       int64          `json:"expires"`
	Minimum       string         `json:"minimum"`
	Target        string         `json:"target"`
	Force         bool           `json:"force"`
	Paused        bool           `json:"paused"`
	Message       string         `json:"message"`
	Announcements []Announcement `json:"announcements"`
}

type PolicyView struct {
	Policy
	Blocked  bool   `json:"blocked"`
	Required bool   `json:"required"`
	Reason   string `json:"reason"`
	Error    string `json:"error"`
	Checked  string `json:"checked"`
}

type PolicyManager struct {
	mu        sync.Mutex
	policy    Policy
	digest    [32]byte
	path      string
	errorText string
	checked   string
}

func NewPolicyManager(dir string) *PolicyManager {
	p := &PolicyManager{path: filepath.Join(dir, "state-cache.dat")}
	if raw, err := os.ReadFile(p.path); err == nil {
		if err := p.accept(raw, time.Now(), false); err != nil {
			p.errorText = err.Error()
		}
	}
	return p
}

func verifyPolicy(raw []byte, now time.Time) (Policy, [32]byte, error) {
	var policy Policy
	raw, openErr := distribution.Open("policy", raw)
	if openErr != nil {
		return policy, [32]byte{}, openErr
	}
	var signed SignedRelease
	key, err := base64.StdEncoding.DecodeString(PolicyPublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return policy, [32]byte{}, errors.New("缺少策略验签公钥")
	}
	if json.Unmarshal(raw, &signed) != nil {
		return policy, [32]byte{}, errors.New("策略格式无效")
	}
	sig, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil || !ed25519.Verify(key, signed.Payload, sig) {
		return policy, [32]byte{}, errors.New("策略签名无效")
	}
	if json.Unmarshal(signed.Payload, &policy) != nil {
		return policy, [32]byte{}, errors.New("策略内容无效")
	}
	if policy.Revision <= 0 || policy.Issued <= 0 || policy.Issued > now.Add(5*time.Minute).Unix() || policy.Expires <= policy.Issued || policy.Expires-policy.Issued > 30*86400 {
		return policy, [32]byte{}, errors.New("策略时间范围无效")
	}
	if _, err := parseVersion(policy.Minimum); err != nil {
		return policy, [32]byte{}, err
	}
	if _, err := parseVersion(policy.Target); err != nil {
		return policy, [32]byte{}, err
	}
	if versionNewer(policy.Minimum, policy.Target) {
		return policy, [32]byte{}, errors.New("最低版本不能高于目标版本")
	}
	if len(policy.Announcements) > 20 || len(policy.Message) > 2000 {
		return policy, [32]byte{}, errors.New("策略内容过长")
	}
	ids := map[string]bool{}
	for _, a := range policy.Announcements {
		if a.ID == "" || ids[a.ID] || len(a.ID) > 128 || len(a.Title) > 300 || len(a.Body) > 20000 || a.Start < 0 || a.End < 0 || (a.End > 0 && a.End <= a.Start) {
			return policy, [32]byte{}, errors.New("公告内容或时间无效")
		}
		ids[a.ID] = true
	}
	return policy, sha256.Sum256(signed.Payload), nil
}

func (p *PolicyManager) accept(raw []byte, now time.Time, persist bool) error {
	policy, digest, err := verifyPolicy(raw, now)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if policy.Revision < p.policy.Revision || (policy.Revision == p.policy.Revision && digest != p.digest) {
		return errors.New("拒绝回退或重复编号的策略")
	}
	if persist {
		file, err := os.CreateTemp(filepath.Dir(p.path), ".policy-*")
		if err != nil {
			return err
		}
		name := file.Name()
		defer os.Remove(name)
		if err = file.Chmod(0600); err == nil {
			_, err = file.Write(raw)
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if err = os.Rename(name, p.path); err != nil {
			return err
		}
	}
	p.policy = policy
	p.digest = digest
	p.errorText = ""
	p.checked = now.Format(time.RFC3339)
	return nil
}

func (p *PolicyManager) View() PolicyView {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := PolicyView{Policy: p.policy, Error: p.errorText, Checked: p.checked}
	v.Required = versionNewer(p.policy.Minimum, Version) || (p.policy.Force && versionNewer(p.policy.Target, Version))
	switch {
	case p.policy.Revision == 0:
		v.Blocked = true
		v.Reason = "等待获取有效版本策略，机器人功能暂未启动"
	case time.Now().Unix() >= p.policy.Expires:
		v.Blocked = true
		v.Reason = "版本策略已过期，请联网后立即检查"
	case v.Required:
		v.Blocked = true
		v.Reason = "当前版本需要升级，机器人功能已暂停"
	}
	return v
}

func (p *PolicyManager) Allowed() bool { return !p.View().Blocked }

func (u *Updater) refreshPolicy() (PolicyView, error) {
	candidates := fetchFeedCandidates(u.ctx, "state.dat", 1024*1024)
	raw, err := selectNewestPolicy(candidates, time.Now())
	if err == nil {
		err = u.Policy.accept(raw, time.Now(), true)
	}
	if err != nil {
		u.Policy.mu.Lock()
		u.Policy.errorText = fmt.Sprintf("策略检查失败，沿用本地有效策略：%v", err)
		u.Policy.mu.Unlock()
	}
	return u.Policy.View(), err
}

func selectNewestPolicy(candidates []feedCandidate, now time.Time) ([]byte, error) {
	var newest []byte
	var newestRevision int64
	var failures []string
	for _, candidate := range candidates {
		policy, _, err := verifyPolicy(candidate.raw, now)
		if err != nil {
			failures = append(failures, candidate.base+": "+err.Error())
			continue
		}
		if newest == nil || policy.Revision > newestRevision {
			newest = candidate.raw
			newestRevision = policy.Revision
		}
	}
	if newest == nil {
		return nil, fmt.Errorf("所有更新源的版本策略都未通过校验：%s", strings.Join(failures, "；"))
	}
	return newest, nil
}
