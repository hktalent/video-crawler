package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"video-crawler/internal/crawler"
)

// 版本信息（通过 -ldflags -X 注入）
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "显示版本信息")
	startURL := flag.String("url", "", "目标网站 URL（必填）")
	outDir := flag.String("out", "./downloads", "视频保存目录")
	minViews := flag.Int64("min-views", 100000, "最小观看次数阈值")
	maxDepth := flag.Int("depth", 2, "最大爬取深度")
	maxPages := flag.Int("max-pages", 50, "最大抓取页面数（0 表示不限制）")
	workers := flag.Int("workers", 4, "并发爬取数")
	sameHost := flag.Bool("same-host", true, "是否只爬取同域页面")
	cookieStr := flag.String("cookie", "", "请求 Cookie 字符串")
	userAgent := flag.String("ua", "", "自定义 User-Agent")

	// JS 渲染相关参数
	render := flag.Bool("render", false, "启用 chromedp JS 渲染（支持 SPA 站点）")
	headless := flag.Bool("headless", true, "渲染时是否使用无头浏览器")
	renderWait := flag.Duration("render-wait", 1*time.Second, "渲染稳定后额外等待时间")
	renderTimeout := flag.Duration("render-timeout", 45*time.Second, "单页面渲染超时时间")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "视频爬虫 - 自动识别并下载高播放量视频\n\n")
		fmt.Fprintf(os.Stderr, "用法: %s -url <网站URL> [选项]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "选项:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n示例:\n")
		fmt.Fprintf(os.Stderr, "  %s -url https://example.com/videos -min-views 100000 -out ./videos\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -url https://spa-site.com -render -depth 3 -workers 2\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -url https://example.com -cookie \"sid=xxx\" -ua \"Mozilla/5.0...\"\n", os.Args[0])
	}

	flag.Parse()

	if *showVersion {
		fmt.Printf("Video Crawler %s\n", Version)
		fmt.Printf("  Commit:    %s\n", Commit)
		fmt.Printf("  BuildTime: %s\n", BuildTime)
		os.Exit(0)
	}

	if *startURL == "" {
		fmt.Fprintln(os.Stderr, "错误: 必须通过 -url 指定目标网站 URL")
		flag.Usage()
		os.Exit(1)
	}

	// 确保输出目录存在
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建输出目录失败: %v\n", err)
		os.Exit(1)
	}

	cfg := crawler.Config{
		StartURL:  *startURL,
		OutDir:    *outDir,
		MinViews:  *minViews,
		MaxDepth:  *maxDepth,
		MaxPages:  *maxPages,
		Workers:   *workers,
		SameHost:  *sameHost,
		CookieStr: *cookieStr,
		UserAgent: *userAgent,
		Render: crawler.RenderConfig{
			Enabled:   *render,
			Headless:  *headless,
			WaitTime:  *renderWait,
			Timeout:   *renderTimeout,
			UserAgent: *userAgent,
		},
	}

	c, err := crawler.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化爬虫失败: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	// 优雅退出
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n⏹️  收到中断信号，正在停止...")
		cancel()
	}()

	start := time.Now()
	c.Run(ctx)
	elapsed := time.Since(start)

	// 输出结果摘要
	results := c.Results()
	fmt.Printf("\n📊 任务完成摘要\n")
	fmt.Printf("   耗时: %s\n", elapsed.Round(time.Second))
	fmt.Printf("   命中视频数: %d\n", len(results))
	fmt.Printf("   保存目录: %s\n", *outDir)
	if len(results) > 0 {
		fmt.Printf("\n📋 已下载视频列表:\n")
		for i, v := range results {
			fmt.Printf("   %d. %s (观看: %d)\n", i+1, v.Title, v.Views)
		}
	}
}
