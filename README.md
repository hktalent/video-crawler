# Video Crawler — 通用视频爬虫

自动识别任意网站的视频资源，按观看次数阈值筛选并下载到本地。支持静态页面和 JS 渲染（SPA）站点。

## 功能特性

- **多源视频识别**：`<video>`/`<source>` 标签、`og:video` meta、JSON-LD `VideoObject`、`<a>` 视频链接、iframe 嵌入、内联脚本兜底正则
- **观看次数解析**：JSON-LD `interactionCount`、通用正则匹配（播放量/观看/views）、中英文单位换算（万/亿/K/M/B）
- **观看次数过滤**：默认只下载播放量 ≥ 10 万的视频，阈值可自定义
- **JS 渲染支持**：集成 chromedp，自动检测 SPA 空壳页面并降级渲染，也可强制开启
- **多格式下载**：直链视频（mp4/webm/mov 等）直接下载；m3u8/HLS 优先用 ffmpeg，无 ffmpeg 时自行分片下载合并
- **并发爬取**：支持自定义并发数、爬取深度、同域限制、URL 去重
- **反爬适配**：可注入 Cookie、自定义 User-Agent、自动处理 gzip 压缩

## 快速开始

### 环境要求

- Go 1.21+
- Chrome / Chromium（仅 JS 渲染时需要，系统已安装即可）
- ffmpeg（推荐，用于 m3u8 下载；不装则回退到自行分片下载）

### 编译

```bash
# 编译当前平台（带优化参数）
go build -trimpath -ldflags="-s -w" -o video-crawler .

# 或使用脚本编译多平台
./build.sh all          # 编译全部平台
./build.sh linux-amd64  # 编译指定平台
./build.sh all upx      # 编译后用 upx 压缩
```

### 运行

```bash
# 基本用法
./video-crawler -url https://example.com/videos

# 指定输出目录和观看次数阈值
./video-crawler -url https://example.com -out ./videos -min-views 100000

# SPA 站点：强制 JS 渲染
./video-crawler -url https://spa-site.com -render

# 携带 Cookie 访问需要登录的页面
./video-crawler -url https://example.com -cookie "sessionid=xxx; csrftoken=yyy"
```

## 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-url` | （必填） | 目标网站 URL |
| `-out` | `./downloads` | 视频保存目录 |
| `-min-views` | `100000` | 最小观看次数阈值 |
| `-depth` | `2` | 最大爬取深度 |
| `-max-pages` | `50` | 最大抓取页面数（0 不限制） |
| `-workers` | `4` | 并发爬取数 |
| `-same-host` | `true` | 是否只爬取同域页面 |
| `-cookie` | — | 请求 Cookie 字符串 |
| `-ua` | Safari UA | 自定义 User-Agent |
| `-render` | `false` | 强制启用 chromedp JS 渲染 |
| `-headless` | `true` | 渲染时是否无头模式 |
| `-render-wait` | `1s` | 渲染稳定后额外等待时间 |
| `-render-timeout` | `45s` | 单页面渲染超时 |

## 使用示例

```bash
# 1. 爬取视频列表页，只下载 10 万+ 播放的视频
./video-crawler -url https://example.com/videos -out ./videos

# 2. 爬取纯 SPA 站点（React/Vue 等前端渲染）
./video-crawler -url https://spa-site.com -render -depth 3 -workers 2

# 3. 自动模式（推荐）：静态页走 HTTP，SPA 页自动降级渲染
./video-crawler -url https://example.com

# 4. 需要登录的站点：从浏览器复制 Cookie 传入
./video-crawler -url https://member-site.com -cookie "uid=123; token=abc"

# 5. 调试渲染：有头浏览器 + 延长等待
./video-crawler -url https://complex-spa.com -render -headless=false -render-wait 3s

# 6. 跨站爬取（关闭同域限制）
./video-crawler -url https://example.com -same-host=false -max-pages 200
```

## 项目结构

```
video-crawler/
├── main.go                     # 命令行入口
├── build.sh                    # 多平台优化编译脚本
├── internal/
│   ├── crawler/
│   │   ├── crawler.go          # 爬虫调度（并发/深度/去重/降级）
│   │   ├── parser.go           # 视频识别与观看次数解析
│   │   └── renderer.go         # chromedp JS 渲染器
│   ├── downloader/
│   │   └── downloader.go       # 下载器（直链 + ffmpeg/HLS 分片）
│   └── utils/
│       └── utils.go            # 工具函数（URL/单位换算/下载）
├── go.mod
├── go.sum
└── .gitignore
```

## 渲染策略

爬虫对页面抓取采用三级策略：

1. **强制渲染模式**（`-render`）：所有页面用 chromedp 渲染，适合纯 SPA 站点
2. **自动 SPA 检测降级**（默认）：优先 HTTP 抓取，当页面存在 SPA 挂载点（`id=root/app/__next/__nuxt`）且正文稀少时自动启动 chromedp
3. **HTTP 失败降级**：HTTP 请求失败（网络错误/403 等）时自动尝试渲染

## 编译优化说明

`build.sh` 采用以下优化参数：

| 参数 | 作用 |
|------|------|
| `-trimpath` | 去除编译路径信息，提高可重现性和安全性 |
| `-ldflags="-s"` | 去除符号表 |
| `-ldflags="-w"` | 去除 DWARF 调试信息 |
| `-ldflags="-buildid="` | 去除 buildid |
| `CGO_ENABLED=0` | 静态编译，不依赖系统 C 库，可跨平台运行 |
| `upx --best --lzma` | （可选）UPX 压缩，进一步减小体积约 40-60% |

优化后二进制体积约 8MB（原始 12.5MB），UPX 压缩后约 3-4MB。

## 注意事项

1. **观看次数匹配精度**：通用爬虫无法将页面上的观看次数文本精确关联到每个视频。JSON-LD 中明确标注的 `interactionCount` 优先级最高，其余视频共享页面级观看次数。结构化站点精度最高。
2. **反爬限制**：遇到 403/JS 跳转时，可通过 `-cookie` 传入浏览器 Cookie，或用 `-ua` 自定义 UA。渲染模式对反爬更友好。
3. **合规使用**：仅下载你有权访问的公开资源，遵守目标站点的 `robots.txt` 和服务条款。
4. **资源占用**：JS 渲染会启动 Chrome 进程，内存占用较高；普通静态页面不会启动 Chrome。

## License

MIT
