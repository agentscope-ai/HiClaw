#!/bin/bash
# setup-yuanbao.sh - 元宝龙虾集成一键配置脚本
#
# 用法:
#   ./setup-yuanbao.sh --appid <AppID> --appsecret <AppSecret>
#
# 参数:
#   --appid      元宝 AppID（从元宝 App 配置面板获取）
#   --appsecret  元宝 AppSecret（从元宝 App 配置面板获取）
#
# 示例:
#   ./setup-yuanbao.sh --appid "your_app_id" --appsecret "your_app_secret"

set -e

# 解析参数
APPID=""
APPSECRET=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --appid)
            APPID="$2"
            shift 2
            ;;
        --appsecret)
            APPSECRET="$2"
            shift 2
            ;;
        *)
            echo "未知参数: $1"
            exit 1
            ;;
    esac
done

if [[ -z "$APPID" || -z "$APPSECRET" ]]; then
    echo "用法: $0 --appid <AppID> --appsecret <AppSecret>"
    echo ""
    echo "请从元宝 App「关联已有 OpenClaw」面板获取 AppID 和 AppSecret"
    exit 1
fi

echo "=== 元宝龙虾集成配置 ==="
echo ""

# 步骤 1: 安装插件
echo "[1/4] 安装元宝龙虾插件..."
openclaw plugins install "openclaw-plugin-yuanbao@latest"

# 步骤 2: 配置通道
echo "[2/4] 配置元宝通道..."
openclaw channels add --channel yuanbao --token "${APPID}:${APPSECRET}"

# 步骤 3: 重启服务
echo "[3/4] 重启 OpenClaw 服务..."
openclaw gateway restart

# 步骤 4: 完成
echo "[4/4] 配置完成！"
echo ""
echo "请在元宝 App 中点击「我已操作」完成关联。"
echo ""
echo "验证命令:"
echo "  openclaw plugins list"
echo "  openclaw channels list"
