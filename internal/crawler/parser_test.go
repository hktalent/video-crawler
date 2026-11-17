package crawler

import (
	"testing"

	"github.com/PuerkitoBio/goquery"
	"strings"
)

// 测试观看次数解析 - "227K 次观看" 等带单位格式
func TestExtractViewCount(t *testing.T) {
	cases := []struct {
		html     string
		expected int64
		desc     string
	}{
		{`<span>227K 次观看</span>`, 227000, "227K 次观看"},
		{`<span>1.5M views</span>`, 1500000, "1.5M views"},
		{`<span>播放量: 25.6万次</span>`, 256000, "25.6万次播放"},
		{`<span>观看次数：3.2亿</span>`, 320000000, "3.2亿观看"},
		{`<span>1,234 views</span>`, 1234, "1,234 views"},
		{`<span>500K plays</span>`, 500000, "500K plays"},
		{`<div>次观看 890K</div>`, 890000, "次观看 890K（反向）"},
		{`<meta itemprop="interactionCount" content="1234567">`, 1234567, "meta interactionCount"},
	}

	for _, c := range cases {
		doc, _ := goquery.NewDocumentFromReader(strings.NewReader(c.html))
		got, raw := extractViewCount(c.html, doc)
		if got != c.expected {
			t.Errorf("[%s] 期望 %d, 实际 %d (raw=%q)", c.desc, c.expected, got, raw)
		} else {
			t.Logf("[%s] ✅ %d (raw=%q)", c.desc, got, raw)
		}
	}
}

// 测试标题提取
func TestExtractTitle(t *testing.T) {
	cases := []struct {
		html     string
		expected string
		desc     string
	}{
		{
			`<html><head><meta property="og:title" content="我的视频标题"></head><body></body></html>`,
			"我的视频标题",
			"og:title 优先",
		},
		{
			`<html><head><title>视频标题 - 某视频站</title></head><body></body></html>`,
			"视频标题",
			"title 清洗站点后缀",
		},
		{
			`<html><head><title>完整标题 没有后缀</title></head><body></body></html>`,
			"完整标题 没有后缀",
			"title 无后缀不清洗",
		},
		{
			`<html><body><h1>这是一个视频标题</h1></body></html>`,
			"这是一个视频标题",
			"h1 标签",
		},
		{
			`<html><head><title>标题A - 站点</title></head><body><h1>标题B</h1></body></html>`,
			"标题B",
			"h1 优先于 title",
		},
	}

	for _, c := range cases {
		doc, _ := goquery.NewDocumentFromReader(strings.NewReader(c.html))
		got := extractTitle(doc)
		if got != c.expected {
			t.Errorf("[%s] 期望 %q, 实际 %q", c.desc, c.expected, got)
		} else {
			t.Logf("[%s] ✅ %q", c.desc, got)
		}
	}
}

// 测试标题清洗
func TestCleanTitle(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"视频标题 - 某站", "视频标题"},
		{"视频标题 _ 某站", "视频标题"},
		{"视频标题 | 某站", "视频标题"},
		{"这是一个很长的完整标题", "这是一个很长的完整标题"},
		{"  多  余  空  白  ", "多 余 空 白"},
		{"标题 - 带空格的站点名", "标题 - 带空格的站点名"}, // 后缀含空格，不清洗
	}

	for _, c := range cases {
		got := cleanTitle(c.input)
		if got != c.expected {
			t.Errorf("cleanTitle(%q) = %q, 期望 %q", c.input, got, c.expected)
		}
	}
}

// 测试无意义标题判断
func TestIsMeaningfulTitle(t *testing.T) {
	cases := []struct {
		input    string
		expected bool
	}{
		{"播放", false},
		{"下载", false},
		{"play", false},
		{"这是视频标题", true},
		{"", false},
		{"ab", false},
		{"abc", true},
	}

	for _, c := range cases {
		got := isMeaningfulTitle(c.input)
		if got != c.expected {
			t.Errorf("isMeaningfulTitle(%q) = %v, 期望 %v", c.input, got, c.expected)
		}
	}
}
