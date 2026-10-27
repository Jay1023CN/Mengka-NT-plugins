// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const Version = "1.0.12"
const PluginID = "catherina"

type M = map[string]any
type Account struct {
	SelfID     int64  `json:"self_id"`
	ClientType string `json:"client_type"`
	Nickname   string `json:"nickname"`
	Status     int    `json:"status"`
	Enabled    bool   `json:"enabled"`
}

func (a Account) Key() string { return fmt.Sprintf("%d:%s", a.SelfID, a.ClientType) }

type Segment struct {
	Type string `json:"type"`
	Data M      `json:"data"`
}
type Event struct {
	EventType  string    `json:"event_type"`
	EventID    string    `json:"event_id"`
	Category   string    `json:"category"`
	PostType   string    `json:"post_type"`
	SelfID     int64     `json:"self_id"`
	ClientType string    `json:"client_type"`
	UserID     int64     `json:"user_id"`
	UserUID    string    `json:"user_uid"`
	GroupID    int64     `json:"group_id"`
	MessageID  int64     `json:"message_id"`
	MessageSeq int64     `json:"message_seq"`
	MsgSeq     int64     `json:"msg_seq"`
	RawMessage string    `json:"raw_message"`
	AltMessage string    `json:"alt_message"`
	Source     string    `json:"source"`
	Message    []Segment `json:"message"`
	Sender     struct {
		UserID   int64  `json:"user_id"`
		Nickname string `json:"nickname"`
		Card     string `json:"card"`
		Remark   string `json:"remark"`
	} `json:"sender"`
	Receiver struct {
		UserID int64 `json:"user_id"`
	} `json:"receiver"`
	Operator     M      `json:"operator"`
	Target       M      `json:"target"`
	Body         M      `json:"body"`
	Flag         string `json:"flag"`
	RequestID    int64  `json:"request_id"`
	RequestType  int64  `json:"request_type"`
	RequestExtra int64  `json:"request_extra"`
}

func (e Event) Account() Account { return Account{SelfID: e.SelfID, ClientType: e.ClientType} }
func (e Event) Author() int64 {
	if e.Sender.UserID > 0 {
		return e.Sender.UserID
	}
	if e.UserID > 0 {
		return e.UserID
	}
	return number(e.Operator["user_id"])
}

func (e Event) IsSelf() bool { return e.SelfID > 0 && e.Author() == e.SelfID }
func (e Event) Peer() int64 {
	if e.IsSelf() && e.Receiver.UserID > 0 {
		return e.Receiver.UserID
	}
	return e.Author()
}
func (e Event) Text() string {
	var b strings.Builder
	for _, s := range e.Message {
		if s.Type == "text" {
			b.WriteString(text(s.Data["text"]))
		}
	}
	if b.Len() > 0 {
		return b.String()
	}
	if e.RawMessage != "" {
		return e.RawMessage
	}
	return e.AltMessage
}

type Feature struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Command     string `json:"command"`
	Owner       bool   `json:"owner"`
	DefaultOn   bool   `json:"default_on"`
}

var Features = []Feature{
	{"qunfa", "群发", "分别向选中的群或好友发送消息", "群发群", true, true},
	{"loop", "循环群发", "按间隔向选中的群或好友发送", "循环群发群", true, true},
	{"timed", "定时群发", "每天在指定时间向选中的群或好友发送", "定时群发群", true, true},
	{"rand", "随机字母", "生成 1–1024 位随机大写字母", "随机字母", false, true},
	{"daifa", "代发", "以当前机器人账号发送好友或群消息", "代发", true, true},
	{"qiangmi", "抢米", "收到红包后查询并领取；结果私聊主人", "", true, false},
	{"monitor", "监控", "将消息和事件记入本账号的数据库记录", "", true, false},
	{"like", "名片点赞", "调用 QQ 名片点赞接口，返回真实回执", "点赞", false, true},
	{"at", "艾特回复", "群聊中被单独 @ 时回复，带冷却时间", "", false, false},
	{"antirec", "防撤回", "用本地收到的消息缓存还原撤回内容", "", true, false},
	{"antiflash", "防闪照", "提取闪照原图并保存到插件数据目录", "", true, false},
	{"auto", "自动处理", "同意好友申请、入群申请和邀请", "", true, false},
	{"xuhuo", "续火", "记录私聊互动天数，按设置时间提醒", "续火状态", false, false},
	{"daka", "群打卡", "在当前群或指定群执行打卡", "打卡", false, true},
	{"beizhu", "好友备注", "修改本账号通讯录中的好友备注", "备注", true, true},
	{"emoji", "表情回应", "给群消息添加指定表情，带冷却时间", "设置表情回应", true, false},
	{"qa", "问答", "管理问答库，精确命中后回复一次", "问答", true, true},
	{"pen", "随机回话", "从本账号配置的语料中随机回复", "随机回话", false, true},
	{"poke", "戳一戳回复", "被戳时回复，带冷却时间", "设置戳一戳回复", true, false},
	{"miao", "秒赞秒评", "定期检查好友新动态，去重后点赞及评论", "秒赞", true, false},
	{"diange", "点歌", "搜索网易云歌曲并发送签名音乐卡片", "点歌", false, true},
	{"touxian", "群头衔", "设置群成员的专属头衔", "头衔", true, true},
	{"assist", "群聊助理", "全体禁言、踢人、公告和成员查询", "群聊助理", true, true},
	{"persona", "个性签名", "保留当前昵称并修改 QQ 签名", "个性签名", true, true},
	{"charset", "字符工具", "字数统计、反转、全角与半角转换", "字符", false, true},
	{"speed", "等级加速", "读取任务面板，只执行可执行的免费任务", "加速", true, true},
	{"timedss", "定时说说", "每天在指定时间发布文本空间动态", "定时说说", true, true},
	{"menucfg", "菜单配置", "查看各项功能状态", "菜单配置", true, true},
	{"account", "账号状态", "查询当前账号实际在线状态", "账号状态", false, true},
	{"search", "查找功能", "按关键词搜索已实现功能和指令", "搜索命令", false, true},
	{"feedback", "问题反馈", "把反馈转给本账号主人", "反馈", false, true},
	{"custom_menu", "自定义菜单", "编辑真正回复的菜单内容与触发人员", "自定义菜单", true, true},
	{"menu_fix", "菜单前后缀", "多行编辑菜单模板，支持上下文变量", "菜单前缀", true, true},
	{"online_notify", "上线通知", "本账号上线时通知指定好友", "上线通知", true, true},
}

type CustomMenu struct {
	Trigger string  `json:"trigger"`
	Reply   string  `json:"reply"`
	Global  bool    `json:"global"`
	Permit  []int64 `json:"permit"`
}
type AnswerRule struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Match    string `json:"match"`
	Scope    string `json:"scope"`
}
type Job struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Target     string `json:"target"`
	Text       string `json:"text"`
	Time       string `json:"time"`
	Interval   int64  `json:"interval"`
	Next       int64  `json:"next"`
	Enabled    bool   `json:"enabled"`
	LastRun    int64  `json:"last_run,omitempty"`
	LastResult string `json:"last_result,omitempty"`
}
type Config struct {
	Owner               int64                      `json:"owner"`
	Enabled             map[string]bool            `json:"enabled"`
	GroupSwitch         map[string]map[string]bool `json:"group_switch"`
	Aliases             map[string]string          `json:"aliases"`
	CommandSwitch       map[string]bool            `json:"command_switch"`
	Permits             map[string][]int64         `json:"permits"`
	Access              map[string]string          `json:"access"`
	Replies             map[string]string          `json:"replies"`
	MenuContent         string                     `json:"menu_content"`
	MenuPrefix          string                     `json:"menu_prefix"`
	MenuSuffix          string                     `json:"menu_suffix"`
	Menus               []CustomMenu               `json:"menus"`
	QA                  map[string]string          `json:"qa"`
	AnswerRules         []AnswerRule               `json:"answer_rules"`
	Mass                map[string]bool            `json:"mass"`
	MassFriends         map[string]bool            `json:"mass_friends"`
	MassExcluded        map[string]bool            `json:"mass_excluded"`
	MassFriendsExcluded map[string]bool            `json:"mass_friends_excluded"`
	SignGroups          map[string]bool            `json:"sign_groups"`
	SignEnabled         bool                       `json:"sign_enabled"`
	SignTime            string                     `json:"sign_time"`
	SignLastRun         string                     `json:"sign_last_run"`
	SignLastResult      string                     `json:"sign_last_result"`
	Jobs                []Job                      `json:"jobs"`
	Notify              bool                       `json:"notify"`
	NotifyTargets       []int64                    `json:"notify_targets"`
	NotifyText          string                     `json:"notify_text"`
	AtText              string                     `json:"at_text"`
	PokeText            string                     `json:"poke_text"`
	Emoji               string                     `json:"emoji"`
	Cooldown            int                        `json:"cooldown"`
	PollSeconds         int                        `json:"poll_seconds"`
	Comments            []string                   `json:"comments"`
	Phrases             []string                   `json:"phrases"`
	MiaoUsers           []int64                    `json:"miao_users"`
	XuhuoTime           string                     `json:"xuhuo_time"`
	XuhuoText           string                     `json:"xuhuo_text"`
}
type State struct {
	Revision int64  `json:"revision"`
	Config   Config `json:"config"`
}

func defaults() Config {
	c := Config{Enabled: map[string]bool{}, GroupSwitch: map[string]map[string]bool{}, Aliases: map[string]string{}, CommandSwitch: map[string]bool{}, Permits: map[string][]int64{}, Access: map[string]string{}, Replies: map[string]string{}, Menus: []CustomMenu{}, QA: map[string]string{}, AnswerRules: []AnswerRule{}, Mass: map[string]bool{}, MassFriends: map[string]bool{}, MassExcluded: map[string]bool{}, MassFriendsExcluded: map[string]bool{}, SignGroups: map[string]bool{}, SignTime: "00:00", Jobs: []Job{}, NotifyTargets: []int64{}, Comments: []string{}, MiaoUsers: []int64{}, Phrases: []string{"收到，我看看。", "好，我记下了。", "明白，继续说。"}, NotifyText: "【插件名字】已上线，账号【框架QQ】。", AtText: "我在，有事请直接说。", PokeText: "收到，别戳啦。", Cooldown: 5, PollSeconds: 60, XuhuoTime: "20:00", XuhuoText: "今天也来聊两句吧。"}
	for _, f := range Features {
		c.Enabled[f.ID] = f.DefaultOn
	}
	return c
}
func (c Config) On(id string, gid int64) bool {
	on, ok := c.Enabled[id]
	if !ok {
		for _, f := range Features {
			if f.ID == id {
				on = f.DefaultOn
			}
		}
	}
	if gid > 0 {
		if g := c.GroupSwitch[strconv.FormatInt(gid, 10)]; g != nil {
			if v, yes := g[id]; yes {
				on = v
			}
		}
	}
	return on
}
func (c Config) IsOwner(e Event) bool {
	return e.IsSelf() || (c.Owner > 0 && e.Author() == c.Owner)
}
func (c Config) Allow(e Event, id string) bool {

	if e.IsSelf() {
		return true
	}
	if !c.On(id, e.GroupID) {
		return false
	}
	if c.IsOwner(e) {
		return true
	}
	switch c.Access[id] {
	case "owner":
		return false
	case "everyone":
		return true
	case "allowlist":
		return contains(c.Permits[id], e.Author())
	}
	if len(c.Permits[id]) > 0 {
		return contains(c.Permits[id], e.Author())
	}
	for _, f := range Features {
		if f.ID == id {
			return !f.Owner || f.ID == "custom_menu"
		}
	}
	return false
}
func contains(a []int64, n int64) bool {
	for _, v := range a {
		if v == n {
			return true
		}
	}
	return false
}
func number(v any) int64 {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return i
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}
func text(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func expand(raw string, e Event) string {
	group := ""
	if e.GroupID > 0 {
		group = strconv.FormatInt(e.GroupID, 10)
	}
	now := time.Now()
	return strings.NewReplacer("【换行符】", "\n", "【空格】", " ", "【发送者QQ】", strconv.FormatInt(e.Author(), 10), "【框架QQ】", strconv.FormatInt(e.SelfID, 10), "【群号】", group, "【插件名字】", "Catherina", "【插件版本】", Version, "【现行时间】", now.Format("2006-01-02 15:04:05"), "【10位时间戳】", strconv.FormatInt(now.Unix(), 10), "【13位时间戳】", strconv.FormatInt(now.UnixMilli(), 10)).Replace(raw)
}
