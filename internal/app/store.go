// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrConflict = errors.New("配置已被其他页面修改，请重新读取后再保存")

// Codex 修改：保留旧菜单入口并避免其触发词与自定义配置冲突。
type Store struct {
	db   *sql.DB
	mu   sync.Mutex
	Path string
}

func OpenStore(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("插件数据目录必须为绝对路径")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	schema := `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS service_settings(id INTEGER PRIMARY KEY CHECK(id=1),data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS global_settings(id INTEGER PRIMARY KEY CHECK(id=1),data TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS account_state(account TEXT PRIMARY KEY,data TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS account_identity(selector TEXT PRIMARY KEY,account TEXT NOT NULL UNIQUE);
 CREATE TABLE IF NOT EXISTS seen(account TEXT NOT NULL,id TEXT NOT NULL,created INTEGER NOT NULL,PRIMARY KEY(account,id));
 CREATE TABLE IF NOT EXISTS messages(account TEXT NOT NULL,id INTEGER NOT NULL,seq INTEGER NOT NULL,group_id INTEGER NOT NULL,data TEXT NOT NULL,created INTEGER NOT NULL,PRIMARY KEY(account,id));
 CREATE INDEX IF NOT EXISTS messages_seq ON messages(account,group_id,seq);
 CREATE TABLE IF NOT EXISTS activity(account TEXT NOT NULL,user_id INTEGER NOT NULL,last_date TEXT NOT NULL,days INTEGER NOT NULL,notified TEXT NOT NULL DEFAULT '',PRIMARY KEY(account,user_id));
 CREATE TABLE IF NOT EXISTS monitor(id INTEGER PRIMARY KEY,account TEXT NOT NULL,kind TEXT NOT NULL,body TEXT NOT NULL,created INTEGER NOT NULL);`
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db, Path: path}
	initial, _ := json.Marshal(effectiveGlobal(GlobalConfig{}))
	if _, err = db.Exec("INSERT OR IGNORE INTO global_settings(id,data,revision) VALUES(1,?,0)", string(initial)); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

type GlobalConfig struct {
	Owner            int64             `json:"owner"`
	DisabledAccounts []string          `json:"disabled_accounts"`
	MenuContent      string            `json:"menu_content"`
	MenuPrefix       string            `json:"menu_prefix"`
	MenuSuffix       string            `json:"menu_suffix"`
	MenuAutomatic    bool              `json:"menu_automatic"`
	Menus            []CustomMenu      `json:"menus"`
	Replies          map[string]string `json:"replies"`
}

func effectiveGlobal(c GlobalConfig) GlobalConfig {
	if c.DisabledAccounts == nil {
		c.DisabledAccounts = []string{}
	}
	legacy := strings.NewReplacer("（本人及主人）", "", "（主人）", "", "所有 QQ 共用此菜单；普通“菜单”按当前 QQ 的设置生成。指令之间用空格分隔参数。", "所有 QQ 共用。用登录账号本人发送指令即可，无需逐项开通。指令与参数用空格分隔。").Replace(c.MenuContent)
	if legacy == buildGlobalMenu() || legacy == buildSharedCatalogue(false) || isLegacyAutomaticMenu(legacy) {
		c.MenuAutomatic = true
	}

	if strings.TrimSpace(c.MenuContent) == "" || c.MenuAutomatic {
		c.MenuContent = buildGlobalMenu()
		c.MenuAutomatic = true
	}
	if c.Menus == nil {
		c.Menus = []CustomMenu{}
	}
	if c.Replies == nil {
		c.Replies = map[string]string{}
	}
	return c
}

func isLegacyAutomaticMenu(menu string) bool {
	lines := strings.Split(strings.TrimSpace(menu), "\n")
	if len(lines) < 32 || strings.TrimSpace(lines[0]) != "【Catherina 总菜单】" {
		return false
	}
	if strings.TrimSpace(lines[1]) != "• 菜单" || strings.TrimSpace(lines[2]) != "• 总菜单" {
		return false
	}

	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "• ") || strings.Contains(line, "\t") {
			return false
		}
	}
	return true
}

type GlobalState struct {
	Revision int64        `json:"revision"`
	Config   GlobalConfig `json:"config"`
}

func (s *Store) Global() (GlobalState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := GlobalState{}
	var raw string
	err := s.db.QueryRow("SELECT data,revision FROM global_settings WHERE id=1").Scan(&raw, &out.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		out.Config = effectiveGlobal(out.Config)
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(raw), &out.Config)
	out.Config = effectiveGlobal(out.Config)
	return out, err
}
func (s *Store) PatchGlobal(revision int64, patch map[string]json.RawMessage) (GlobalState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := GlobalState{}
	var raw string
	err := s.db.QueryRow("SELECT data,revision FROM global_settings WHERE id=1").Scan(&raw, &out.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		out.Config = GlobalConfig{}
	} else if err != nil {
		return out, err
	} else if err = json.Unmarshal([]byte(raw), &out.Config); err != nil {
		return out, err
	}
	if revision != out.Revision {
		return out, ErrConflict
	}
	out.Config = effectiveGlobal(out.Config)
	allRaw, _ := json.Marshal(out.Config)
	var all map[string]json.RawMessage
	_ = json.Unmarshal(allRaw, &all)
	for k, v := range patch {
		if _, ok := all[k]; !ok {
			return out, fmt.Errorf("不认识的全局配置字段 %s", k)
		}
		all[k] = v
	}
	allRaw, _ = json.Marshal(all)
	if err = json.Unmarshal(allRaw, &out.Config); err != nil {
		return out, err
	}
	if _, changesContent := patch["menu_content"]; changesContent {
		out.Config.MenuAutomatic = strings.TrimSpace(out.Config.MenuContent) == "" || out.Config.MenuContent == buildGlobalMenu()
	}
	out.Config = effectiveGlobal(out.Config)
	if out.Config.Owner < 0 || out.Config.Owner > 999999999999999 {
		return out, errors.New("主人 QQ 格式不正确")
	}
	seenAccounts := map[string]bool{}
	for _, key := range out.Config.DisabledAccounts {
		parts := strings.Split(key, ":")
		if !accountPattern.MatchString(key) {
			return out, errors.New("停用账号格式不正确")
		}
		id, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil || id <= 0 || seenAccounts[key] {
			return out, errors.New("停用账号列表包含无效或重复账号")
		}
		seenAccounts[key] = true
	}
	if len(out.Config.MenuContent) > 131072 || len(out.Config.MenuPrefix) > 131072 || len(out.Config.MenuSuffix) > 131072 {
		return out, errors.New("总菜单内容过长")
	}
	check := defaults()
	check.Menus, check.Replies = out.Config.Menus, out.Config.Replies
	if err = Validate(check); err != nil {
		return out, err
	}
	allRaw, _ = json.Marshal(out.Config)
	out.Revision++
	_, err = s.db.Exec("INSERT INTO global_settings(id,data,revision) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data,revision=excluded.revision", string(allRaw), out.Revision)
	return out, err
}
func (s *Store) Close() error { return s.db.Close() }

// Codex 修改：将 QQ、平台和 protocol_id 映射到持久化账号键，保留升级前的单账号配置。
func (s *Store) AssignAccountKeys(accounts []Account) ([]Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	type candidate struct {
		account  Account
		selector string
		base     string
	}
	items := make([]candidate, 0, len(accounts))
	counts := map[string]int{}
	selectorCounts := map[string]int{}
	for _, account := range accounts {
		if account.SelfID <= 0 || (account.ClientType != "android" && account.ClientType != "linuxqq") || account.ProtocolID < 0 {
			continue
		}
		base := fmt.Sprintf("%d:%s", account.SelfID, account.ClientType)
		selector := fmt.Sprintf("%s:%d", base, account.ProtocolID)
		items = append(items, candidate{account: account, selector: selector, base: base})
		counts[base]++
		selectorCounts[selector]++
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].base != items[j].base {
			return items[i].base < items[j].base
		}
		return items[i].account.ProtocolID < items[j].account.ProtocolID
	})

	rows, err := s.db.Query("SELECT selector,account FROM account_identity")
	if err != nil {
		return nil, err
	}
	known, used := map[string]string{}, map[string]bool{}
	for rows.Next() {
		var selector, key string
		if err = rows.Scan(&selector, &key); err != nil {
			rows.Close()
			return nil, err
		}
		known[selector], used[key] = key, true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}

	assigned := make([]Account, 0, len(items))
	for _, item := range items {
		// 相同 selector 无法由 3.0 路由区分，丢弃歧义项，避免串用另一个账号的数据。
		if selectorCounts[item.selector] != 1 {
			continue
		}
		key, exists := known[item.selector]
		if !exists {
			key = item.base
			if counts[item.base] > 1 || used[key] {
				key = item.selector
			}
			if used[key] {
				return nil, fmt.Errorf("协议账号键冲突：%s", key)
			}
			if _, err = s.db.Exec("INSERT INTO account_identity(selector,account) VALUES(?,?)", item.selector, key); err != nil {
				return nil, err
			}
			known[item.selector], used[key] = key, true
		}
		item.account.AccountKey = key
		assigned = append(assigned, item.account)
	}
	return assigned, nil
}

func (s *Store) load(key string) (State, error) {
	out := State{Config: defaults()}
	var raw string
	err := s.db.QueryRow("SELECT data,revision FROM account_state WHERE account=?", key).Scan(&raw, &out.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(raw), &out.Config)
	if out.Config.MassFriends == nil {
		out.Config.MassFriends = map[string]bool{}
	}
	if out.Config.MassExcluded == nil {
		out.Config.MassExcluded = map[string]bool{}
	}
	if out.Config.MassFriendsExcluded == nil {
		out.Config.MassFriendsExcluded = map[string]bool{}
	}
	if out.Config.SignGroups == nil {
		out.Config.SignGroups = map[string]bool{}
	}
	if out.Config.AnswerRules == nil {
		out.Config.AnswerRules = []AnswerRule{}
	}
	if out.Config.SignTime == "" {
		out.Config.SignTime = "00:00"
	}
	return out, err
}
func (s *Store) Get(key string) (State, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.load(key) }
func (s *Store) Update(key string, revision int64, fn func(*Config) error) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.load(key)
	if err != nil {
		return v, err
	}
	if revision >= 0 && revision != v.Revision {
		return v, ErrConflict
	}
	if err = fn(&v.Config); err != nil {
		return v, err
	}
	if err = Validate(v.Config); err != nil {
		return v, err
	}
	raw, err := json.Marshal(v.Config)
	if err != nil {
		return v, err
	}
	v.Revision++
	_, err = s.db.Exec("INSERT INTO account_state(account,data,revision) VALUES(?,?,?) ON CONFLICT(account) DO UPDATE SET data=excluded.data,revision=excluded.revision", key, string(raw), v.Revision)
	return v, err
}
func (s *Store) Patch(key string, revision int64, patch map[string]json.RawMessage) (State, error) {
	return s.Update(key, revision, func(c *Config) error {
		raw, _ := json.Marshal(c)
		var all map[string]json.RawMessage
		_ = json.Unmarshal(raw, &all)
		for k, v := range patch {
			if _, ok := all[k]; !ok {
				return fmt.Errorf("不认识的配置字段 %s", k)
			}
			all[k] = v
		}
		raw, _ = json.Marshal(all)
		return json.Unmarshal(raw, c)
	})
}
func (s *Store) Seen(key, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	r, err := s.db.Exec("INSERT OR IGNORE INTO seen(account,id,created) VALUES(?,?,?)", key, id, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 0, err
}
func (s *Store) Cache(e Event) error {
	if e.MessageID <= 0 {
		e.MessageID = number(e.Body["message_id"])
	}
	if e.MessageID <= 0 {
		return nil
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	seq := e.MessageSeq
	if seq == 0 {
		seq = e.MsgSeq
	}
	if seq == 0 {
		seq = number(e.Body["msg_seq"])
	}
	if seq == 0 {
		seq = number(e.Body["message_seq"])
	}
	_, err = s.db.Exec("INSERT OR REPLACE INTO messages(account,id,seq,group_id,data,created) VALUES(?,?,?,?,?,?)", e.Account().Key(), e.MessageID, seq, e.GroupID, string(raw), time.Now().Unix())
	return err
}
func (s *Store) Recalled(e Event) (Event, error) {
	var raw string
	ids := []int64{number(e.Body["message_id"]), e.MessageID, number(e.Target["message_id"]), number(e.Body["id"])}
	seqs := []int64{e.MessageSeq, e.MsgSeq, number(e.Body["message_seq"]), number(e.Body["msg_seq"]), number(e.Target["message_seq"]), number(e.Target["msg_seq"])}
	err := sql.ErrNoRows
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		err = s.db.QueryRow("SELECT data FROM messages WHERE account=? AND group_id=? AND id=? ORDER BY created DESC LIMIT 1", e.Account().Key(), e.GroupID, id).Scan(&raw)
		if err == nil {
			break
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Event{}, err
		}
	}
	if errors.Is(err, sql.ErrNoRows) || err == nil {
		for _, seq := range seqs {
			if seq <= 0 {
				continue
			}
			err = s.db.QueryRow("SELECT data FROM messages WHERE account=? AND group_id=? AND seq=? ORDER BY created DESC LIMIT 1", e.Account().Key(), e.GroupID, seq).Scan(&raw)
			if err == nil {
				break
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return Event{}, err
			}
		}
	}
	if err != nil {
		return Event{}, err
	}
	var m Event
	if err != nil {
		return m, err
	}
	err = json.Unmarshal([]byte(raw), &m)
	return m, err
}
func (s *Store) Log(key, kind, body string) error {
	if len(body) > 8192 {
		body = string([]rune(body)[:min(len([]rune(body)), 2048)])
	}
	_, err := s.db.Exec("INSERT INTO monitor(account,kind,body,created) VALUES(?,?,?,?)", key, kind, body, time.Now().Unix())
	return err
}

type MonitorRow struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Body    string `json:"body"`
	Created int64  `json:"created"`
}

func (s *Store) Monitor(key string, limit int) ([]MonitorRow, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query("SELECT id,kind,body,created FROM monitor WHERE account=? ORDER BY id DESC LIMIT ?", key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonitorRow{}
	for rows.Next() {
		var row MonitorRow
		if err := rows.Scan(&row.ID, &row.Kind, &row.Body, &row.Created); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ClearMonitor(key string) error {
	_, err := s.db.Exec("DELETE FROM monitor WHERE account=?", key)
	return err
}
func (s *Store) Touch(e Event) error {
	if e.GroupID != 0 || e.Author() == e.SelfID || e.Author() <= 0 {
		return nil
	}
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	_, err := s.db.Exec(`INSERT INTO activity(account,user_id,last_date,days) VALUES(?,?,?,1)
 ON CONFLICT(account,user_id) DO UPDATE SET days=CASE WHEN last_date=excluded.last_date THEN days WHEN last_date=? THEN days+1 ELSE 1 END,last_date=excluded.last_date`, e.Account().Key(), e.Author(), today, yesterday)
	return err
}
func (s *Store) Activity(key string, user int64) string {
	var last string
	var days int
	err := s.db.QueryRow("SELECT last_date,days FROM activity WHERE account=? AND user_id=?", key, user).Scan(&last, &days)
	if err != nil {
		return "还没有私聊互动记录"
	}
	return fmt.Sprintf("连续互动 %d 天，上次互动：%s", days, last)
}
func (s *Store) Reminders(key string) ([]int64, error) {
	rows, err := s.db.Query("SELECT user_id FROM activity WHERE account=? AND last_date=? AND notified<>? ORDER BY user_id LIMIT 500", key, time.Now().AddDate(0, 0, -1).Format("2006-01-02"), time.Now().Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Store) MarkReminded(key string, id int64) error {
	_, err := s.db.Exec("UPDATE activity SET notified=? WHERE account=? AND user_id=?", time.Now().Format("2006-01-02"), key, id)
	return err
}
func (s *Store) Prune() error {
	cutoff := time.Now().AddDate(0, 0, -7).Unix()
	for _, table := range []string{"seen", "messages", "monitor"} {
		if _, err := s.db.Exec("DELETE FROM "+table+" WHERE created<?", cutoff); err != nil {
			return err
		}
	}
	_, err := s.db.Exec("DELETE FROM monitor WHERE id NOT IN (SELECT id FROM monitor ORDER BY id DESC LIMIT 10000)")
	if err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM messages WHERE rowid IN (SELECT rowid FROM (SELECT rowid,row_number() OVER (PARTITION BY account ORDER BY created DESC) AS n FROM messages) WHERE n>4096)")
	return err
}

var timePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
var accountPattern = regexp.MustCompile(`^[1-9]\d{0,14}:(android|linuxqq)(:\d{1,19})?$`)

func Validate(c Config) error {
	if c.Owner < 0 || c.Owner > 999999999999999 {
		return errors.New("主人 QQ 格式不正确")
	}
	if c.Enabled == nil || c.GroupSwitch == nil || c.Aliases == nil || c.CommandSwitch == nil || c.Permits == nil || c.Replies == nil || c.QA == nil || c.Mass == nil || c.MassFriends == nil || c.MassExcluded == nil || c.MassFriendsExcluded == nil || c.SignGroups == nil {
		return errors.New("配置表不能为 null")
	}
	for id, rule := range c.Access {
		known := false
		for _, f := range Features {
			if f.ID == id {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("未知功能权限：%s", id)
		}
		if rule != "default" && rule != "owner" && rule != "everyone" && rule != "allowlist" {
			return fmt.Errorf("触发权限无效：%s", id)
		}
	}
	for _, ids := range c.Permits {
		for _, id := range ids {
			if id <= 0 || id > 999999999999999 {
				return errors.New("触发名单 QQ 号码无效")
			}
		}
	}
	if c.PollSeconds < 30 || c.PollSeconds > 3600 {
		return errors.New("空间检查间隔应为 30–3600 秒")
	}
	if c.Cooldown < 1 || c.Cooldown > 3600 {
		return errors.New("冷却时间应为 1–3600 秒")
	}
	if !timePattern.MatchString(c.XuhuoTime) {
		return errors.New("续火时间格式应为 HH:MM")
	}
	if !timePattern.MatchString(c.SignTime) {
		return errors.New("群打卡时间格式应为 HH:MM")
	}
	if len(c.Menus) > 2000 || len(c.QA) > 10000 || len(c.AnswerRules) > 10000 || len(c.Jobs) > 100 || len(c.Mass) > 5000 || len(c.MassFriends) > 5000 || len(c.MassExcluded) > 5000 || len(c.MassFriendsExcluded) > 5000 || len(c.SignGroups) > 5000 {
		return errors.New("配置数量过多")
	}
	answerRules := map[string]bool{}
	for _, rule := range c.AnswerRules {
		if strings.TrimSpace(rule.Question) == "" || strings.TrimSpace(rule.Answer) == "" || (rule.Match != "exact" && rule.Match != "fuzzy") || (rule.Scope != "group" && rule.Scope != "friend" && rule.Scope != "both") {
			return errors.New("问答规则内容无效")
		}
		key := rule.Match + "\x00" + rule.Scope + "\x00" + strings.ToLower(strings.TrimSpace(rule.Question))
		if answerRules[key] {
			return errors.New("相同范围和匹配方式的问答不能重复")
		}
		answerRules[key] = true
		if len(rule.Question) > 4096 || len(rule.Answer) > 131072 {
			return errors.New("问答内容过长")
		}
	}
	seen := map[string]bool{}
	for _, group := range append(append([]commandMenuGroup(nil), commandMenuGroups...), legacyCommandMenuGroups...) {
		seen[group.Trigger] = true
	}
	commandNames := map[string]bool{}
	for _, cmd := range Commands {
		commandNames[cmd.Word] = true
	}
	for _, feature := range Features {
		seen[commandTrigger(c, feature.Title+"菜单")] = true
		if !commandNames[feature.Title] {
			seen[feature.Title] = true
		}
	}
	for _, cmd := range Commands {
		alias := c.Aliases[cmd.Word]
		if alias == "" {
			alias = cmd.Word
		}
		if strings.TrimSpace(alias) != alias || len([]rune(alias)) > 100 || strings.ContainsAny(alias, "\r\n") {
			return errors.New("触发词不能有首尾空格或换行，最多 100 字")
		}
		if seen[alias] {
			return fmt.Errorf("触发词与已有指令或内置菜单重复：%s", alias)
		}
		seen[alias] = true
	}
	for id := range c.Enabled {
		known := false
		for _, f := range Features {
			if f.ID == id {
				known = true
			}
		}
		if !known {
			return fmt.Errorf("未知功能：%s", id)
		}
	}
	for _, m := range c.Menus {
		if strings.TrimSpace(m.Trigger) == "" || strings.ContainsAny(m.Trigger, "\r\n") || len([]rune(m.Trigger)) > 100 {
			return errors.New("菜单触发词无效")
		}
		if seen[m.Trigger] {
			return fmt.Errorf("菜单或指令触发词重复：%s", m.Trigger)
		}
		seen[m.Trigger] = true
		if len(m.Reply) > 131072 {
			return errors.New("菜单内容过长")
		}
	}
	ids := map[string]bool{}
	for _, j := range c.Jobs {
		if j.ID == "" || ids[j.ID] {
			return errors.New("定时任务 ID 为空或重复")
		}
		ids[j.ID] = true
		if j.Kind != "loop" && j.Kind != "timed" && j.Kind != "timedss" {
			return errors.New("不支持的定时任务类型")
		}
		if j.Kind != "timedss" && j.Target != "" && j.Target != "group" && j.Target != "friend" && j.Target != "both" {
			return errors.New("群发任务对象应为群、好友或两者")
		}
		if strings.TrimSpace(j.Text) == "" {
			return errors.New("任务内容不能为空")
		}
		if j.Kind == "loop" {
			if j.Interval < 5 || j.Interval > 86400 {
				return errors.New("群发间隔应为 5–86400 秒")
			}
		} else if !timePattern.MatchString(j.Time) {
			return errors.New("任务时间格式应为 HH:MM")
		}
	}
	for k := range c.Mass {
		n := number(k)
		if n <= 0 || n > 999999999999999 {
			return fmt.Errorf("群号无效：%s", k)
		}
	}
	for k := range c.MassExcluded {
		n := number(k)
		if n <= 0 || n > 999999999999999 {
			return fmt.Errorf("群发排除群号无效：%s", k)
		}
	}
	for k := range c.MassFriends {
		n := number(k)
		if n <= 0 || n > 999999999999999 {
			return fmt.Errorf("好友 QQ 无效：%s", k)
		}
	}
	for k := range c.MassFriendsExcluded {
		n := number(k)
		if n <= 0 || n > 999999999999999 {
			return fmt.Errorf("好友排除 QQ 无效：%s", k)
		}
	}
	for k := range c.SignGroups {
		n := number(k)
		if n <= 0 || n > 999999999999999 {
			return fmt.Errorf("打卡群号无效：%s", k)
		}
	}
	for _, list := range [][]int64{c.NotifyTargets, c.MiaoUsers} {
		for _, id := range list {
			if id <= 0 || id > 999999999999999 {
				return errors.New("QQ 号码无效")
			}
		}
	}
	return nil
}
