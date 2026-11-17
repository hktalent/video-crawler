package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CommonHeaders 返回通用浏览器请求头，用于模拟正常浏览器访问
func CommonHeaders() map[string]string {
	return map[string]string{
		"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Safari/605.1.15",
		"Accept":     "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
		"Accept-Encoding": "gzip, deflate, br",
		"Connection":      "keep-alive",
		"Upgrade-Insecure-Requests": "1",
	}
}

// ApplyHeaders 将请求头应用到 http.Request
func ApplyHeaders(req *http.Request, headers map[string]string) {
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

// NormalizeURL 将相对 URL 转为绝对 URL
func NormalizeURL(baseURL, href string) string {
	if href == "" {
		return ""
	}
	href = strings.TrimSpace(href)
	if strings.HasPrefix(href, "//") {
		if u, err := url.Parse(href); err == nil {
			base, err := url.Parse(baseURL)
			if err == nil {
				u.Scheme = base.Scheme
				return u.String()
			}
			u.Scheme = "https"
			return u.String()
		}
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	return base.ResolveReference(ref).String()
}

// DomainOf 从 URL 中提取域名
func DomainOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// IsSameDomain 判断两个 URL 是否同域
func IsSameDomain(a, b string) bool {
	da := DomainOf(a)
	db := DomainOf(b)
	if da == "" || db == "" {
		return false
	}
	return da == db
}

// IsVideoURL 判断链接是否是直接的视频文件
func IsVideoURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path := strings.ToLower(u.Path)
	videoExts := []string{
		".mp4", ".m4v", ".webm", ".mov", ".mkv", ".avi", ".flv",
		".wmv", ".mpeg", ".mpg", ".3gp", ".ts", ".m3u8", ".mpd",
	}
	for _, ext := range videoExts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	// m3u8/mpd 可能带 query
	if strings.Contains(path, ".m3u8") || strings.Contains(path, ".mpd") {
		return true
	}
	return false
}

// SanitizeFilename 清理文件名，移除不合法字符
func SanitizeFilename(name string) string {
	if name == "" {
		return "video"
	}
	// 移除路径分隔符和控制字符
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
		"\n", "_", "\r", "_", "\t", "_",
	)
	name = replacer.Replace(name)
	name = strings.TrimSpace(name)
	// 限制长度
	if len(name) > 120 {
		name = name[:120]
	}
	if name == "" {
		name = "video"
	}
	return name
}

// RandomID 生成随机 ID 用于文件名
func RandomID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ParseViewCount 解析观看次数字符串，支持中文/英文数字单位
// 例如：12345、1.2万、12万3千、1.5M、320K、1.2亿
func ParseViewCount(s string) int64 {
	if s == "" {
		return 0
	}
	s = strings.TrimSpace(s)
	// 移除所有空白和逗号
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u00a0", "") // non-breaking space

	// 匹配数字（可带小数）+ 可选单位
	re := regexp.MustCompile(`([\d]+(?:\.\d+)?)\s*([万亿万千百KMBkm]?)`)
	matches := re.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		// 尝试纯数字
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		return 0
	}

	var total int64
	// 处理 "12万3千" 这类组合
	for _, m := range matches {
		numStr := m[1]
		unit := m[2]
		num, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			continue
		}
		multiplier := int64(1)
		switch unit {
		case "万":
			multiplier = 10000
		case "亿":
			multiplier = 100000000
		case "千":
			multiplier = 1000
		case "百":
			multiplier = 100
		case "K", "k":
			multiplier = 1000
		case "M", "m":
			multiplier = 1000000
		case "B", "b":
			multiplier = 1000000000
		}
		total += int64(num * float64(multiplier))
	}
	return total
}

// EnsureDir 确保目录存在
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// FileExists 判断文件是否存在
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FormatBytes 格式化字节数为可读字符串
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// HTTPGetWithRetry 带重试的 HTTP GET
func HTTPGetWithRetry(client *http.Client, rawURL string, headers map[string]string, maxRetries int) (*http.Response, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		req, err := http.NewRequest("GET", rawURL, nil)
		if err != nil {
			return nil, err
		}
		ApplyHeaders(req, headers)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
			continue
		}
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("request failed after %d retries: %w", maxRetries, lastErr)
}

// DownloadFile 下载文件到指定路径，带进度回调
func DownloadFile(client *http.Client, rawURL, destPath string, headers map[string]string, onProgress func(downloaded, total int64)) error {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return err
	}
	ApplyHeaders(req, headers)
	req.Header.Set("Referer", rawURL)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// 确保目录存在
	if err := EnsureDir(filepath.Dir(destPath)); err != nil {
		return err
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 64*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			downloaded += int64(n)
			if onProgress != nil {
				onProgress(downloaded, total)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}
