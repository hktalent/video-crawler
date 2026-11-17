package crawler

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// RenderConfig chromedp 渲染配置
type RenderConfig struct {
	Enabled    bool          // 是否启用 JS 渲染
	Headless   bool          // 是否无头模式
	WaitTime   time.Duration // 渲染稳定后额外等待时间
	Timeout    time.Duration // 页面加载超时
	UserAgent  string        // 自定义 UA
	WindowSize [2]int        // 窗口大小 [width, height]
}

// Renderer 封装 chromedp 浏览器实例
type Renderer struct {
	cfg       RenderConfig
	allocator context.Context
	cancel    context.CancelFunc
}

// NewRenderer 创建渲染器（启动浏览器实例，可复用）
func NewRenderer(cfg RenderConfig) (*Renderer, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", cfg.Headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("mute-audio", true),
		chromedp.WindowSize(cfg.WindowSize[0], cfg.WindowSize[1]),
	)
	if cfg.UserAgent != "" {
		opts = append(opts, chromedp.UserAgent(cfg.UserAgent))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	return &Renderer{
		cfg:       cfg,
		allocator: allocCtx,
		cancel:    cancel,
	}, nil
}

// Close 关闭浏览器实例
func (r *Renderer) Close() {
	if r != nil && r.cancel != nil {
		r.cancel()
	}
}

// RenderPage 渲染页面并返回 HTML
// 通过轮询 documentElement.outerHTML 长度判断渲染是否稳定
func (r *Renderer) RenderPage(ctx context.Context, rawURL string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("renderer not enabled")
	}

	// 创建带超时的 tab context
	timeoutCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	tabCtx, tabCancel := chromedp.NewContext(r.allocator)
	defer tabCancel()

	// 1. 导航
	if _, err := chromedp.Run(tabCtx, chromedp.Navigate(rawURL)); err != nil {
		return "", fmt.Errorf("navigate failed: %w", err)
	}

	// 2. 轮询等待渲染稳定
	deadline := time.Now().Add(r.cfg.Timeout)
	var lastLen int
	stableCount := 0
	for time.Now().Before(deadline) {
		select {
		case <-timeoutCtx.Done():
			// 超时，取当前内容
			return r.getHTML(tabCtx)
		default:
		}

		cur, err := r.getHTML(tabCtx)
		if err != nil {
			return "", fmt.Errorf("get html failed: %w", err)
		}

		if len(cur) == lastLen {
			stableCount++
		} else {
			stableCount = 0
			lastLen = len(cur)
		}

		// 连续 2 次长度不变，认为渲染稳定
		if stableCount >= 2 {
			time.Sleep(r.cfg.WaitTime)
			// 再取一次确认
			finalHTML, err := r.getHTML(tabCtx)
			if err != nil {
				return "", err
			}
			if len(finalHTML) == lastLen {
				return finalHTML, nil
			}
			lastLen = len(finalHTML)
			stableCount = 0
		}

		time.Sleep(300 * time.Millisecond)
	}

	// 超时返回最后内容
	return r.getHTML(tabCtx)
}

// getHTML 获取当前页面完整 HTML
func (r *Renderer) getHTML(tabCtx context.Context) (string, error) {
	html, err := chromedp.Run(tabCtx, chromedp.OuterHTML("html"))
	if err != nil {
		return "", err
	}
	return html, nil
}

// needsRender 判断 HTTP 抓取的内容是否需要 JS 渲染
// 启发式规则：body 中存在 SPA 挂载点且除 script/style 外几乎无实质内容
func needsRender(html string) bool {
	low := strings.ToLower(strings.TrimSpace(html))

	// 提取 body 内容
	bodyStart := strings.Index(low, "<body")
	if bodyStart < 0 {
		return false
	}
	bodyContent := low[bodyStart:]

	// 移除所有 <script>...</script> 标签及内容
	scriptRe := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	bodyNoScript := scriptRe.ReplaceAllString(bodyContent, "")
	// 也移除 <style> 标签
	styleRe := regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`)
	bodyNoScript = styleRe.ReplaceAllString(bodyNoScript, "")

	// 检查是否有 SPA 挂载点
	spaMarkers := []string{
		`<div id="root"`,
		`<div id="app"`,
		`<div id="__next"`,
		`<div id="__nuxt"`,
		`<div id="__layout"`,
	}
	hasSPAMarker := false
	for _, m := range spaMarkers {
		if strings.Contains(bodyNoScript, m) {
			hasSPAMarker = true
			break
		}
	}
	if !hasSPAMarker {
		return false
	}

	// 去掉 script/style 后，body 正文纯文本内容很少 → 疑似 SPA 空壳
	textOnly := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(bodyNoScript, "")
	textOnly = strings.TrimSpace(textOnly)
	return len(textOnly) < 300
}
