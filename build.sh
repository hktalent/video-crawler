#!/usr/bin/env bash
# 视频爬虫 - 优化编译脚本
# 用法: ./build.sh [平台] [压缩]
#   平台: all(default) | darwin-amd64 | darwin-arm64 | linux-amd64 | linux-arm64 | windows-amd64
#   压缩: upx (使用 upx 进一步压缩二进制，需已安装 upx)

set -euo pipefail

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# 项目根目录（脚本所在目录）
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$PROJECT_DIR"

# 输出目录
OUTPUT_DIR="${PROJECT_DIR}/dist"
mkdir -p "$OUTPUT_DIR"

# 版本信息（从 git 获取，失败则用时间戳）
if command -v git >/dev/null 2>&1 && git rev-parse --git-dir >/dev/null 2>&1; then
    VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
    COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    BUILD_TIME="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
else
    VERSION="dev"
    COMMIT="unknown"
    BUILD_TIME="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
fi

# 优化链接参数
#   -s  去除符号表
#   -w  去除 DWARF 调试信息
#   -buildid=  去除 buildid
#   -X 注入版本信息
LDFLAGS="-s -w -buildid= \
    -X main.Version=${VERSION} \
    -X main.Commit=${COMMIT} \
    -X main.BuildTime=${BUILD_TIME}"

# 编译参数
#   -trimpath  去除文件系统路径，提高可重现性和安全性
#   -ldflags   链接参数
#   CGO_ENABLED=0  静态编译，不依赖系统 C 库
BUILD_FLAGS="-trimpath -ldflags \"${LDFLAGS}\""

# 支持的目标平台
TARGETS=(
    "darwin:amd64:video-crawler-darwin-amd64"
    "darwin:arm64:video-crawler-darwin-arm64"
    "linux:amd64:video-crawler-linux-amd64"
    "linux:arm64:video-crawler-linux-arm64"
    "windows:amd64:video-crawler-windows-amd64.exe"
)

# 解析参数
TARGET="${1:-all}"
COMPRESS="${2:-}"

# 构建单个平台
build_one() {
    local goos="$1"
    local goarch="$2"
    local outname="$3"
    local outpath="${OUTPUT_DIR}/${outname}"

    info "构建 ${goos}/${goarch} -> ${outname}"

    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -trimpath -ldflags "${LDFLAGS}" -o "$outpath" .

    local size
    size="$(stat -f%z "$outpath" 2>/dev/null || stat -c%s "$outpath" 2>/dev/null || echo 0)"
    info "  完成: ${outname} ($(numfmt --to=iec "$size" 2>/dev/null || echo "${size} B"))"

    # 冒烟测试：仅对当前主机可直接执行的产物验证，避免打包出一个跑不起来的二进制
    if [[ "$goos" == "$(go env GOOS)" && "$goarch" == "$(go env GOARCH)" ]]; then
        if ! "$outpath" -version >/dev/null 2>&1; then
            error "冒烟测试失败：${outname} 无法正常运行"
        fi
        info "  冒烟测试通过 (-version)"
    fi

    # UPX 压缩已禁用（所有平台）
    #   Go 运行时要求在自身线程上持有有效的 g。UPX 之类的壳在运行时会用到外来线程
    #  （非 Go 创建的线程），信号一旦落在这些线程上，无法挂接 m，运行时直接打印
    #  “fatal: bad g in signal handler” 并 exit 2 崩溃（CGO_ENABLED=0 时必崩）。
    if [[ "$COMPRESS" == "upx" ]]; then
        warn "  已跳过 UPX：Go 二进制压缩后运行会崩溃（fatal: bad g in signal handler）"
    fi
}

# 主流程
info "视频爬虫优化编译"
info "版本: ${VERSION} | Commit: ${COMMIT} | 构建时间: ${BUILD_TIME}"
info "输出目录: ${OUTPUT_DIR}"
echo

case "$TARGET" in
    all)
        for t in "${TARGETS[@]}"; do
            IFS=':' read -r goos goarch outname <<< "$t"
            build_one "$goos" "$goarch" "$outname"
        done
        ;;
    darwin-amd64|darwin-arm64|linux-amd64|linux-arm64|windows-amd64)
        IFS='-' read -r goos goarch <<< "$TARGET"
        if [[ "$goos" == "windows" ]]; then
            outname="video-crawler-${TARGET}.exe"
        else
            outname="video-crawler-${TARGET}"
        fi
        build_one "$goos" "$goarch" "$outname"
        ;;
    *)
        error "未知平台: ${TARGET}"
        echo "支持: all | darwin-amd64 | darwin-arm64 | linux-amd64 | linux-arm64 | windows-amd64"
        exit 1
        ;;
esac

echo
info "✅ 全部构建完成"
ls -lh "$OUTPUT_DIR"
