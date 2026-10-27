// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func selectedMassIDs(c Config, target string) []string {
	configured := c.Mass
	excluded := c.MassExcluded
	groupTarget := target == "group"
	if target == "friend" {
		configured = c.MassFriends
		excluded = c.MassFriendsExcluded
	} else if !groupTarget {
		return nil
	}
	ids := make([]string, 0, len(configured))
	for id, on := range configured {
		if !on || excluded[id] {
			continue
		}
		if groupTarget && !c.On("qunfa", number(id)) {
			continue
		}
		if !groupTarget && !c.On("qunfa", 0) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func selectedSignIDs(c Config) []string {
	ids := make([]string, 0, len(c.SignGroups))
	for id, on := range c.SignGroups {
		if on && c.On("daka", number(id)) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func validateJobRecipients(c Config, job Job) error {
	if !job.Enabled || job.Kind == "timedss" {
		return nil
	}
	target := job.Target
	if target == "" {
		target = "group"
	}
	groups, friends := len(selectedMassIDs(c, "group")), len(selectedMassIDs(c, "friend"))
	switch target {
	case "group":
		if groups == 0 {
			return errors.New("还没有可发送的群。先到网页“问答与白名单”选择群，再创建任务。")
		}
	case "friend":
		if friends == 0 {
			return errors.New("还没有可发送的好友。先到网页“问答与白名单”选择好友，再创建任务。")
		}
	case "both":
		if groups == 0 || friends == 0 {
			return errors.New("群和好友都要有选择。“问答与白名单”里至少选择一个群和一个好友，再创建任务。")
		}
	default:
		return errors.New("发送对象无效，请选择群、好友或两者。")
	}
	return nil
}

func scheduleAck(job Job, c Config, replaced bool) string {
	title := "定时任务已保存"
	if replaced {
		title = "自动任务已更新"
	}
	var lines []string
	lines = append(lines, "【"+title+"】")
	switch job.Kind {
	case "timedss":
		lines = append(lines, "内容：QQ 空间说说")
	case "loop", "timed":
		target := job.Target
		if target == "" {
			target = "group"
		}
		label := map[string]string{"group": "群聊", "friend": "好友私聊", "both": "群聊和好友私聊"}[target]
		count := len(selectedMassIDs(c, target))
		if target == "both" {
			count = len(selectedMassIDs(c, "group")) + len(selectedMassIDs(c, "friend"))
		}
		unit := "个对象"
		if target == "group" {
			unit = "个群"
		} else if target == "friend" {
			unit = "位好友"
		}
		lines = append(lines, fmt.Sprintf("发送对象：%s，共 %d%s", label, count, unit))
		if job.Kind == "loop" {
			lines = append(lines, "发送间隔：每隔 "+intervalLabel(job.Interval))
		} else {
			lines = append(lines, "发送时间：每天 "+job.Time)
		}
	}
	lines = append(lines, "发送内容："+job.Text)
	if job.Next > 0 {
		lines = append(lines, "下次执行："+time.Unix(job.Next, 0).Local().Format("2006-01-02 15:04"))
	}
	return strings.Join(lines, "\n")
}

func intervalLabel(seconds int64) string {
	switch {
	case seconds%3600 == 0:
		return fmt.Sprintf("%d小时", seconds/3600)
	case seconds%60 == 0:
		return fmt.Sprintf("%d分钟", seconds/60)
	default:
		return fmt.Sprintf("%d秒", seconds)
	}
}

func taskStatus(c Config) string {
	if len(c.Jobs) == 0 {
		return "【自动任务】\n目前没有任务。可用“定时群发菜单”查看 QQ 指令，或在网页“自动任务”里添加。"
	}
	kinds := map[string]string{"loop": "循环群发", "timed": "定时群发", "timedss": "定时说说"}
	targets := map[string]string{"group": "群聊", "friend": "好友私聊", "both": "群聊和好友私聊"}
	lines := []string{"【自动任务状态】"}
	for _, job := range c.Jobs {
		state := "已暂停"
		if job.Enabled {
			state = "运行中"
		}
		title := kinds[job.Kind]
		if title == "" {
			title = "自动任务"
		}
		if job.Kind != "timedss" {
			target := job.Target
			if target == "" {
				target = "group"
			}
			title += " · " + targets[target]
		}
		lines = append(lines, "\n• "+title+"（"+state+"）")
		if job.Kind == "loop" {
			lines = append(lines, "  间隔：每隔 "+intervalLabel(job.Interval))
		} else {
			lines = append(lines, "  时间：每天 "+job.Time)
		}
		if job.Kind != "timedss" {
			target := job.Target
			if target == "" {
				target = "group"
			}
			count := len(selectedMassIDs(c, target))
			if target == "both" {
				count = len(selectedMassIDs(c, "group")) + len(selectedMassIDs(c, "friend"))
			}
			lines = append(lines, fmt.Sprintf("  当前收件人：%d 个", count))
		}
		lines = append(lines, "  内容："+job.Text)
		if job.Next > 0 && job.Enabled {
			lines = append(lines, "  下次执行："+time.Unix(job.Next, 0).Local().Format("2006-01-02 15:04"))
		}
		if job.LastRun > 0 {
			lines = append(lines, "  上次执行："+time.Unix(job.LastRun, 0).Local().Format("2006-01-02 15:04"))
			if job.LastResult != "" {
				lines = append(lines, "  执行结果："+job.LastResult)
			}
		} else {
			lines = append(lines, "  执行结果：尚未执行")
		}
	}
	return strings.Join(lines, "\n")
}

func actionReply(word string, params M) string {
	groupID := number(params["group_id"])
	targetID := number(params["user_id"])
	switch word {
	case "全体禁言":
		action := "解除全体禁言"
		if mute, _ := params["mute"].(bool); mute {
			action = "开启全体禁言"
		}
		return fmt.Sprintf("【群聊操作已提交】\n群号：%d\n操作：%s", groupID, action)
	case "全体解禁":
		return fmt.Sprintf("【群聊操作已提交】\n群号：%d\n操作：解除全体禁言", groupID)
	case "禁言":
		seconds := number(params["duration_sec"])
		action := "解除禁言"
		if seconds > 0 {
			action = fmt.Sprintf("禁言 %d 秒", seconds)
		}
		return fmt.Sprintf("【群聊操作已提交】\n群号：%d\nQQ：%d\n操作：%s", groupID, number(params["target_uin"]), action)
	case "解禁":
		return fmt.Sprintf("【群聊操作已提交】\n群号：%d\nQQ：%d\n操作：解除禁言", groupID, number(params["target_uin"]))
	case "踢人":
		return fmt.Sprintf("【群聊操作已提交】\n群号：%d\nQQ：%d\n操作：提交移出群聊请求", groupID, targetID)
	case "公告", "发群公告":
		result := fmt.Sprintf("【群公告已提交】\n群号：%d\n内容：%s", groupID, text(params["content"]))
		if text(params["image"]) != "" {
			result += "\n图片：已附上"
		}
		return result
	case "头衔":
		title := text(params["title"])
		if title == "" {
			title = "清除头衔"
		} else {
			title = "设置为“" + title + "”"
		}
		return fmt.Sprintf("【群头衔操作已提交】\n群号：%d\nQQ：%d\n操作：%s", groupID, targetID, title)
	case "打卡":
		return fmt.Sprintf("【群打卡已完成】\n群号：%d", groupID)
	case "备注":
		return fmt.Sprintf("【好友备注已更新】\nQQ：%d\n备注：%s", targetID, text(params["remark"]))
	case "个性签名":
		return "【QQ 资料已更新】\n个性签名已提交。"
	default:
		return ""
	}
}
