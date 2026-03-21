#!/bin/bash
# run-hiclaw-test.sh - 快速运行 HiClaw 测试
# 用法: run-hiclaw-test.sh [options] [test-filter]
#
# Options:
#   --repo-dir <path>     指定 HiClaw 仓库目录 (默认: 当前目录或 /tmp/hiclaw)
#   --env-file <path>     指定环境配置文件 (默认: ~/hiclaw-manager.env)
#   --skip-pull           跳过 git pull
#   existing              # 使用现有安装运行测试
#
# Examples:
#   run-hiclaw-test.sh                        # 运行所有测试
#   run-hiclaw-test.sh "01 02 03"             # 仅运行测试 01, 02, 03
#   run-hiclaw-test.sh existing               # 使用现有安装运行测试
#   run-hiclaw-test.sh --repo-dir ~/hiclaw   # 指定仓库目录

set -e

# 默认值（可通过环境变量覆盖）
REPO_DIR="${HICLAW_REPO_DIR:-}"
ENV_FILE="${HICLAW_ENV_FILE:-$HOME/hiclaw-manager.env}"
SKIP_PULL=false
TEST_FILTER=""

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 解析参数
while [[ $# -gt 0 ]]; do
    case $1 in
        --repo-dir)
            REPO_DIR="$2"
            shift 2
            ;;
        --env-file)
            ENV_FILE="$2"
            shift 2
            ;;
        --skip-pull)
            SKIP_PULL=true
            shift
            ;;
        existing)
            TEST_FILTER="existing"
            shift
            ;;
        *)
            TEST_FILTER="$1"
            shift
            ;;
    esac
done

# 自动检测仓库目录
detect_repo_dir() {
    if [ -n "$REPO_DIR" ]; then
        return
    fi
    
    # 检查当前目录
    if [ -f "./Makefile" ] && grep -q "hiclaw" ./Makefile 2>/dev/null; then
        REPO_DIR="$(pwd)"
        return
    fi
    
    # 检查标准位置
    for dir in "./hiclaw" "../hiclaw" "/tmp/hiclaw" "$HOME/hiclaw"; do
        if [ -d "$dir" ] && [ -f "$dir/Makefile" ]; then
            REPO_DIR="$dir"
            return
        fi
    done
    
    # 默认使用 /tmp/hiclaw
    REPO_DIR="/tmp/hiclaw"
}

# 检查环境
check_prerequisites() {
    if [ ! -f "$ENV_FILE" ]; then
        log_error "Config file not found: $ENV_FILE"
        log_info "Please create hiclaw-manager.env first or set HICLAW_ENV_FILE"
        exit 1
    fi
    
    if ! command -v docker &> /dev/null; then
        log_error "Docker not installed"
        exit 1
    fi
}

# 克隆/更新代码
update_repo() {
    if [ ! -d "$REPO_DIR" ]; then
        log_info "Cloning HiClaw repository to $REPO_DIR..."
        git clone https://github.com/alibaba/hiclaw.git "$REPO_DIR"
        cd "$REPO_DIR"
    elif [ "$SKIP_PULL" = true ]; then
        log_info "Skipping git pull (--skip-pull)"
        cd "$REPO_DIR"
    else
        log_info "Updating HiClaw repository at $REPO_DIR..."
        cd "$REPO_DIR"
        git fetch origin
        git reset --hard origin/main
    fi
    
    log_info "Repository ready at $REPO_DIR"
}

# 运行测试
run_tests() {
    cd "$REPO_DIR"
    
    # 加载环境变量
    set -a
    source "$ENV_FILE"
    set +a
    
    export HICLAW_YOLO=1
    
    if [ "$TEST_FILTER" = "existing" ]; then
        # 使用现有安装
        log_info "Running tests with existing installation..."
        ./tests/run-all-tests.sh --skip-build --use-existing
    elif [ -n "$TEST_FILTER" ]; then
        # 运行指定测试
        log_info "Running tests: $TEST_FILTER"
        ./tests/run-all-tests.sh --test-filter "$TEST_FILTER"
    else
        # 运行完整测试流程
        log_info "Running full test cycle (make test)..."
        make test
    fi
}

# 显示结果
show_results() {
    echo ""
    log_info "=== Test Results ==="
    
    if [ -d "$REPO_DIR/tests/output" ]; then
        echo "Metrics files:"
        ls -la "$REPO_DIR/tests/output/"*.json 2>/dev/null || echo "  No metrics files found"
    fi
    
    echo ""
    echo "To debug issues, run:"
    echo "  hiclaw-debug.sh analyze"
}

# 主流程
main() {
    log_info "=== HiClaw Test Runner ==="
    
    detect_repo_dir
    log_info "Using repository: $REPO_DIR"
    
    check_prerequisites
    update_repo
    run_tests
    show_results
}

main "$@"
