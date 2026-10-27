// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func actionTimeout(action string) time.Duration {
	switch action {
	case "get_friend_list", "get_group_list", "get_group_member_list":
		return 70 * time.Second
	case "grab_red_packet", "get_red_packet_info", "get_music_ark":
		return 65 * time.Second
	case "execute_level_tasks":
		return 5 * time.Minute
	default:
		return 30 * time.Second
	}
}

func firstText(row M, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(text(row[key])); value != "" {
			return value
		}
	}
	return ""
}

func memberReply(group, id int64, member M) string {
	lines := []string{"【群成员资料】", fmt.Sprintf("群号：%d\nQQ：%d", group, id)}
	for _, field := range []struct{ key, label string }{{"nickname", "昵称"}, {"card", "群名片"}, {"title", "专属头衔"}, {"level", "群等级"}} {
		if value := firstText(member, field.key); value != "" {
			lines = append(lines, field.label+"："+value)
		}
	}
	for _, field := range []struct{ key, label string }{{"join_time", "入群时间"}, {"last_speak_time", "最后发言"}} {
		if stamp := number(member[field.key]); stamp > 0 {
			lines = append(lines, field.label+"："+time.Unix(stamp, 0).Local().Format("2006-01-02 15:04"))
		}
	}
	return strings.Join(lines, "\n")
}

func commandAPIReply(word string, params M, raw json.RawMessage) (string, error) {
	var result M
	if string(raw) != "null" && len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", errors.New("萌卡返回的数据无法读取，请在接口调试页查看响应")
		}
	}
	switch word {
	case "账号状态":
		if result == nil || number(result["self_id"]) <= 0 {
			return "", errors.New("账号状态响应缺少 QQ 号，请重新读取")
		}
		state, ok := map[int64]string{0: "离线", 1: "在线", 2: "登录中"}[number(result["status"])]
		if !ok {
			state = "状态未知"
		}
		lines := []string{"【账号状态】", "QQ：" + text(result["self_id"]), "状态：" + state}
		if name := firstText(result, "nickname"); name != "" {
			lines = append(lines, "昵称："+name)
		}
		for _, field := range []struct{ key, label string }{{"level", "QQ 等级"}, {"friend_count", "好友数量"}, {"group_count", "群聊数量"}, {"received", "收到消息"}, {"sent", "发出消息"}} {
			if value, exists := result[field.key]; exists {
				lines = append(lines, field.label+"："+text(value))
			}
		}
		return strings.Join(lines, "\n"), nil
	case "点赞":
		code, ok := result["code"]
		if !ok || number(code) != 0 {
			reason := firstText(result, "msg", "message")
			if reason == "" {
				reason = "接口没有确认成功"
			}
			return "", fmt.Errorf("名片点赞未成功：%s", reason)
		}
		return fmt.Sprintf("【名片点赞已完成】\nQQ：%d\n次数：%d", number(params["target_uin"]), number(params["like_count"])), nil
	case "踢人", "头衔", "个性签名":
		if confirmed, _ := result["success"].(bool); !confirmed {
			return "", errors.New("接口没有确认操作成功，请先核对 QQ 中的结果")
		}
	case "公告", "发群公告":
		if firstText(result, "notice_id") == "" {
			return "", errors.New("公告接口没有返回公告编号，请先查看群公告确认结果")
		}
	}
	return actionReply(word, params), nil
}

func parseMuteDuration(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	multiplier := int64(1)
	for _, unit := range []struct {
		suffix  string
		seconds int64
	}{{"分钟", 60}, {"小时", 3600}, {"秒钟", 1}, {"天", 86400}, {"时", 3600}, {"分", 60}, {"秒", 1}} {
		if strings.HasSuffix(value, unit.suffix) {
			value = strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			multiplier = unit.seconds
			break
		}
	}
	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil || count < 0 || count > 2592000/multiplier {
		return 0, false
	}
	return count * multiplier, true
}
