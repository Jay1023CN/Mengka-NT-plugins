// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.
// Codex 修改：补充自然问答、群发排除、群打卡与群管理指令；整理默认菜单排版。

package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Command struct {
	Word    string `json:"word"`
	Feature string `json:"feature"`
	Usage   string `json:"usage"`
}

var Commands = []Command{
	{"菜单", "custom_menu", "菜单"}, {"总菜单", "custom_menu", "总菜单"}, {"初始化配置", "menucfg", "初始化配置（生成默认全局菜单，不覆盖其他设置）"}, {"菜单配置", "menucfg", "菜单配置"}, {"搜索命令", "search", "搜索命令 关键词"}, {"开启", "menucfg", "开启 功能名称"}, {"关闭", "menucfg", "关闭 功能名称"}, {"设置主人", "menucfg", "设置主人 QQ（全局）"}, {"设置管理QQ", "menucfg", "设置管理QQ 12345678"},
	{"随机字母", "rand", "随机字母 [长度]"}, {"字符", "charset", "字符 统计/反转/全角/半角 内容"}, {"随机回话", "pen", "随机回话"}, {"喷狗", "pen", "喷狗（旧指令）"}, {"问答", "qa", "问答 添加 问题|答案；问答 删除 问题；问答 列表"}, {"添加精准问", "qa", "添加精准问 群聊/好友/合并 问题 答 回答"}, {"添加模糊问", "qa", "添加模糊问 群聊/好友/合并 关键词 答 回答"}, {"删除精准问", "qa", "删除精准问 群聊/好友/合并 问题"}, {"删除模糊问", "qa", "删除模糊问 群聊/好友/合并 关键词"}, {"反馈", "feedback", "反馈 内容"},
	{"自定义菜单", "custom_menu", "自定义菜单 添加 触发词|回复；自定义菜单 删除 触发词；自定义菜单 列表"}, {"菜单前缀", "menu_fix", "菜单前缀 内容（清空则输入 清空）"}, {"菜单后缀", "menu_fix", "菜单后缀 内容（清空则输入 清空）"}, {"上线通知", "online_notify", "上线通知 开/关；上线通知 添加/删除 QQ；上线通知 内容 文本"},
	{"群发", "qunfa", "群发（旧指令，请选择群聊或好友）"}, {"群发群", "qunfa", "群发群 内容；群发群 添加 群号；群发群 删除 群号；群发群 列表"}, {"群发好友", "qunfa", "群发好友 内容；群发好友 添加 QQ号；群发好友 删除 QQ号；群发好友 列表"}, {"开始群发群", "qunfa", "开始群发群 内容"}, {"添加群发群", "qunfa", "添加群发群 群号"}, {"删除群发群", "qunfa", "删除群发群 群号"}, {"群发群列表", "qunfa", "群发群列表"}, {"添加群发好友", "qunfa", "添加群发好友 QQ号"}, {"删除群发好友", "qunfa", "删除群发好友 QQ号"}, {"群发好友列表", "qunfa", "群发好友列表"}, {"循环群发", "loop", "循环群发（旧指令，请选择群聊或好友）"}, {"循环群发群", "loop", "循环群发群 10分钟 我马上到"}, {"循环群发好友", "loop", "循环群发好友 10分钟 晚安"}, {"停止循环群发", "loop", "停止循环群发"}, {"定时群发", "timed", "定时群发（旧指令，请选择群聊或好友）"}, {"定时群发群", "timed", "定时群发群 每天晚上8点发 晚安"}, {"定时群发好友", "timed", "定时群发好友 每天晚上8点发 晚安"}, {"取消定时群发", "timed", "取消定时群发"}, {"定时说说", "timedss", "定时说说 每天晚上8点 今天也要开心"}, {"取消定时说说", "timedss", "取消定时说说"}, {"自动任务状态", "timed", "自动任务状态"}, {"确认发送", "qunfa", "确认发送（确认刚才预览的群或好友）"}, {"取消发送", "qunfa", "取消发送（放弃尚未发送的预览）"},
	{"群发排除群", "qunfa", "群发排除群 添加/删除/列表 群号（名单中的群不会收到群发）"}, {"群发排除好友", "qunfa", "群发排除好友 添加/删除/列表 QQ号（名单中的好友不会收到群发）"},
	{"打卡", "daka", "打卡 [群号]"}, {"一键群打卡", "daka", "一键群打卡（打卡名单）"}, {"飞速打卡", "daka", "飞速打卡（打卡名单）"}, {"添加打卡群", "daka", "添加打卡群 群号"}, {"删除打卡群", "daka", "删除打卡群 群号"}, {"打卡群列表", "daka", "打卡群列表"}, {"定时群打卡", "daka", "定时群打卡 时间（默认 00:00）"}, {"取消定时群打卡", "daka", "取消定时群打卡"}, {"头衔", "touxian", "头衔 QQ 内容（清空则输入 清空）"}, {"点赞", "like", "点赞 [QQ] [次数]"}, {"代发", "daifa", "代发 好友/群 QQ或群号 内容"},
	{"全体禁言", "assist", "全体禁言（开启）/全体禁言 开/关"}, {"全体解禁", "assist", "全体解禁"}, {"禁言", "assist", "禁言 @成员 10分钟"}, {"解禁", "assist", "解禁 @成员"}, {"踢人", "assist", "踢人 QQ"}, {"发群公告", "assist", "发群公告 内容（可加 | 图片URL）"}, {"公告", "assist", "公告 内容（旧指令）"}, {"成员查询", "assist", "成员查询 @成员"}, {"群聊助理", "assist", "群聊助理"},
	{"账号状态", "account", "账号状态"}, {"备注", "beizhu", "备注 QQ 备注名（清空则输入 清空）"}, {"个性签名", "persona", "个性签名 内容（清空则输入 清空）"}, {"加速", "speed", "加速；加速 执行"}, {"点歌", "diange", "点歌 歌名"},
	{"设置艾特回复", "at", "设置艾特回复 内容"}, {"设置戳一戳回复", "poke", "设置戳一戳回复 内容"}, {"设置表情回应", "emoji", "设置表情回应 表情ID（0 为停用）"}, {"设置续火时间", "xuhuo", "设置续火时间 8:00（也支持 8.00、20：00）"}, {"续火状态", "xuhuo", "续火状态"}, {"秒赞", "miao", "秒赞 添加 评论；秒赞 删除 序号；秒赞 列表；秒赞 状态"},
}

func hiddenHelpCommand(word string) bool {
	switch word {
	case "设置主人", "群发", "开始群发群", "添加群发群", "删除群发群", "群发群列表", "添加群发好友", "删除群发好友", "群发好友列表", "循环群发", "定时群发", "群聊助理", "喷狗":
		return true
	default:
		return false
	}
}

func visibleCommands() []Command {
	visible := make([]Command, 0, len(Commands))
	for _, cmd := range Commands {
		if !hiddenHelpCommand(cmd.Word) {
			visible = append(visible, cmd)
		}
	}
	return visible
}

func (e *Engine) command(m Event, c Config) bool {
	input := commandText(m, c)
	if help, ok := commandCategoryHelp(c, m, input); ok {
		_ = e.reply(m, c.MenuPrefix+help+c.MenuSuffix)
		return true
	}

	for _, f := range Features {
		menu := f.Title + "菜单"
		if input != commandTrigger(c, menu) {
			continue
		}
		if !canUseFeature(c, m, f.ID) {
			e.replyCommand(m, f.Title+"当前不可用，请检查功能开关或触发权限。")
			return true
		}
		body := featureHelp(c, m, f.ID)
		if body != "" {
			_ = e.reply(m, c.MenuPrefix+body+c.MenuSuffix)
		}
		return true
	}
	for _, f := range Features {
		if input == f.Title && (f.Title != f.Command || c.Replies[f.ID] != "") && canUseFeature(c, m, f.ID) {
			reply := c.Replies[f.ID]
			if reply == "" {
				reply = featureHelp(c, m, f.ID)
				if reply == "" {
					reply = f.Title + "由事件自动触发，可在管理页设置。"
				}
			}
			_ = e.reply(m, c.MenuPrefix+reply+c.MenuSuffix)
			return true
		}
	}
	list := append([]Command(nil), Commands...)
	sort.SliceStable(list, func(i, j int) bool {
		a, b := c.Aliases[list[i].Word], c.Aliases[list[j].Word]
		if a == "" {
			a = list[i].Word
		}
		if b == "" {
			b = list[j].Word
		}
		return len(a) > len(b)
	})
	for _, cmd := range list {
		trigger := c.Aliases[cmd.Word]
		if trigger == "" {
			trigger = cmd.Word
		}
		if !strings.HasPrefix(input, trigger) {
			continue
		}
		if on, ok := c.CommandSwitch[cmd.Word]; ok && !on {
			return true
		}
		arg := strings.TrimSpace(strings.TrimPrefix(input, trigger))
		if arg != "" && (cmd.Word == "菜单" || cmd.Word == "菜单配置" || cmd.Word == "账号状态" || cmd.Word == "续火状态" || cmd.Word == "随机回话" || cmd.Word == "喷狗" || cmd.Word == "群聊助理" || cmd.Word == "全体解禁" || cmd.Word == "一键群打卡" || cmd.Word == "飞速打卡" || cmd.Word == "打卡群列表" || cmd.Word == "取消定时群打卡" || cmd.Word == "群发群列表" || cmd.Word == "群发好友列表" || cmd.Word == "停止循环群发" || cmd.Word == "取消定时群发" || cmd.Word == "取消定时说说" || cmd.Word == "自动任务状态" || (cmd.Word == "确认发送" || cmd.Word == "取消发送")) {
			e.replyCommand(m, cmd.Usage)
			return true
		}
		allowed := canUseFeature(c, m, cmd.Feature)

		if cmd.Word == "菜单" || cmd.Word == "总菜单" {
			allowed = true
		}
		if !allowed {
			if c.On(cmd.Feature, m.GroupID) {
				e.replyCommand(m, "你没有使用这条指令的权限。")
			}
			return true
		}

		if (strings.HasPrefix(cmd.Word, "设置") || cmd.Word == "开启" || cmd.Word == "关闭") && !c.IsOwner(m) {
			e.replyCommand(m, "这条配置指令由登录账号本人或额外管理 QQ 使用。")
			return true
		}
		result, err := e.execute(m, c, cmd, arg)
		if err != nil {
			e.replyCommand(m, "【操作未完成】\n"+err.Error())
		} else if result != "" {
			e.replyCommand(m, result)
		}
		return true
	}
	return false
}

func (e *Engine) replyCommand(m Event, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if !strings.HasPrefix(text, "【") {
		text = "【指令回复】\n" + text
	}
	_ = e.reply(m, text)
}
func (e *Engine) change(m Event, fn func(*Config) error) (string, error) {
	_, err := e.Store.Update(m.Account().Key(), -1, fn)
	if err != nil {
		return "", err
	}
	return "【已完成】\n设置已保存", nil
}
func (e *Engine) changeShared(fn func(*GlobalConfig) error) (string, error) {
	state, err := e.Store.Global()
	if err != nil {
		return "", err
	}
	if err = fn(&state.Config); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(state.Config)
	patch := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &patch)
	_, err = e.Store.PatchGlobal(state.Revision, patch)
	return "【已完成】\n设置已保存，所有 QQ 共用", err
}

func replyList(title string, items []string) string {
	if len(items) == 0 {
		return "【" + title + "】\n目前没有内容。"
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, "【"+title+"】")
	for _, item := range items {
		lines = append(lines, "• "+item)
	}
	return strings.Join(lines, "\n")
}
func newID() string {
	var b [12]byte
	_, err := rand.Read(b[:])
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func parts(s string) (string, string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeftFunc(s[i:], unicode.IsSpace)
}
func splitPair(s string) (string, string, bool) {
	a, b, ok := strings.Cut(s, "|")
	return strings.TrimSpace(a), strings.TrimSpace(b), ok && strings.TrimSpace(a) != "" && strings.TrimSpace(b) != ""
}
func parseAnswerScope(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case "群", "群聊":
		return "group", true
	case "好友", "私聊":
		return "friend", true
	case "合并", "通用", "群聊好友", "全部":
		return "both", true
	default:
		return "", false
	}
}
func answerScopeLabel(scope string) string {
	return map[string]string{"group": "群聊", "friend": "好友", "both": "群聊和好友"}[scope]
}
func parseAnswerRule(value, match string, groupMessage bool) (AnswerRule, error) {
	scope := "friend"
	if groupMessage {
		scope = "group"
	}
	first, rest := parts(value)
	if parsed, ok := parseAnswerScope(first); ok {
		scope = parsed
		value = rest
	}
	question, answer, ok := splitPair(value)
	if !ok {
		question, answer, ok = strings.TrimSpace(value), "", false
		if separator := strings.LastIndex(question, "答"); separator > 0 {
			answer = strings.TrimSpace(question[separator+len("答"):])
			question = strings.TrimSpace(question[:separator])
			ok = question != "" && answer != ""
		}
	}
	if !ok {
		return AnswerRule{}, errors.New("请按“添加精准问 群聊 你好答你好呀”填写；好友私聊中发送会默认添加到好友问答，也可写“合并”共用")
	}
	return AnswerRule{Question: question, Answer: answer, Match: match, Scope: scope}, nil
}
func positive(s string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(s, "@"), 10, 64)
	if err != nil || id <= 0 || id > 999999999999999 {
		return 0, errors.New("QQ 或群号格式不正确")
	}
	return id, nil
}

func commandText(m Event, c Config) string {
	input := strings.TrimSpace(m.Text())
	for _, word := range []string{"点赞", "头衔", "禁言", "解禁", "踢人", "成员查询", "备注"} {
		if !strings.HasPrefix(input, commandTrigger(c, word)) {
			continue
		}
		var out strings.Builder
		for _, segment := range m.Message {
			switch segment.Type {
			case "text":
				out.WriteString(text(segment.Data["text"]))
			case "at", "mention", "mention_user":
				id := number(segment.Data["uin"])
				if id <= 0 {
					id = number(segment.Data["user_id"])
				}
				if id <= 0 {
					id = number(segment.Data["qq"])
				}
				if id > 0 && !(id == m.SelfID && strings.TrimSpace(out.String()) == "") {
					prefix := strings.TrimRightFunc(out.String(), unicode.IsSpace)
					out.Reset()
					out.WriteString(prefix)
					fmt.Fprintf(&out, " %d", id)
				}
			}
		}
		if out.Len() > 0 {
			return strings.TrimSpace(out.String())
		}
	}
	return input
}
func clearWord(s string) string {
	if s == "清空" {
		return ""
	}
	return s
}
func (e *Engine) execute(m Event, c Config, cmd Command, arg string) (string, error) {
	if arg == "" {
		switch cmd.Word {
		case "菜单", "总菜单", "初始化配置", "菜单配置", "账号状态", "随机字母", "随机回话", "喷狗", "续火状态", "群聊助理", "全体禁言", "全体解禁", "打卡", "一键群打卡", "飞速打卡", "打卡群列表", "定时群打卡", "取消定时群打卡", "加速", "点赞", "群发群列表", "群发好友列表", "停止循环群发", "取消定时群发", "取消定时说说", "自动任务状态", "确认发送", "取消发送":
		default:
			return strings.Join(visibleCommandUsages(c, cmd), "\n"), nil
		}
	}
	a := m.Account()
	action := ""
	p := M{}
	switch cmd.Word {
	case "菜单", "总菜单":
		global, err := e.Store.Global()
		if err != nil {
			return "", err
		}
		body := global.Config.MenuContent
		if body == "" {
			body = buildGlobalMenu()
		}
		return global.Config.MenuPrefix + body + global.Config.MenuSuffix, nil
	case "初始化配置":
		global, err := e.Store.Global()
		if err != nil {
			return "", err
		}
		if global.Config.MenuAutomatic {
			return "默认共用总菜单已自动生成。发送“菜单”或“总菜单”查看分类，再发送分类菜单名称查看指令。", nil
		}
		if strings.TrimSpace(global.Config.MenuContent) != "" {
			return "共用菜单已有自定义正文，未覆盖。可在菜单页查看或恢复默认。", nil
		}
		menuRaw, _ := json.Marshal(buildGlobalMenu())
		_, err = e.Store.PatchGlobal(global.Revision, map[string]json.RawMessage{"menu_content": menuRaw})
		if err != nil {
			return "", err
		}
		return "默认总菜单已初始化，所有 QQ 共用。发送“菜单”或“总菜单”查看分类，再发送分类菜单名称查看指令。", nil
	case "菜单配置":
		var lines []string
		for _, f := range Features {
			status := "关"
			if c.On(f.ID, m.GroupID) {
				status = "开"
			}
			lines = append(lines, f.Title+"："+status)
		}
		return replyList("自定义菜单", lines), nil
	case "搜索命令":
		return searchCommands(c, arg), nil
	case "自动任务状态":
		return taskStatus(c), nil
	case "开启", "关闭":
		return e.change(m, func(v *Config) error {
			for _, f := range Features {
				if f.Title == arg || f.ID == arg {
					if m.GroupID > 0 {
						k := strconv.FormatInt(m.GroupID, 10)
						if v.GroupSwitch[k] == nil {
							v.GroupSwitch[k] = map[string]bool{}
						}
						v.GroupSwitch[k][f.ID] = cmd.Word == "开启"
					} else {
						v.Enabled[f.ID] = cmd.Word == "开启"
					}
					return nil
				}
			}
			return errors.New("没有这个功能，请输入完整功能名称")
		})
	case "设置主人", "设置管理QQ":
		id, err := positive(arg)
		if err != nil {
			return "", err
		}
		global, err := e.Store.Global()
		if err != nil {
			return "", err
		}
		ownerRaw, _ := json.Marshal(id)
		if _, err = e.Store.PatchGlobal(global.Revision, map[string]json.RawMessage{"owner": ownerRaw}); err != nil {
			return "", err
		}
		return "额外管理 QQ 已更新，所有未设置单账号覆盖的 QQ 均已生效；各登录账号本人仍可直接使用。", nil
	case "随机字母":
		n := int64(8)
		if arg != "" {
			n = number(arg)
		}
		if n < 1 || n > 1024 {
			return "长度应为 1–1024", nil
		}
		out := make([]byte, n)
		for i := range out {
			x, err := rand.Int(rand.Reader, big.NewInt(26))
			if err != nil {
				return "", err
			}
			out[i] = 'A' + byte(x.Int64())
		}
		return string(out), nil
	case "字符":
		kind, body := parts(arg)
		r := []rune(body)
		switch kind {
		case "统计":
			return fmt.Sprintf("字符 %d 个，UTF-8 字节 %d 个", len(r), len(body)), nil
		case "反转":
			for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
				r[i], r[j] = r[j], r[i]
			}
		case "全角":
			for i, x := range r {
				if x == ' ' {
					r[i] = 0x3000
				} else if x >= 33 && x <= 126 {
					r[i] = x + 0xFEE0
				}
			}
		case "半角":
			for i, x := range r {
				if x == 0x3000 {
					r[i] = ' '
				} else if x >= 0xFF01 && x <= 0xFF5E {
					r[i] = x - 0xFEE0
				}
			}
		default:
			return cmd.Usage, nil
		}
		return string(r), nil
	case "随机回话", "喷狗":
		if len(c.Phrases) == 0 {
			return "还没有设置回话语料", nil
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(c.Phrases))))
		if err != nil {
			return "", err
		}
		return c.Phrases[n.Int64()], nil
	case "问答":
		kind, rest := parts(arg)
		switch kind {
		case "列表":
			keys := make([]string, 0, len(c.QA))
			for q := range c.QA {
				keys = append(keys, q)
			}
			sort.Strings(keys)
			for _, rule := range c.AnswerRules {
				match := "精准"
				if rule.Match == "fuzzy" {
					match = "模糊"
				}
				scope := answerScopeLabel(rule.Scope)
				keys = append(keys, fmt.Sprintf("%s / %s：%s → %s", scope, match, rule.Question, rule.Answer))
			}
			if len(keys) == 0 {
				return "问答库为空", nil
			}
			return replyList("问答规则", keys), nil
		case "添加":
			q, answer, ok := splitPair(rest)
			if !ok {
				return cmd.Usage, nil
			}
			return e.change(m, func(v *Config) error { v.QA[q] = answer; return nil })
		case "删除":
			return e.change(m, func(v *Config) error { delete(v.QA, rest); return nil })
		}
		return cmd.Usage, nil
	case "添加精准问", "添加模糊问":
		match := "exact"
		if cmd.Word == "添加模糊问" {
			match = "fuzzy"
		}
		rule, err := parseAnswerRule(arg, match, m.GroupID > 0)
		if err != nil {
			return "", err
		}
		return e.change(m, func(v *Config) error {
			for i := range v.AnswerRules {
				old := v.AnswerRules[i]
				if old.Match == rule.Match && old.Scope == rule.Scope && strings.EqualFold(strings.TrimSpace(old.Question), strings.TrimSpace(rule.Question)) {
					v.AnswerRules[i] = rule
					return nil
				}
			}
			v.AnswerRules = append(v.AnswerRules, rule)
			return nil
		})
	case "删除精准问", "删除模糊问":
		match := "exact"
		if cmd.Word == "删除模糊问" {
			match = "fuzzy"
		}
		scopeText, question := parts(arg)
		scope, ok := parseAnswerScope(scopeText)
		if !ok || strings.TrimSpace(question) == "" {
			return cmd.Usage, nil
		}
		removed := false
		_, err := e.Store.Update(m.Account().Key(), -1, func(v *Config) error {
			for i := range v.AnswerRules {
				rule := v.AnswerRules[i]
				if rule.Match == match && rule.Scope == scope && strings.EqualFold(strings.TrimSpace(rule.Question), strings.TrimSpace(question)) {
					v.AnswerRules = append(v.AnswerRules[:i], v.AnswerRules[i+1:]...)
					removed = true
					break
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		if !removed {
			return "没有找到这条问答规则。", nil
		}
		return "【已完成】\n问答规则已删除。", nil
	case "反馈":
		owner := c.Owner
		if owner == 0 {
			owner = m.SelfID
		}
		err := e.send(a, 0, owner, []Segment{{Type: "text", Data: M{"text": fmt.Sprintf("Catherina 反馈\n来自 %d，群 %d\n%s", m.Author(), m.GroupID, arg)}}})
		if err != nil {
			return "", err
		}
		return "反馈已发送", nil
	case "自定义菜单":
		kind, rest := parts(arg)
		switch kind {
		case "列表":
			var lines []string
			for _, v := range c.Menus {
				lines = append(lines, v.Trigger)
			}
			if len(lines) == 0 {
				return "自定义菜单为空", nil
			}
			return replyList("功能开关", lines), nil
		case "添加":
			trigger, body, ok := splitPair(rest)
			if !ok {
				return cmd.Usage, nil
			}
			return e.changeShared(func(v *GlobalConfig) error {
				for i := range v.Menus {
					if v.Menus[i].Trigger == trigger {
						v.Menus[i].Reply = body
						return nil
					}
				}
				v.Menus = append(v.Menus, CustomMenu{Trigger: trigger, Reply: body, Global: true, Permit: []int64{}})
				return nil
			})
		case "删除":
			return e.changeShared(func(v *GlobalConfig) error {
				for i, menu := range v.Menus {
					if menu.Trigger == rest {
						v.Menus = append(v.Menus[:i], v.Menus[i+1:]...)
						break
					}
				}
				return nil
			})
		}
		return cmd.Usage, nil
	case "菜单前缀", "菜单后缀":
		return e.changeShared(func(v *GlobalConfig) error {
			if cmd.Word == "菜单前缀" {
				v.MenuPrefix = clearWord(arg)
			} else {
				v.MenuSuffix = clearWord(arg)
			}
			return nil
		})
	case "上线通知":
		kind, rest := parts(arg)
		return e.change(m, func(v *Config) error {
			switch kind {
			case "开":
				v.Notify = true
			case "关":
				v.Notify = false
			case "内容":
				v.NotifyText = rest
			case "添加", "删除":
				id, err := positive(rest)
				if err != nil {
					return err
				}
				if kind == "添加" && !contains(v.NotifyTargets, id) {
					v.NotifyTargets = append(v.NotifyTargets, id)
				} else if kind == "删除" {
					for i, n := range v.NotifyTargets {
						if n == id {
							v.NotifyTargets = append(v.NotifyTargets[:i], v.NotifyTargets[i+1:]...)
							break
						}
					}
				}
			default:
				return errors.New(cmd.Usage)
			}
			return nil
		})
	case "确认发送":
		return e.confirmMass(m)
	case "取消发送":
		return e.cancelMass(m), nil
	case "群发排除群", "群发排除好友":
		target := "group"
		title := "群发排除群名单"
		if cmd.Word == "群发排除好友" {
			target, title = "friend", "群发排除好友名单"
		}
		kind, value := parts(arg)
		if kind == "列表" {
			list := c.MassExcluded
			if target == "friend" {
				list = c.MassFriendsExcluded
			}
			ids := make([]string, 0, len(list))
			for id, excluded := range list {
				if excluded {
					ids = append(ids, id)
				}
			}
			sort.Strings(ids)
			if len(ids) == 0 {
				return "【" + title + "】\n名单为空，所有已选对象都会收到群发。", nil
			}
			return replyList(title+"（这些对象不会收到群发）", ids), nil
		}
		id, err := positive(value)
		if (kind != "添加" && kind != "删除") || err != nil {
			return cmd.Usage, nil
		}
		return e.change(m, func(v *Config) error {
			list := v.MassExcluded
			if target == "friend" {
				list = v.MassFriendsExcluded
			}
			key := strconv.FormatInt(id, 10)
			if kind == "添加" {
				list[key] = true
			} else {
				delete(list, key)
			}
			return nil
		})
	case "群发", "群发群", "群发好友", "开始群发群", "添加群发群", "删除群发群", "群发群列表", "添加群发好友", "删除群发好友", "群发好友列表":
		target := "group"
		if cmd.Word == "群发好友" || cmd.Word == "添加群发好友" || cmd.Word == "删除群发好友" || cmd.Word == "群发好友列表" {
			target = "friend"
		}
		addWord, deleteWord, listWord, targetTitle, idLabel := "添加群发群", "删除群发群", "群发群列表", "群", "群号"
		if target == "friend" {
			addWord, deleteWord, listWord, targetTitle, idLabel = "添加群发好友", "删除群发好友", "群发好友列表", "好友", "QQ号"
		}
		if cmd.Word == addWord || cmd.Word == deleteWord {
			id, err := positive(arg)
			if err != nil {
				return "", fmt.Errorf("请填写有效的%s，例如：%s 123456", idLabel, addWord)
			}
			return e.change(m, func(v *Config) error {
				k := strconv.FormatInt(id, 10)
				if target == "friend" {
					if cmd.Word == addWord {
						v.MassFriends[k] = true
					} else {
						delete(v.MassFriends, k)
					}
				} else {
					if cmd.Word == addWord {
						v.Mass[k] = true
					} else {
						delete(v.Mass, k)
					}
				}
				return nil
			})
		}
		if cmd.Word == "群发" {
			return "请说清楚发到哪里：“群发群 今晚8点集合”发到群，“群发好友 晚安”私聊好友。旧的“群发”不会再猜发送对象。", nil
		}
		if cmd.Word == "群发群列表" || cmd.Word == "群发好友列表" {
			arg = "列表"
		} else if cmd.Word == "开始群发群" {
			target = "group"
		} else if cmd.Word == "群发" {

		} else if cmd.Word == "群发群" || cmd.Word == "群发好友" {
			kind, rest := parts(arg)
			if kind == "添加" {
				if !commandIsEnabled(c, addWord) {
					return "【操作未完成】\n这条指令已停用：" + addWord, nil
				}
				return e.execute(m, c, Command{Word: addWord, Feature: "qunfa", Usage: addWord + " " + idLabel}, rest)
			}
			if kind == "删除" {
				if !commandIsEnabled(c, deleteWord) {
					return "【操作未完成】\n这条指令已停用：" + deleteWord, nil
				}
				return e.execute(m, c, Command{Word: deleteWord, Feature: "qunfa", Usage: deleteWord + " " + idLabel}, rest)
			}
			if kind == "列表" {
				if !commandIsEnabled(c, listWord) {
					return "【操作未完成】\n这条指令已停用：" + listWord, nil
				}
				cmd.Word = listWord
				arg = "列表"
			} else {
				arg = strings.TrimSpace(arg)
			}
		}
		kind, _ := parts(arg)
		if cmd.Word == listWord || kind == "列表" {
			configured := c.Mass
			if target == "friend" {
				configured = c.MassFriends
			}
			keys := make([]string, 0, len(configured))
			for k, on := range configured {
				if on {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			if len(keys) == 0 {
				return "还没有选择要接收消息的" + targetTitle + "。可在管理页“问答与白名单”中添加。", nil
			}
			names := e.massNames(a, target)
			for i, id := range keys {
				if name := names[id]; name != "" {
					keys[i] = name + "（" + id + "）"
				}
			}
			return replyList(targetTitle+"发送名单", keys), nil
		}
		if kind == "添加" || kind == "删除" {
			return "请使用“" + addWord + " " + idLabel + "”或“" + deleteWord + " " + idLabel + "”。", nil
		}
		if strings.TrimSpace(arg) == "" {
			return cmd.Usage, nil
		}
		return e.prepareMass(m, arg, target)
	case "循环群发", "循环群发群", "循环群发好友", "定时群发", "定时群发群", "定时群发好友", "定时说说", "停止循环群发", "取消定时群发", "取消定时说说":
		kind := cmd.Feature
		if cmd.Word == "循环群发" || cmd.Word == "定时群发" {
			return "请明确发送对象：“" + cmd.Word + "群”发到已选择的群，“" + cmd.Word + "好友”私聊已选择的好友。", nil
		}
		if cmd.Word == "停止循环群发" || cmd.Word == "取消定时群发" || cmd.Word == "取消定时说说" || arg == "停止" || arg == "取消" {
			stopped := 0
			targetFilter := ""
			if strings.HasSuffix(cmd.Word, "好友") {
				targetFilter = "friend"
			}
			if strings.HasSuffix(cmd.Word, "群") {
				targetFilter = "group"
			}
			_, err := e.Store.Update(m.Account().Key(), -1, func(v *Config) error {
				for i := range v.Jobs {
					target := v.Jobs[i].Target
					if target == "" {
						target = "group"
					}
					if v.Jobs[i].Kind == kind && v.Jobs[i].Enabled && (targetFilter == "" || target == targetFilter) {
						v.Jobs[i].Enabled = false
						stopped++
					}
				}
				return nil
			})
			if err != nil {
				return "", err
			}
			if stopped == 0 {
				return "【没有进行中的任务】\n当前没有启用的" + map[string]string{"loop": "循环群发", "timed": "定时群发", "timedss": "定时说说"}[kind] + "任务。", nil
			}
			return fmt.Sprintf("【任务已暂停】\n已暂停 %d 个%s任务。需要恢复时可在网页“自动任务”重新开启。", stopped, map[string]string{"loop": "循环群发", "timed": "定时群发", "timedss": "定时说说"}[kind]), nil
		}
		value, body := parts(arg)
		if body == "" {
			return cmd.Usage, nil
		}
		target := "group"
		if cmd.Word == "循环群发好友" || cmd.Word == "定时群发好友" {
			target = "friend"
		}
		j := Job{ID: newID(), Kind: kind, Target: target, Text: body, Enabled: true}
		if kind == "loop" {
			interval, ok := parseIntervalSeconds(value)
			if !ok {
				return cmd.Usage, nil
			}
			j.Interval = interval
			j.Next = time.Now().Add(time.Duration(j.Interval) * time.Second).Unix()
		} else {
			clock, ok := parseScheduleClock(value)
			if !ok {
				return cmd.Usage, nil
			}
			j.Time = clock
			j.Next = nextDaily(clock, time.Now()).Unix()
		}
		if err := validateJobRecipients(c, j); err != nil {
			return "【任务未保存】\n" + err.Error(), nil
		}
		replaced := false
		_, err := e.Store.Update(m.Account().Key(), -1, func(v *Config) error {
			v.Enabled[kind] = true
			for i := range v.Jobs {
				if v.Jobs[i].Kind == kind && (v.Jobs[i].Target == target || (target == "group" && v.Jobs[i].Target == "")) {
					j.ID = v.Jobs[i].ID
					j.LastRun = 0
					j.LastResult = ""
					v.Jobs[i] = j
					replaced = true
					return nil
				}
			}
			v.Jobs = append(v.Jobs, j)
			return nil
		})
		if err != nil {
			return "", err
		}
		return scheduleAck(j, c, replaced), nil
	case "添加打卡群", "删除打卡群":
		id, err := positive(arg)
		if err != nil {
			return "", fmt.Errorf("请填写有效群号，例如：添加打卡群 123456")
		}
		return e.change(m, func(v *Config) error {
			key := strconv.FormatInt(id, 10)
			if cmd.Word == "添加打卡群" {
				v.SignGroups[key] = true
			} else {
				delete(v.SignGroups, key)
			}
			return nil
		})
	case "打卡群列表":
		ids := selectedSignIDs(c)
		lines := append([]string(nil), ids...)
		status := "定时打卡：已关闭"
		if c.SignEnabled {
			status = "定时打卡：每天 " + c.SignTime
		}
		lines = append([]string{status}, lines...)
		if c.SignLastRun != "" {
			lines = append(lines, "上次执行："+c.SignLastRun, "执行结果："+c.SignLastResult)
		}
		return replyList("群打卡名单", lines), nil
	case "定时群打卡":
		clock := "00:00"
		if strings.TrimSpace(arg) != "" {
			var ok bool
			clock, ok = parseScheduleClock(arg)
			if !ok {
				return "请填写时间，例如“定时群打卡 00:00”。", nil
			}
		}
		return e.change(m, func(v *Config) error {
			v.SignEnabled = true
			v.SignTime = clock
			return nil
		})
	case "取消定时群打卡":
		return e.change(m, func(v *Config) error {
			v.SignEnabled = false
			return nil
		})
	case "一键群打卡", "飞速打卡":
		ids := selectedSignIDs(c)
		if len(ids) == 0 {
			return "打卡群名单为空，请先发“添加打卡群 群号”。", nil
		}
		if !e.background(a.Key()+":manual-group-sign", func() {
			result := e.runGroupSigns(a, ids)
			e.replyCommand(m, result)
		}) {
			return "本账号正在执行群打卡，请等当前结果返回。", nil
		}
		return fmt.Sprintf("已开始为名单中的 %d 个群打卡，完成后会回复结果。", len(ids)), nil
	case "打卡":
		gid := m.GroupID
		if arg != "" {
			var err error
			gid, err = positive(arg)
			if err != nil {
				return "", err
			}
		}
		if gid <= 0 {
			return "请在群里使用，或填写群号", nil
		}
		action = "group_sign"
		p = M{"group_id": gid}
	case "点赞":
		who, count := parts(arg)
		id := m.Author()
		if who != "" {
			var err error
			id, err = positive(who)
			if err != nil {
				return "", err
			}
		}
		times := int64(1)
		if count != "" {
			times = number(count)
		}
		if times < 1 || times > 10 {
			return "点赞次数应为 1–10", nil
		}
		action = "like_summary_card"
		p = M{"target_uin": id, "like_count": times}
	case "代发":
		kind, rest := parts(arg)
		target, body := parts(rest)
		id, err := positive(target)
		if err != nil || body == "" {
			return cmd.Usage, nil
		}
		group := int64(0)
		user := id
		if kind == "群" {
			group = id
			user = 0
		} else if kind != "好友" {
			return cmd.Usage, nil
		}
		err = e.send(a, group, user, []Segment{{Type: "text", Data: M{"text": expand(body, m)}}})
		if err != nil {
			return "", err
		}
		return "消息已发送", nil
	case "头衔", "全体禁言", "全体解禁", "禁言", "解禁", "踢人", "公告", "发群公告", "成员查询":
		if m.GroupID <= 0 {
			return "请在群聊里使用这条指令", nil
		}
		p["group_id"] = m.GroupID
		switch cmd.Word {
		case "全体禁言", "全体解禁":
			if cmd.Word == "全体禁言" && arg == "" {
				arg = "开"
			}
			if cmd.Word == "全体解禁" {
				arg = "关"
			}
			if arg != "开" && arg != "关" {
				return cmd.Usage, nil
			}
			action = "set_group_mute_all"
			p["mute"] = arg == "开"
		case "公告", "发群公告":
			content, imageURL, hasImage := strings.Cut(arg, "|")
			content = strings.TrimSpace(content)
			if content == "" {
				return cmd.Usage, nil
			}
			action = "send_group_notice"
			p["content"] = content
			if hasImage && strings.TrimSpace(imageURL) != "" {
				p["image"] = strings.TrimSpace(imageURL)
			}
		default:
			who, rest := parts(arg)
			id, err := positive(who)
			if err != nil {
				return "", err
			}
			switch cmd.Word {
			case "头衔":
				if rest == "" {
					return cmd.Usage, nil
				}
				action = "set_group_special_title"
				p["user_id"] = id
				p["title"] = clearWord(rest)
			case "踢人":
				action = "kick_group_member"
				p["user_id"] = id
				p["reject_add_request"] = false
			case "禁言":
				sec, ok := parseMuteDuration(rest)
				if !ok {
					return "请输入禁言时长，例如“禁言 12345678 10分钟”。支持秒、分钟、小时、天，最多 30 天；填 0 解除禁言。", nil
				}
				action = "set_group_mute"
				p["target_uin"] = id
				p["duration_sec"] = sec
			case "解禁":
				action = "set_group_mute"
				p["target_uin"] = id
				p["duration_sec"] = 0
			case "成员查询":
				raw, err := e.call(a, "get_group_member_list", p)
				if err != nil {
					return "", err
				}
				var list struct {
					Members []M `json:"members"`
				}
				if json.Unmarshal(raw, &list) != nil {
					return "", errors.New("成员列表回包格式不正确")
				}
				for _, mem := range list.Members {
					if number(mem["uin"]) == id || number(mem["user_id"]) == id {
						return memberReply(m.GroupID, id, mem), nil
					}
				}
				return "成员列表中没有这个 QQ", nil
			}
		}
	case "群聊助理":
		return featureHelp(c, m, "assist"), nil
	case "账号状态":
		action = "get_bot_info"
	case "备注":
		who, remark := parts(arg)
		id, err := positive(who)
		if err != nil || remark == "" {
			return cmd.Usage, nil
		}
		action = "set_friend_remark"
		p = M{"user_id": id, "remark": clearWord(remark)}
	case "个性签名":
		raw, err := e.call(a, "get_summary_card", M{"target_uin": a.SelfID})
		if err != nil {
			return "", err
		}
		var card M
		if json.Unmarshal(raw, &card) != nil || text(card["nickname"]) == "" {
			return "", errors.New("没有取得当前昵称，未提交资料修改")
		}
		action = "set_qq_profile"
		p = M{"nickname": card["nickname"], "personal_note": clearWord(arg)}
	case "加速":
		if arg != "" && arg != "执行" {
			return cmd.Usage, nil
		}
		if !e.background(a.Key()+":level", func() {
			result, err := e.level(a, arg == "执行")
			if err != nil {
				result = err.Error()
			}
			e.replyCommand(m, result)
		}) {
			return "本账号已有等级任务或后台队列繁忙", nil
		}
		return "正在读取等级任务面板", nil
	case "点歌":
		if !e.background(a.Key()+":music", func() {
			if err := e.music(m, arg); err != nil {
				e.replyCommand(m, err.Error())
			}
		}) {
			return "点歌队列繁忙，请稍后再试", nil
		}
		return "", nil
	case "设置艾特回复", "设置戳一戳回复", "设置表情回应", "设置续火时间":
		return e.change(m, func(v *Config) error {
			switch cmd.Word {
			case "设置艾特回复":
				v.AtText = arg
				v.Enabled["at"] = true
			case "设置戳一戳回复":
				v.PokeText = arg
				v.Enabled["poke"] = true
			case "设置表情回应":
				if arg == "0" {
					v.Emoji = ""
					v.Enabled["emoji"] = false
				} else {
					if _, err := positive(arg); err != nil {
						return err
					}
					v.Emoji = arg
					v.Enabled["emoji"] = true
				}
			case "设置续火时间":
				clock, ok := parseScheduleClock(arg)
				if !ok {
					return errors.New("请填写提醒时间，例如“设置续火时间 晚上8点”")
				}
				v.XuhuoTime = clock
				v.Enabled["xuhuo"] = true
			}
			return nil
		})
	case "续火状态":
		return e.Store.Activity(a.Key(), m.Author()), nil
	case "秒赞":
		kind, rest := parts(arg)
		switch kind {
		case "添加":
			return e.change(m, func(v *Config) error {
				if rest == "" {
					return errors.New(cmd.Usage)
				}
				v.Comments = append(v.Comments, rest)
				return nil
			})
		case "删除":
			idx := int(number(rest)) - 1
			return e.change(m, func(v *Config) error {
				if idx < 0 || idx >= len(v.Comments) {
					return errors.New("评论序号不存在")
				}
				v.Comments = append(v.Comments[:idx], v.Comments[idx+1:]...)
				return nil
			})
		case "列表":
			var lines []string
			for i, v := range c.Comments {
				lines = append(lines, fmt.Sprintf("%d. %s", i+1, v))
			}
			if len(lines) == 0 {
				return "评论语料为空，只点赞", nil
			}
			return replyList("评论内容", lines), nil
		case "状态":
			return fmt.Sprintf("检查间隔 %d 秒，评论 %d 条；只处理启用后的新动态", c.PollSeconds, len(c.Comments)), nil
		}
		return cmd.Usage, nil
	default:
		return "", fmt.Errorf("没有实现的指令：%s", cmd.Word)
	}
	if action != "" {
		raw, err := e.call(a, action, p)
		if err != nil {
			return "", err
		}
		// Codex 修改：检查 3.0 群打卡回包中的业务 success 字段。
		if e.protocolV3.Load() && action == "group_sign" {
			if err := confirmedSuccess(raw); err != nil {
				return "", err
			}
		}
		result, replyErr := commandAPIReply(cmd.Word, p, raw)
		if replyErr != nil {
			return "", replyErr
		}
		if result != "" {
			return result, nil
		}
		if string(raw) == "null" || len(raw) == 0 {
			return "【已完成】\n接口已确认完成。", nil
		}
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "", "  ") == nil {
			raw = pretty.Bytes()
		}
		result = string(raw)
		runes := []rune(result)
		if len(runes) > 3000 {
			result = string(runes[:3000]) + "\n…内容较长，已截取前 3000 字；完整结果请在接口调试页查看。"
		}
		return "【接口结果】\n" + result, nil
	}
	return "", nil
}

func buildMenu(c Config, m Event) string {
	return buildCommandCatalogue(c, m, false)
}

func buildGlobalMenu() string { return buildSharedCatalogue(true) }
func buildSharedCatalogue(includeEvents bool) string {
	_ = includeEvents
	return buildCommandCatalogue(defaults(), Event{}, true)
}

type commandMenuGroup struct {
	Trigger  string
	Features []string
}

// Codex 修改：按功能拆分菜单入口，保留旧分类入口以兼容已有用法。
var commandMenuGroups = []commandMenuGroup{
	{"群发菜单", []string{"qunfa"}},
	{"定时任务", []string{"loop", "timed", "timedss"}},
	{"群管功能", []string{"assist"}},
	{"群打卡菜单", []string{"daka"}},
	{"群头衔菜单", []string{"touxian"}},
	{"问答功能", []string{"qa"}},
	{"艾特处理", []string{"at", "poke", "emoji"}},
	{"续火菜单", []string{"xuhuo"}},
	{"秒赞秒评菜单", []string{"miao"}},
	{"点歌菜单", []string{"diange"}},
	{"QQ工具", []string{"rand", "daifa", "like", "charset", "pen", "speed"}},
	{"账号功能", []string{"account", "beizhu", "persona"}},
	{"菜单设置", []string{"menucfg", "custom_menu", "menu_fix"}},
	{"其他功能", []string{"online_notify", "feedback", "search"}},
}

var legacyCommandMenuGroups = []commandMenuGroup{
	{"群打卡菜单", []string{"daka"}},
	{"群头衔菜单", []string{"touxian"}},
	{"秒赞秒评菜单", []string{"miao"}},
	{"QQ工具", []string{"rand", "daifa", "like", "charset", "pen", "speed"}},
	{"自动任务菜单", []string{"loop", "timed", "timedss"}},
	{"群聊管理菜单", []string{"assist", "touxian", "daka"}},
	{"消息互动菜单", []string{"at", "poke", "emoji", "xuhuo", "qa", "pen", "miao"}},
	{"QQ工具菜单", []string{"rand", "daifa", "like", "beizhu", "persona", "charset", "speed", "diange", "account"}},
	{"配置菜单", []string{"menucfg", "custom_menu", "menu_fix", "online_notify", "feedback", "search"}},
}

func buildCommandCatalogue(c Config, m Event, shared bool) string {
	lines := []string{"Catherina", "----"}
	var entries []string
	for _, group := range commandMenuGroups {
		if shared || commandGroupHasCommands(c, m, group) {
			entries = append(entries, group.Trigger)
		}
	}
	width := 0
	for i := 0; i < len(entries); i += 2 {
		if w := menuDisplayWidth(entries[i]); w > width {
			width = w
		}
	}
	for i := 0; i < len(entries); i += 2 {
		line := entries[i]
		if i+1 < len(entries) {
			padding := width - menuDisplayWidth(line) + 2
			line += strings.Repeat("　", padding/2) + strings.Repeat(" ", padding%2) + entries[i+1]
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// Codex 修改：按中文全角和英文半角计算目录列宽。
func menuDisplayWidth(value string) int {
	width := 0
	for _, r := range value {
		if unicode.Is(unicode.Han, r) || r == '　' {
			width += 2
		} else {
			width++
		}
	}
	return width
}

func commandGroupHasCommands(c Config, m Event, group commandMenuGroup) bool {
	for _, cmd := range Commands {
		if containsString(group.Features, cmd.Feature) && !hiddenHelpCommand(cmd.Word) && commandIsEnabled(c, cmd.Word) && canUseFeature(c, m, cmd.Feature) {
			return true
		}
	}
	return false
}

func commandCategoryHelp(c Config, m Event, input string) (string, bool) {
	for _, group := range append(append([]commandMenuGroup(nil), commandMenuGroups...), legacyCommandMenuGroups...) {
		if input != group.Trigger {
			continue
		}
		if len(group.Features) == 1 {
			body := featureHelp(c, m, group.Features[0])
			if strings.TrimSpace(c.Replies[group.Features[0]]) == "" {
				if _, rest, ok := strings.Cut(body, "\n----\n"); ok {
					body = group.Trigger + "\n----\n" + rest
				}
			}
			return body, true
		}
		var lines []string
		for _, id := range group.Features {
			if !canUseFeature(c, m, id) {
				continue
			}
			var usages []string
			for _, cmd := range Commands {
				if cmd.Feature == id && !hiddenHelpCommand(cmd.Word) && commandIsEnabled(c, cmd.Word) {
					usages = append(usages, visibleCommandUsages(c, cmd)...)
				}
			}
			if len(usages) == 0 {
				continue
			}
			if len(lines) > 0 {
				lines = append(lines, "-")
			}
			if reply := c.Replies[id]; reply != "" {
				lines = append(lines, reply)
			} else {
				lines = append(lines, usages...)
			}
		}
		if len(lines) == 0 {
			return "【" + group.Trigger + "】\n这类功能当前没有可用指令。", true
		}
		return group.Trigger + "\n----\n" + strings.Join(lines, "\n"), true
	}
	return "", false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func featureHelp(c Config, m Event, feature string) string {
	for _, f := range Features {
		if f.ID != feature {
			continue
		}
		if !canUseFeature(c, m, feature) {
			return f.Title + "当前不可用，请检查功能开关或触发权限。"
		}
		if reply := c.Replies[feature]; strings.TrimSpace(reply) != "" {
			return reply
		}
		var lines []string
		for _, cmd := range Commands {
			if cmd.Feature != feature || hiddenHelpCommand(cmd.Word) || !commandIsEnabled(c, cmd.Word) {
				continue
			}
			if feature == "qunfa" {
				switch cmd.Word {
				case "群发群":
					lines = append(lines, "发到群聊")
				case "群发好友":
					lines = append(lines, "\n好友私聊")
				case "确认发送":
					lines = append(lines, "\n确认与取消")
				case "群发排除群":
					lines = append(lines, "\n群发排除名单（名单中的对象不会收到群发）")
				}
			}
			lines = append(lines, visibleCommandUsages(c, cmd)...)
		}
		if len(lines) == 0 {
			return "【" + f.Title + "】\n" + f.Description + "。\n在“自动功能与开关”中启用后，收到对应事件时运行。"
		}
		note := ""
		if feature == "qunfa" {
			note = "\n\n先添加接收对象；排除名单中的群和好友不会收到群发。"
		}
		return f.Title + "菜单\n----\n" + strings.Join(lines, "\n") + note
	}
	return "暂无可用指令。"
}

func appendUsageLines(lines *[]string, usage string) {
	for _, item := range strings.Split(usage, "；") {
		item = strings.TrimSpace(item)
		if item != "" {
			*lines = append(*lines, "• "+item)
		}
	}
}

func visibleCommandUsages(c Config, cmd Command) []string {
	items := strings.Split(cmd.Usage, "；")
	if cmd.Word == "群发群" || cmd.Word == "群发好友" {
		addWord, deleteWord := "添加群发群", "删除群发群"
		listWord := "群发群列表"
		if cmd.Word == "群发好友" {
			addWord, deleteWord = "添加群发好友", "删除群发好友"
			listWord = "群发好友列表"
		}
		filtered := items[:0]
		for _, item := range items {
			if strings.Contains(item, " 添加 ") && !commandIsEnabled(c, addWord) {
				continue
			}
			if strings.Contains(item, " 删除 ") && !commandIsEnabled(c, deleteWord) {
				continue
			}
			if strings.HasSuffix(strings.TrimSpace(item), " 列表") && !commandIsEnabled(c, listWord) {
				continue
			}
			filtered = append(filtered, item)
		}
		items = filtered
	}
	var lines []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		item = menuUsageFormat(item)
		trigger := commandTrigger(c, cmd.Word)
		if trigger != cmd.Word {
			item = strings.ReplaceAll(item, cmd.Word, trigger)
		}
		lines = append(lines, item)
	}
	return lines
}

func canUseFeature(c Config, m Event, feature string) bool {
	return c.Allow(m, feature)
}

func commandIsEnabled(c Config, word string) bool {
	on, exists := c.CommandSwitch[word]
	return !exists || on
}

func searchCommands(c Config, query string) string {
	query = strings.ToLower(strings.TrimSpace(query))
	var lines []string
	for _, cmd := range Commands {
		if hiddenHelpCommand(cmd.Word) {
			continue
		}
		trigger := commandTrigger(c, cmd.Word)
		usages := visibleCommandUsages(c, cmd)
		usage := strings.Join(usages, "；")
		var feature Feature
		for _, f := range Features {
			if f.ID == cmd.Feature {
				feature = f
				break
			}
		}
		searchable := strings.ToLower(cmd.Word + " " + trigger + " " + usage + " " + feature.Title + " " + feature.Description)
		if !strings.Contains(searchable, query) {
			continue
		}
		status := ""
		if !commandIsEnabled(c, cmd.Word) {
			status = "（指令已停用）"
		} else if !c.On(cmd.Feature, 0) {
			status = "（功能已关闭）"
		}
		for _, item := range usages {
			lines = append(lines, item+"  —  "+feature.Title+status)
		}
	}
	if len(lines) == 0 {
		return "没有找到匹配的指令。试试搜索功能名称或用途关键词。"
	}
	return "【指令搜索结果】\n" + strings.Join(lines, "\n")
}

func parseScheduleClock(value string) (string, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "每天"))
	value = strings.TrimSuffix(value, "发送")
	value = strings.TrimSuffix(value, "发")
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("：", ":", "．", ".", "。", ".").Replace(value)
	if timePattern.MatchString(value) {
		return value, true
	}
	period := ""
	for _, candidate := range []string{"凌晨", "早上", "上午", "中午", "下午", "晚上"} {
		if strings.HasPrefix(value, candidate) {
			period = candidate
			value = strings.TrimSpace(strings.TrimPrefix(value, candidate))
			break
		}
	}
	if hourPart, minutePart, found := strings.Cut(value, ":"); found {
		hour, hourErr := strconv.Atoi(hourPart)
		minute, minuteErr := strconv.Atoi(minutePart)
		if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return "", false
		}
		hour = adjustClockHour(hour, period)
		return fmt.Sprintf("%02d:%02d", hour, minute), true
	}
	if hourPart, minutePart, found := strings.Cut(value, "."); found {
		if len(minutePart) != 2 {
			return "", false
		}
		hour, hourErr := strconv.Atoi(hourPart)
		minute, minuteErr := strconv.Atoi(minutePart)
		if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return "", false
		}
		hour = adjustClockHour(hour, period)
		return fmt.Sprintf("%02d:%02d", hour, minute), true
	}
	hourText, minuteText := value, "0"
	if hourText == "" {
		return "", false
	}
	if hourPart, minutePart, found := strings.Cut(hourText, "点"); found {
		hourText = strings.TrimSpace(hourPart)
		minutePart = strings.TrimSpace(minutePart)
		switch {
		case minutePart == "" || minutePart == "整":
			minuteText = "0"
		case minutePart == "半":
			minuteText = "30"
		default:
			minuteText = strings.TrimSuffix(minutePart, "分")
		}
	} else if strings.HasSuffix(hourText, "时") {
		hourText = strings.TrimSuffix(hourText, "时")
	} else if strings.ContainsAny(hourText, "点分") {
		return "", false
	}
	hour, err := strconv.Atoi(hourText)
	if err != nil || hour < 0 || hour > 24 {
		return "", false
	}
	minute, err := strconv.Atoi(minuteText)
	if err != nil || minute < 0 || minute > 59 || hour == 24 && minute != 0 {
		return "", false
	}
	hour = adjustClockHour(hour, period)
	if hour > 23 {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d", hour, minute), true
}

func adjustClockHour(hour int, period string) int {
	switch period {
	case "凌晨", "早上", "上午":
		if hour == 12 {
			return 0
		}
	case "中午", "下午", "晚上":
		if hour >= 1 && hour <= 11 {
			return hour + 12
		}
	}
	return hour
}

func parseIntervalSeconds(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "每隔")
	value = strings.TrimPrefix(value, "每")
	value = strings.TrimSuffix(value, "发送")
	value = strings.TrimSuffix(value, "发")
	value = strings.TrimSpace(value)
	unit := int64(1)
	for _, choice := range []struct {
		suffix string
		unit   int64
	}{{"小时", 3600}, {"分钟", 60}, {"秒", 1}, {"时", 3600}, {"分", 60}} {
		if strings.HasSuffix(value, choice.suffix) {
			value = strings.TrimSpace(strings.TrimSuffix(value, choice.suffix))
			unit = choice.unit
			break
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 || n > 86400/unit {
		return 0, false
	}
	seconds := n * unit
	return seconds, seconds >= 5 && seconds <= 86400
}

func commandTrigger(c Config, word string) string {
	if alias := strings.TrimSpace(c.Aliases[word]); alias != "" {
		return alias
	}
	return word
}

func hasCommandSwitch(c Config, word string) bool {
	_, ok := c.CommandSwitch[word]
	return ok
}
