package crawler

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"video-crawler/internal/utils"

	"github.com/PuerkitoBio/goquery"
)

// VideoInfo 表示一个识别到的视频资源
type VideoInfo struct {
	PageURL   string // 视频所在页面 URL
	Title     string // 视频标题
	VideoURL  string // 视频直接地址（mp4/m3u8 等）
	Thumbnail string // 缩略图
	Views     int64  // 观看次数
	RawViews  string // 原始观看次数字符串
	Source    string // 识别来源（video标签/og:meta/JSON-LD等）
}

// ParsePage 解析 HTML 页面，提取视频信息和观看次数
func ParsePage(pageURL, html string) []VideoInfo {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}

	var videos []VideoInfo
	seen := make(map[string]bool)

	addVideo := func(v VideoInfo) {
		if v.VideoURL == "" {
			return
		}
		v.VideoURL = utils.NormalizeURL(pageURL, v.VideoURL)
		if seen[v.VideoURL] {
			return
		}
		seen[v.VideoURL] = true
		if v.Title == "" {
			v.Title = extractTitle(doc)
		}
		v.PageURL = pageURL
		videos = append(videos, v)
	}

	// 1. <video> 标签及其 <source> 子标签
	doc.Find("video").Each(func(i int, s *goquery.Selection) {
		v := VideoInfo{Source: "video-tag"}
		if src, ok := s.Attr("src"); ok {
			v.VideoURL = src
		}
		if poster, ok := s.Attr("poster"); ok {
			v.Thumbnail = utils.NormalizeURL(pageURL, poster)
		}
		// 子 source
		s.Find("source").Each(func(j int, src *goquery.Selection) {
			if ssrc, ok := src.Attr("src"); ok {
				addVideo(VideoInfo{
					VideoURL:  ssrc,
					Thumbnail: v.Thumbnail,
					Title:     v.Title,
					Source:    "source-tag",
				})
			}
		})
		if v.VideoURL != "" {
			addVideo(v)
		}
	})

	// 2. og:video / twitter:player 等 meta
	doc.Find("meta").Each(func(i int, s *goquery.Selection) {
		property, _ := s.Attr("property")
		name, _ := s.Attr("name")
		content, _ := s.Attr("content")
		if content == "" {
			return
		}
		switch {
		case property == "og:video" || property == "og:video:url" || property == "og:video:secure_url":
			addVideo(VideoInfo{VideoURL: content, Source: "og:video"})
		case property == "og:image":
			// 先记下来，标题后面统一处理
		}
		_ = name
	})

	// 3. JSON-LD VideoObject
	doc.Find("script[type=\"application/ld+json\"]").Each(func(i int, s *goquery.Selection) {
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			return
		}
		// 可能是数组或对象
		var items []map[string]interface{}
		var single map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			if err2 := json.Unmarshal([]byte(raw), &single); err2 == nil {
				items = []map[string]interface{}{single}
			}
		}
		for _, item := range items {
			video := parseJSONLDVideo(pageURL, item)
			if video.VideoURL != "" {
				addVideo(video)
			}
		}
	})

	// 4. <a> 链接中的视频文件
	doc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		abs := utils.NormalizeURL(pageURL, href)
		if utils.IsVideoURL(abs) {
			linkText := strings.TrimSpace(s.Text())
			// 链接文本可能是"播放"/"下载"等无意义词，回退到页面标题
			if !isMeaningfulTitle(linkText) {
				linkText = ""
			}
			addVideo(VideoInfo{
				VideoURL: abs,
				Title:    linkText,
				Source:   "link",
			})
		}
	})

	// 5. iframe 嵌入（记录为候选，交给上层递归处理）
	doc.Find("iframe[src]").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		abs := utils.NormalizeURL(pageURL, src)
		if abs == "" {
			return
		}
		// 常见视频平台 iframe
		if isVideoEmbedURL(abs) {
			addVideo(VideoInfo{
				VideoURL: abs,
				Source:   "iframe",
			})
		}
	})

	// 6. 内联 <script> 中的视频 URL（兜底正则）
	inlineVideoURLs := extractInlineVideoURLs(html)
	for _, u := range inlineVideoURLs {
		addVideo(VideoInfo{VideoURL: u, Source: "inline-script"})
	}

	// 提取观看次数并应用到所有视频
	views, rawViews := extractViewCount(html, doc)
	for i := range videos {
		if videos[i].Views == 0 {
			videos[i].Views = views
			videos[i].RawViews = rawViews
		}
	}

	return videos
}

// parseJSONLDVideo 从 JSON-LD 条目中解析视频
func parseJSONLDVideo(pageURL string, item map[string]interface{}) VideoInfo {
	v := VideoInfo{Source: "json-ld"}

	// 递归查找 @type 包含 VideoObject
	if t, ok := item["@type"]; ok {
		typeStr := fmtType(t)
		if !strings.Contains(typeStr, "VideoObject") && !strings.Contains(typeStr, "Video") {
			// 递归子对象
			for _, val := range item {
				if sub, ok := val.(map[string]interface{}); ok {
					if sv := parseJSONLDVideo(pageURL, sub); sv.VideoURL != "" {
						return sv
					}
				}
			}
			return v
		}
	}

	if contentURL, ok := item["contentUrl"]; ok {
		v.VideoURL = utils.NormalizeURL(pageURL, fmtString(contentURL))
	}
	if embedURL, ok := item["embedUrl"]; ok && v.VideoURL == "" {
		v.VideoURL = utils.NormalizeURL(pageURL, fmtString(embedURL))
	}
	if name, ok := item["name"]; ok {
		v.Title = fmtString(name)
	}
	if thumb, ok := item["thumbnailUrl"]; ok {
		v.Thumbnail = utils.NormalizeURL(pageURL, fmtString(thumb))
	}
	// interactionCount: 可能是数字或 {@type, userInteractionCount}
	if ic, ok := item["interactionCount"]; ok {
		switch n := ic.(type) {
		case float64:
			v.Views = int64(n)
		case string:
			v.Views = utils.ParseViewCount(n)
		case map[string]interface{}:
			if cnt, ok := n["userInteractionCount"]; ok {
				v.Views = int64(cnt.(float64))
			}
		}
	}
	return v
}

// extractTitle 从页面提取视频标题
// 优先级：og:title > twitter:title > h1 > title（清洗站点后缀）
func extractTitle(doc *goquery.Document) string {
	// 1. og:title（通常是最精准的视频标题）
	if t, ok := doc.Find("meta[property=\"og:title\"]").Attr("content"); ok {
		if t = strings.TrimSpace(t); t != "" {
			return cleanTitle(t)
		}
	}
	// 2. twitter:title
	if t, ok := doc.Find("meta[name=\"twitter:title\"]").Attr("content"); ok {
		if t = strings.TrimSpace(t); t != "" {
			return cleanTitle(t)
		}
	}
	// 3. <h1> 标签（视频页通常有 h1 标题）
	if t := doc.Find("h1").First().Text(); t != "" {
		if t = strings.TrimSpace(t); t != "" {
			return cleanTitle(t)
		}
	}
	// 4. <title> 标签（清洗站点后缀）
	if t := doc.Find("title").Text(); t != "" {
		if t = strings.TrimSpace(t); t != "" {
			return cleanTitle(t)
		}
	}
	return ""
}

// cleanTitle 清洗标题：去除站点名后缀、多余空白
func cleanTitle(title string) string {
	title = strings.TrimSpace(title)
	// 去除常见站点后缀：" - 站点名"、" _ 站点名"、" | 站点名"
	// 只在分隔符后内容较短（可能是站点名）时才去除
	for _, sep := range []string{" - ", " _ ", " | ", " – ", " — "} {
		if idx := strings.LastIndex(title, sep); idx > 0 {
			suffix := strings.TrimSpace(title[idx+len(sep):])
			// 后缀长度 < 20 且不含空格（大概率是站点名）
			if len(suffix) < 20 && !strings.Contains(suffix, " ") {
				title = strings.TrimSpace(title[:idx])
				break
			}
		}
	}
	// 压缩多余空白
	title = regexp.MustCompile(`\s+`).ReplaceAllString(title, " ")
	return strings.TrimSpace(title)
}

// isMeaningfulTitle 判断标题文本是否有意义（非"播放"/"下载"等无意义词）
func isMeaningfulTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}
	// 长度过短无意义
	if len([]rune(title)) <= 2 {
		return false
	}
	// 常见无意义词
	meaningless := []string{
		"播放", "下载", "点击", "观看", "查看", "详情",
		"play", "download", "watch", "view", "click", "more",
		"▶", "▼", ">>", "»",
	}
	low := strings.ToLower(title)
	for _, m := range meaningless {
		if low == m || low == m+"..." || low == "["+m+"]" {
			return false
		}
	}
	return true
}

// extractViewCount 从 HTML 中提取观看次数
func extractViewCount(html string, doc *goquery.Document) (int64, string) {
	// 1. 先从 meta 中找
	if v, ok := doc.Find("meta[itemprop=\"interactionCount\"]").Attr("content"); ok {
		return utils.ParseViewCount(v), v
	}

	// 2. 常见观看次数文本正则
	// 数字部分支持 K/M/B/万/亿/千 单位和千位逗号，匹配如 "227K 次观看"、"1.5M views"、"25.6万次播放"、"1,234 views"
	patterns := []struct {
		re *regexp.Regexp
	}{
		{regexp.MustCompile(`(?i)([\d,]+(?:\.\d+)?\s*[KMB万亿千]?)\s*(?:次)?(?:播放|播放量|观看|浏览)`)},
		{regexp.MustCompile(`(?i)([\d,]+(?:\.\d+)?\s*[KMB万亿千]?)\s*(?:次)?(?:views?|plays?)`)},
		{regexp.MustCompile(`(?i)播放(?:量|次数)?\s*[:：]?\s*([\d,]+(?:\.\d+)?\s*[KMB万亿千]?)`)},
		{regexp.MustCompile(`(?i)观看(?:次数|量)?\s*[:：]?\s*([\d,]+(?:\.\d+)?\s*[KMB万亿千]?)`)},
		{regexp.MustCompile(`(?i)views?\s*[:：]?\s*([\d,.]+\s*[KMB]?)`)},
		{regexp.MustCompile(`(?i)次\s*(?:观看|播放|浏览)\s*[:：]?\s*([\d,]+(?:\.\d+)?\s*[KMB万亿千]?)`)},
	}

	for _, p := range patterns {
		if m := p.re.FindStringSubmatch(html); len(m) > 1 {
			raw := strings.TrimSpace(m[1])
			return utils.ParseViewCount(raw), raw
		}
	}

	// 3. 兜底：在正文中找带单位的数字
	unitRe := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(万|亿|K|M|B)`)
	if m := unitRe.FindStringSubmatch(html); len(m) > 1 {
		raw := m[1] + m[2]
		return utils.ParseViewCount(raw), raw
	}

	return 0, ""
}

// isVideoEmbedURL 判断是否是常见视频平台的嵌入 URL
func isVideoEmbedURL(u string) bool {
	hosts := []string{
		"youtube.com/embed", "youtube.com/v/", "youtu.be",
		"player.bilibili.com", "bilibili.com/blackboard",
		"v.qq.com", "player.youku.com", "player.vimeo.com",
		"dailymotion.com/embed", "v.qq.com/txp",
	}
	low := strings.ToLower(u)
	for _, h := range hosts {
		if strings.Contains(low, h) {
			return true
		}
	}
	return utils.IsVideoURL(u)
}

// extractInlineVideoURLs 从内联脚本中正则提取视频 URL
func extractInlineVideoURLs(html string) []string {
	var urls []string
	// 匹配 mp4/m3u8/webm/mov 等 URL
	re := regexp.MustCompile(`(https?:)?//[^\s"'<>]+\.(?:mp4|m3u8|webm|mov|mkv|m4v|mpd)(?:\?[^\s"'<>]*)?`)
	matches := re.FindAllString(html, -1)
	for _, m := range matches {
		m = strings.Trim(m, `"'`)
		urls = append(urls, m)
	}
	return urls
}

// fmtType 格式化 JSON-LD 的 @type
func fmtType(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []interface{}:
		var parts []string
		for _, p := range t {
			parts = append(parts, fmtString(p))
		}
		return strings.Join(parts, ",")
	}
	return ""
}

// extractLinks 从 HTML 中提取所有 <a> 链接（去重）
func extractLinks(pageURL, html string) []string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var links []string
	doc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		abs := utils.NormalizeURL(pageURL, href)
		if abs == "" || seen[abs] {
			return
		}
		// 跳过锚点和 javascript
		if strings.HasPrefix(abs, "#") || strings.HasPrefix(abs, "javascript:") || strings.HasPrefix(abs, "mailto:") {
			return
		}
		seen[abs] = true
		links = append(links, abs)
	})
	return links
}

// fmtString 将 interface{} 转字符串
func fmtString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}
