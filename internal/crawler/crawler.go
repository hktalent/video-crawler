package crawler

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"video-crawler/internal/downloader"
	"video-crawler/internal/utils"
)

// Config 爬虫配置
type Config struct {
	StartURL  string // 起始 URL
	OutDir    string // 输出目录
	MinViews  int64  // 最小观看次数
	MaxDepth  int    // 最大爬取深度
	MaxPages  int    // 最大页面数（0 表示不限制）
	Workers   int    // 并发数
	SameHost  bool   // 是否只爬同域
	CookieStr string // Cookie 字符串
	UserAgent string // 自定义 UA
	Render    RenderConfig // JS 渲染配置
}

// Crawler 爬虫主体
type Crawler struct {
	cfg        Config
	client     *http.Client
	headers    map[string]string
	downloader *downloader.Downloader
	renderer   *Renderer
	rendererMu sync.Mutex

	visited   map[string]bool
	visitedMu sync.Mutex

	pageCount   int
	pageCountMu sync.Mutex

	results   []VideoInfo
	resultsMu sync.Mutex

	wg sync.WaitGroup
}

// New 创建爬虫
func New(cfg Config) (*Crawler, error) {
	headers := utils.CommonHeaders()
	if cfg.UserAgent != "" {
		headers["User-Agent"] = cfg.UserAgent
	}
	if cfg.CookieStr != "" {
		headers["Cookie"] = cfg.CookieStr
	}

	// 设置渲染默认值
	if cfg.Render.Timeout == 0 {
		cfg.Render.Timeout = 45 * time.Second
	}
	if cfg.Render.WaitTime == 0 {
		cfg.Render.WaitTime = 1 * time.Second
	}
	if cfg.Render.WindowSize[0] == 0 {
		cfg.Render.WindowSize = [2]int{1920, 1080}
	}
	// 渲染器 UA 与 HTTP 请求保持一致（用户未指定时用默认 Safari UA）
	if cfg.Render.UserAgent == "" {
		cfg.Render.UserAgent = headers["User-Agent"]
	}

	c := &Crawler{
		cfg:        cfg,
		client:     &http.Client{Timeout: 60 * time.Second},
		headers:    headers,
		downloader: downloader.New(cfg.OutDir),
		visited:    make(map[string]bool),
	}
	if cfg.Workers <= 0 {
		c.cfg.Workers = 4
	}
	if cfg.MaxDepth <= 0 {
		c.cfg.MaxDepth = 2
	}

	// 仅在强制渲染模式下立即初始化渲染器；否则懒加载（检测到 SPA 时才启动 Chrome）
	if cfg.Render.Enabled {
		renderer, err := NewRenderer(cfg.Render)
		if err != nil {
			return nil, fmt.Errorf("init renderer failed: %w", err)
		}
		c.renderer = renderer
	}

	return c, nil
}

// getRenderer 获取或懒加载创建渲染器（并发安全）
func (c *Crawler) getRenderer() (*Renderer, error) {
	c.rendererMu.Lock()
	defer c.rendererMu.Unlock()
	if c.renderer != nil {
		return c.renderer, nil
	}
	renderCfg := c.cfg.Render
	renderCfg.Enabled = true
	renderer, err := NewRenderer(renderCfg)
	if err != nil {
		return nil, err
	}
	c.renderer = renderer
	return c.renderer, nil
}

// Close 释放资源（关闭浏览器实例）
func (c *Crawler) Close() {
	if c.renderer != nil {
		c.renderer.Close()
	}
}

// Run 启动爬虫
func (c *Crawler) Run(ctx context.Context) {
	fmt.Printf("🚀 启动视频爬虫\n")
	fmt.Printf("   起始 URL: %s\n", c.cfg.StartURL)
	fmt.Printf("   最小观看次数: %d\n", c.cfg.MinViews)
	fmt.Printf("   最大深度: %d\n", c.cfg.MaxDepth)
	fmt.Printf("   输出目录: %s\n", c.cfg.OutDir)
	fmt.Printf("   同域限制: %v\n", c.cfg.SameHost)
	if c.cfg.Render.Enabled {
		fmt.Printf("   JS 渲染: 开启 (headless=%v, 超时=%s)\n", c.cfg.Render.Headless, c.cfg.Render.Timeout)
	} else {
		fmt.Printf("   JS 渲染: 关闭 (自动检测 SPA 时降级)\n")
	}
	fmt.Println()

	sem := make(chan struct{}, c.cfg.Workers)
	c.wg.Add(1)
	go c.crawl(ctx, c.cfg.StartURL, 0, sem)
	c.wg.Wait()

	fmt.Printf("\n✅ 爬取完成，共识别 %d 个视频\n", len(c.results))
}

// crawl 爬取单个页面
func (c *Crawler) crawl(ctx context.Context, rawURL string, depth int, sem chan struct{}) {
	defer c.wg.Done()

	select {
	case <-ctx.Done():
		return
	default:
	}

	// 检查页面数限制
	c.pageCountMu.Lock()
	if c.cfg.MaxPages > 0 && c.pageCount >= c.cfg.MaxPages {
		c.pageCountMu.Unlock()
		return
	}
	c.pageCount++
	c.pageCountMu.Unlock()

	// 去重
	c.visitedMu.Lock()
	if c.visited[rawURL] {
		c.visitedMu.Unlock()
		return
	}
	c.visited[rawURL] = true
	c.visitedMu.Unlock()

	sem <- struct{}{}
	defer func() { <-sem }()

	fmt.Printf("🌐 [%d] 抓取: %s\n", depth, rawURL)

	// 抓取页面（支持 JS 渲染降级）
	html, finalURL, usedRender, err := c.fetchPage(ctx, rawURL)
	if err != nil {
		fmt.Printf("   ⚠️  抓取失败: %v\n", err)
		return
	}
	if usedRender {
		fmt.Printf("   🖥️  已使用 JS 渲染\n")
	}

	// 解析视频
	videos := ParsePage(finalURL, html)
	if len(videos) > 0 {
		fmt.Printf("   📹 识别到 %d 个视频\n", len(videos))
	}

	for _, v := range videos {
		// 过滤观看次数
		if v.Views < c.cfg.MinViews {
			if v.Views > 0 {
				fmt.Printf("   ⏭️  跳过（观看 %d < %d）: %s\n", v.Views, c.cfg.MinViews, v.Title)
			}
			continue
		}
		fmt.Printf("   ✅ 命中（观看 %d）: %s\n", v.Views, v.Title)

		// 去重视频
		c.resultsMu.Lock()
		dup := false
		for _, r := range c.results {
			if r.VideoURL == v.VideoURL {
				dup = true
				break
			}
		}
		if !dup {
			c.results = append(c.results, v)
		}
		c.resultsMu.Unlock()

		if dup {
			continue
		}

		// 下载视频
		dest, err := c.downloader.Download(v.VideoURL, v.PageURL, v.Title)
		if err != nil {
			fmt.Printf("   ❌ 下载失败: %v\n", err)
			continue
		}
		fmt.Printf("   💾 已保存: %s\n", dest)
	}

	// 深度限制
	if depth >= c.cfg.MaxDepth {
		return
	}

	// 提取页面中的链接继续爬取
	links := extractLinks(finalURL, html)
	for _, link := range links {
		// 同域限制
		if c.cfg.SameHost && !utils.IsSameDomain(finalURL, link) {
			continue
		}
		// 跳过非 http 链接
		if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
			continue
		}
		// 跳过视频文件链接（已经处理过）
		if utils.IsVideoURL(link) {
			continue
		}

		c.visitedMu.Lock()
		vis := c.visited[link]
		c.visitedMu.Unlock()
		if vis {
			continue
		}

		c.wg.Add(1)
		go c.crawl(ctx, link, depth+1, sem)
	}
}

// fetchPage 抓取页面 HTML，返回 (html, finalURL, usedRender, error)
// 策略：
//   - 若强制开启渲染，直接用 chromedp
//   - 否则先用 HTTP 抓取；若内容疑似 SPA 空壳或 HTTP 失败，则懒加载 chromedp 降级
func (c *Crawler) fetchPage(ctx context.Context, rawURL string) (string, string, bool, error) {
	// 强制渲染模式
	if c.cfg.Render.Enabled {
		renderer, err := c.getRenderer()
		if err != nil {
			return "", rawURL, false, err
		}
		html, err := renderer.RenderPage(ctx, rawURL)
		if err != nil {
			return "", rawURL, true, err
		}
		return html, rawURL, true, nil
	}

	// HTTP 抓取
	html, finalURL, err := c.fetchHTTP(rawURL)
	if err != nil {
		// HTTP 失败，尝试懒加载渲染器降级
		renderer, rerr := c.getRenderer()
		if rerr == nil {
			fmt.Printf("   ↻ HTTP 失败，尝试 JS 渲染...\n")
			rhtml, rerr2 := renderer.RenderPage(ctx, rawURL)
			if rerr2 != nil {
				return "", rawURL, false, fmt.Errorf("http failed: %w; render failed: %v", err, rerr2)
			}
			return rhtml, rawURL, true, nil
		}
		return "", rawURL, false, err
	}

	// 检测是否需要 JS 渲染
	if needsRender(html) {
		renderer, rerr := c.getRenderer()
		if rerr == nil {
			fmt.Printf("   ↻ 检测到 SPA 空壳，切换 JS 渲染...\n")
			rhtml, rerr2 := renderer.RenderPage(ctx, rawURL)
			if rerr2 != nil {
				// 渲染失败，回退用 HTTP 内容
				return html, finalURL, false, nil
			}
			return rhtml, rawURL, true, nil
		}
		// 渲染器不可用，回退用 HTTP 内容
	}

	return html, finalURL, false, nil
}

// fetchHTTP 纯 HTTP 抓取页面
func (c *Crawler) fetchHTTP(rawURL string) (string, string, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", "", err
	}
	utils.ApplyHeaders(req, c.headers)
	req.Header.Set("Referer", rawURL)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 处理 gzip/br 压缩
	var reader io.Reader = resp.Body
	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		gr, err := gzip.NewReader(resp.Body)
		if err == nil {
			reader = gr
			defer gr.Close()
		}
	case "br":
		// brotli 需要额外依赖，这里直接读取原始数据
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return "", "", err
	}

	// 处理 gzip 但未声明 Content-Encoding 的情况
	if bytes.HasPrefix(body, []byte{0x1f, 0x8b}) {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err == nil {
			body, _ = io.ReadAll(gr)
			gr.Close()
		}
	}

	return string(body), resp.Request.URL.String(), nil
}

// Results 返回识别到的视频结果
func (c *Crawler) Results() []VideoInfo {
	c.resultsMu.Lock()
	defer c.resultsMu.Unlock()
	out := make([]VideoInfo, len(c.results))
	copy(out, c.results)
	return out
}
