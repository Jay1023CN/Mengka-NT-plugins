// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.
// Codex 修改：整理菜单指令格式和参数提示。

package app

import "strings"

func menuUsageFormat(usage string) string {
	replacements := map[string]string{
		"初始化配置（生成默认全局菜单，不覆盖其他设置）": "初始化配置",
		"设置管理QQ 12345678":                   "设置管理QQ QQ号",
		"循环群发群 10分钟 我马上到":                   "循环群发群 间隔 内容",
		"循环群发好友 10分钟 晚安":                    "循环群发好友 间隔 内容",
		"定时群发群 每天晚上8点发 晚安":                  "定时群发群 时间 内容",
		"定时群发好友 每天晚上8点发 晚安":                 "定时群发好友 时间 内容",
		"定时说说 每天晚上8点 今天也要开心":                "定时说说 时间 内容",
		"全体禁言（开启）/全体禁言 开/关":                 "全体禁言",
		"群发排除群 添加/删除/列表 群号（名单中的群不会收到群发）":    "群发排除群 添加 群号\n群发排除群 删除 群号\n群发排除群 列表",
		"群发排除好友 添加/删除/列表 QQ号（名单中的好友不会收到群发）": "群发排除好友 添加 QQ号\n群发排除好友 删除 QQ号\n群发排除好友 列表",
		"添加精准问 群聊/好友/合并 问题 答 回答":            "添加精准问xxx答xxx",
		"添加模糊问 群聊/好友/合并 关键词 答 回答":           "添加模糊问xxx答xxx",
		"设置续火时间 8:00（也支持 8.00、20：00）":       "设置续火时间 时间",
		"一键群打卡（打卡名单）":                       "一键群打卡",
		"飞速打卡（打卡名单）":                        "飞速打卡",
		"禁言 @成员 10分钟":                       "禁言 @成员 时长",
		"发群公告 内容（可加 | 图片URL）":               "发群公告 内容\n发群公告 内容 | 图片URL",
		"公告 内容（旧指令）":                        "公告 内容",
		"确认发送（确认刚才预览的群或好友）":                 "确认发送",
		"取消发送（放弃尚未发送的预览）":                   "取消发送",
		"字符 统计/反转/全角/半角 内容":                 "字符 统计 内容\n字符 反转 内容\n字符 全角 内容\n字符 半角 内容",
	}
	if formatted, ok := replacements[usage]; ok {
		usage = formatted
	}
	return stripMenuNotes(strings.TrimSpace(usage))
}

// Codex 修改：隐藏指令格式后面的解释性括号备注。
func stripMenuNotes(usage string) string {
	for {
		start := strings.Index(usage, "（")
		if start < 0 {
			return strings.TrimSpace(usage)
		}
		end := strings.Index(usage[start+len("（"):], "）")
		if end < 0 {
			return strings.TrimSpace(usage)
		}
		end += start + len("（")
		usage = usage[:start] + usage[end+len("）"):]
	}
}
