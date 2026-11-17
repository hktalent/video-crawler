package downloader

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"video-crawler/internal/utils"
)

// Downloader 视频下载器
type Downloader struct {
	Client    *http.Client
	Headers   map[string]string
	OutDir    string
	UseFFmpeg bool
}

// New 创建下载器
func New(outDir string) *Downloader {
	return &Downloader{
		Client: &http.Client{
			Timeout: 30 * time.Minute,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		Headers:   utils.CommonHeaders(),
		OutDir:    outDir,
		UseFFmpeg: ffmpegAvailable(),
	}
}

// ffmpegAvailable 检查 ffmpeg 是否可用
func ffmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// Download 根据视频 URL 类型选择下载方式
func (d *Downloader) Download(videoURL, pageURL, title string) (string, error) {
	ext := videoExt(videoURL)
	filename := utils.SanitizeFilename(title)
	if filename == "" || filename == "video" {
		filename = "video_" + utils.RandomID(4)
	}

	switch {
	case strings.Contains(strings.ToLower(videoURL), ".m3u8"):
		return d.downloadHLS(videoURL, pageURL, filename)
	case ext != "":
		dest := filepath.Join(d.OutDir, filename+ext)
		if utils.FileExists(dest) {
			return dest, nil
		}
		err := d.downloadDirect(videoURL, pageURL, dest)
		return dest, err
	default:
		// 未知类型，尝试用 ffmpeg 下载
		if d.UseFFmpeg {
			dest := filepath.Join(d.OutDir, filename+".mp4")
			if utils.FileExists(dest) {
				return dest, nil
			}
			return d.downloadWithFFmpeg(videoURL, pageURL, dest)
		}
		// 兜底按 mp4 下载
		dest := filepath.Join(d.OutDir, filename+".mp4")
		if utils.FileExists(dest) {
			return dest, nil
		}
		err := d.downloadDirect(videoURL, pageURL, dest)
		return dest, err
	}
}

// videoExt 从 URL 提取视频扩展名
func videoExt(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	path := strings.ToLower(u.Path)
	exts := []string{".mp4", ".webm", ".mov", ".mkv", ".m4v", ".avi", ".flv", ".ts"}
	for _, ext := range exts {
		if strings.HasSuffix(path, ext) {
			return ext
		}
	}
	return ""
}

// downloadDirect 直接下载视频文件
func (d *Downloader) downloadDirect(videoURL, pageURL, dest string) error {
	headers := copyMap(d.Headers)
	headers["Referer"] = pageURL
	headers["Origin"] = originOf(pageURL)

	fmt.Printf("  ↓ 直接下载: %s\n", videoURL)
	return utils.DownloadFile(d.Client, videoURL, dest, headers, func(downloaded, total int64) {
		if total > 0 {
			pct := float64(downloaded) / float64(total) * 100
			fmt.Printf("\r    进度: %s / %s (%.1f%%)",
				utils.FormatBytes(downloaded), utils.FormatBytes(total), pct)
		} else {
			fmt.Printf("\r    已下载: %s", utils.FormatBytes(downloaded))
		}
	})
}

// downloadHLS 下载 m3u8 视频
func (d *Downloader) downloadHLS(m3u8URL, pageURL, filename string) (string, error) {
	if d.UseFFmpeg {
		dest := filepath.Join(d.OutDir, filename+".mp4")
		if utils.FileExists(dest) {
			return dest, nil
		}
		return d.downloadWithFFmpeg(m3u8URL, pageURL, dest)
	}
	// 自行实现 HLS 下载
	dest := filepath.Join(d.OutDir, filename+".ts")
	if utils.FileExists(dest) {
		return dest, nil
	}
	return d.downloadHLSSegments(m3u8URL, pageURL, dest)
}

// downloadWithFFmpeg 使用 ffmpeg 下载（支持 m3u8/mpd/普通视频）
func (d *Downloader) downloadWithFFmpeg(videoURL, pageURL, dest string) (string, error) {
	fmt.Printf("  ↓ ffmpeg 下载: %s\n", videoURL)

	utils.EnsureDir(filepath.Dir(dest))

	// 构建 ffmpeg 参数
	args := []string{
		"-y",
		"-headers", fmt.Sprintf("Referer: %s\r\nUser-Agent: %s\r\n", pageURL, d.Headers["User-Agent"]),
		"-i", videoURL,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		dest,
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil // ffmpeg 输出到 stderr

	// 用管道捕获 stderr 以便显示进度
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("ffmpeg 启动失败: %w", err)
	}

	// 读取 stderr 输出
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "time=") || strings.Contains(line, "frame=") {
				fmt.Printf("\r    ffmpeg: %s", line)
			}
		}
	}()

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("ffmpeg 执行失败: %w", err)
	}
	fmt.Println()
	return dest, nil
}

// downloadHLSSegments 自行实现 HLS 分片下载与合并
func (d *Downloader) downloadHLSSegments(m3u8URL, pageURL, dest string) (string, error) {
	fmt.Printf("  ↓ HLS 分片下载: %s\n", m3u8URL)

	// 1. 下载 m3u8 播放列表
	headers := copyMap(d.Headers)
	headers["Referer"] = pageURL

	playlist, err := d.fetchText(m3u8URL, headers)
	if err != nil {
		return "", fmt.Errorf("下载 m3u8 失败: %w", err)
	}

	// 2. 解析分片列表
	segments, err := parseM3U8(m3u8URL, playlist)
	if err != nil {
		return "", err
	}
	if len(segments) == 0 {
		return "", fmt.Errorf("m3u8 中未找到分片")
	}

	// 3. 并发下载分片到临时文件
	tmpDir := filepath.Join(os.TempDir(), "hls_"+utils.RandomID(6))
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	var wg sync.WaitGroup
	segFiles := make([]string, len(segments))
	errCh := make(chan error, len(segments))
	sem := make(chan struct{}, 8) // 限制并发

	for i, seg := range segments {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, segURL string) {
			defer wg.Done()
			defer func() { <-sem }()
			segFile := filepath.Join(tmpDir, fmt.Sprintf("seg_%05d.ts", idx))
			if err := utils.DownloadFile(d.Client, segURL, segFile, headers, nil); err != nil {
				errCh <- err
				return
			}
			segFiles[idx] = segFile
		}(i, seg)
	}
	wg.Wait()
	close(errCh)

	for e := range errCh {
		if e != nil {
			return "", fmt.Errorf("分片下载失败: %w", e)
		}
	}

	// 4. 合并分片
	fmt.Printf("  合并 %d 个分片...\n", len(segFiles))
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer out.Close()

	for _, sf := range segFiles {
		f, err := os.Open(sf)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(out, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}
	fmt.Printf("  合并完成: %s\n", dest)
	return dest, nil
}

// parseM3U8 解析 m3u8 播放列表，返回分片 URL 列表
func parseM3U8(baseURL, content string) ([]string, error) {
	var segments []string
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 可能是相对路径
		segURL := utils.NormalizeURL(baseURL, line)
		// 如果是嵌套 m3u8，递归处理（取最高码率）
		if strings.HasSuffix(strings.ToLower(segURL), ".m3u8") {
			// 简化处理：取第一个嵌套的 m3u8
			subPlaylist, err := fetchTextSimple(segURL)
			if err != nil {
				continue
			}
			subSegs, err := parseM3U8(segURL, subPlaylist)
			if err == nil && len(subSegs) > 0 {
				return subSegs, nil
			}
			continue
		}
		segments = append(segments, segURL)
	}
	return segments, nil
}

// fetchTextSimple 简单获取文本内容
func fetchTextSimple(rawURL string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	for k, v := range utils.CommonHeaders() {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// fetchText 获取 URL 文本内容
func (d *Downloader) fetchText(rawURL string, headers map[string]string) (string, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	utils.ApplyHeaders(req, headers)
	resp, err := d.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// copyMap 复制 map
func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// originOf 从 URL 提取 origin (scheme://host)
func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
