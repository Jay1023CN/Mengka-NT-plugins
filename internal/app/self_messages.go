// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"fmt"
	"time"
)

func conversationKey(a Account, group, peer int64) string {
	if group > 0 {
		peer = 0
	}
	return fmt.Sprintf("%s:%d:%d", a.Key(), group, peer)
}

func (e *Engine) rememberOutputID(a Account, group, peer, id int64) {
	e.keepOutput(fmt.Sprintf("%s:id:%d", conversationKey(a, group, peer), id), 2*time.Minute)
}

func (e *Engine) keepOutput(key string, ttl time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.outgoing == nil {
		e.outgoing = map[string]time.Time{}
	}
	now := time.Now()
	for k, until := range e.outgoing {
		if !now.Before(until) {
			delete(e.outgoing, k)
		}
	}

	if len(e.outgoing) >= 8192 {
		var oldest string
		var first time.Time
		for k, until := range e.outgoing {
			if oldest == "" || until.Before(first) {
				oldest, first = k, until
			}
		}
		delete(e.outgoing, oldest)
	}
	e.outgoing[key] = now.Add(ttl)
}

func (e *Engine) isOutput(m Event) bool {
	key := conversationKey(m.Account(), m.GroupID, m.Peer())
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if m.MessageID > 0 && now.Before(e.outgoing[fmt.Sprintf("%s:id:%d", key, m.MessageID)]) {
		return true
	}
	return false
}
