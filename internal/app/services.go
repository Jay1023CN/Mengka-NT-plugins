// Copyright (c) 2026 JAY. All rights reserved.
// Preserve this attribution when editing this file.

package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 25 * time.Second}

func fetch(ctx context.Context, addr string, limit int64) ([]byte, error) {
	return fetchUsing(ctx, addr, limit, httpClient)
}
func fetchUsing(ctx context.Context, addr string, limit int64, client *http.Client) ([]byte, error) {
	u, err := url.Parse(addr)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("地址必须为完整的 HTTP(S) URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Catherina/"+Version)
	req.Header.Set("Referer", "https://music.163.com/")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("响应超过大小上限")
	}
	return body, nil
}
func (e *Engine) saveFlash(m Event, c Config, index int) {
	raw, err := e.call(m.Account(), "get_flash_image", M{"message_id": m.MessageID, "index": index})
	if err != nil {
		e.owner(m, c, "闪照原图获取失败："+err.Error())
		return
	}
	var image M
	if json.Unmarshal(raw, &image) != nil || text(image["url"]) == "" {
		e.owner(m, c, "闪照接口未返回原图地址")
		return
	}
	data, err := fetch(e.ctx, text(image["url"]), 20*1024*1024)
	if err != nil {
		e.owner(m, c, "闪照下载失败："+err.Error())
		return
	}
	ext := ".bin"
	switch http.DetectContentType(data) {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	}
	dir := filepath.Join(e.DataDir, "flash", fmt.Sprintf("%d-%s", m.SelfID, m.ClientType))
	if err = os.MkdirAll(dir, 0700); err != nil {
		e.owner(m, c, "创建闪照目录失败："+err.Error())
		return
	}
	name := fmt.Sprintf("%d-%d-%d%s", m.MessageID, index, time.Now().UnixMilli(), ext)
	dest := filepath.Join(dir, name)
	if err = os.WriteFile(dest, data, 0600); err != nil {
		e.owner(m, c, "保存闪照失败："+err.Error())
		return
	}
	e.owner(m, c, "闪照已保存：\n"+dest)
}
func (e *Engine) pollFeeds(a Account, c Config) {
	fresh, err := e.Store.Get(a.Key())
	if err != nil || !fresh.Config.On("miao", 0) {
		return
	}
	c = fresh.Config
	e.mu.Lock()
	since, ok := e.feedBase[a.Key()]
	if !ok {
		since = time.Now().Unix()
		e.feedBase[a.Key()] = since
	}
	e.mu.Unlock()
	raw, err := e.call(a, "get_qzone_friend_feeds", M{})
	if err != nil {
		log.Printf("账号 %s 检查空间动态失败：%v", a.Key(), err)
		return
	}
	var response struct {
		Feeds []M `json:"feeds"`
	}
	if json.Unmarshal(raw, &response) != nil {
		log.Printf("账号 %s 的空间动态回包格式不正确", a.Key())
		return
	}
	for i, feed := range response.Feeds {
		if i >= 20 {
			break
		}
		uid := number(feed["user_id"])
		if uid == a.SelfID || uid <= 0 || number(feed["create_time"]) < since {
			continue
		}
		if len(c.MiaoUsers) > 0 && !contains(c.MiaoUsers, uid) {
			continue
		}
		id := text(feed["feeds_key"])
		if id == "" {
			id = text(feed["feed_id"])
		}
		if id == "" {
			continue
		}
		seen, err := e.Store.Seen(a.Key(), fmt.Sprintf("feed:%d:%s", uid, id))
		if err != nil || seen {
			continue
		}
		fresh, err = e.Store.Get(a.Key())
		if err != nil || !fresh.Config.On("miao", 0) {
			return
		}
		if !e.allowed() {
			return
		}
		err = e.Client.Notify("like_qzone_feed", M{"self_id": a.SelfID, "client_type": a.ClientType, "feed": feed})
		if err != nil {
			log.Printf("空间点赞提交失败：%v", err)
			continue
		}
		if len(c.Comments) > 0 {
			idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(c.Comments))))
			if err != nil {
				return
			}
			content := expand(c.Comments[idx.Int64()], Event{SelfID: a.SelfID, ClientType: a.ClientType, UserID: uid})
			_, err = e.call(a, "comment_qzone_feed", M{"feed": feed, "content": content, "images": []any{}})
			if err != nil {
				log.Printf("空间评论失败：%v", err)
			}
		}
	}
}
func (e *Engine) level(a Account, execute bool) (string, error) {
	raw, err := e.call(a, "get_level_task_panel", M{"refresh": true})
	if err != nil {
		return "", err
	}
	var data any
	if json.Unmarshal(raw, &data) != nil {
		return "", errors.New("等级任务面板回包格式不正确")
	}
	tasks := map[string]bool{}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["can_execute"] == true && x["is_paid_task"] != true && text(x["title"]) != "" {
				tasks[text(x["title"])] = true
			}
			for _, v := range x {
				visit(v)
			}
		case []any:
			for _, v := range x {
				visit(v)
			}
		}
	}
	visit(data)
	names := make([]string, 0, len(tasks))
	for title := range tasks {
		names = append(names, title)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "当前没有可执行的免费等级任务", nil
	}
	if !execute {
		return "可执行的免费任务：\n" + strings.Join(names, "\n") + "\n\n输入「加速 执行」开始执行。", nil
	}
	_, err = e.call(a, "execute_level_tasks", M{"tasks": names})
	if err != nil {
		return "", err
	}
	return "等级任务接口已确认完成；加速天数以 QQ 等级面板为准", nil
}
func (e *Engine) music(m Event, keyword string) error {
	data, err := fetch(e.ctx, "https://music.163.com/api/search/get/web?s="+url.QueryEscape(keyword)+"&type=1&limit=1", 2*1024*1024)
	if err != nil {
		return fmt.Errorf("歌曲搜索失败：%w", err)
	}
	var search struct {
		Result struct {
			Songs []struct {
				ID      int64  `json:"id"`
				Name    string `json:"name"`
				Artists []struct {
					Name   string `json:"name"`
					PicURL string `json:"picUrl"`
				} `json:"artists"`
				Album struct {
					PicURL string `json:"picUrl"`
				} `json:"album"`
			} `json:"songs"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &search) != nil || len(search.Result.Songs) == 0 {
		return errors.New("没有搜到这首歌")
	}
	song := search.Result.Songs[0]
	if song.ID <= 0 || song.Name == "" {
		return errors.New("搜索没有返回有效歌曲")
	}
	singers := []string{}
	cover := song.Album.PicURL
	for _, artist := range song.Artists {
		singers = append(singers, artist.Name)
		if cover == "" {
			cover = artist.PicURL
		}
	}
	if cover == "" {
		return errors.New("歌曲缺少封面，未生成音乐卡片")
	}
	raw, err := e.call(m.Account(), "get_music_ark", M{"type": "163", "title": song.Name, "content": strings.Join(singers, " / "), "url": fmt.Sprintf("https://music.163.com/song?id=%d", song.ID), "audio": fmt.Sprintf("https://music.163.com/song/media/outer/url?id=%d.mp3", song.ID), "image": cover})
	if err != nil {
		return err
	}
	var result struct {
		Data M `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Data == nil {
		return errors.New("音乐卡片接口没有返回有效 data")
	}
	return e.send(m.Account(), m.GroupID, m.Author(), []Segment{{Type: "ark", Data: result.Data}})
}
