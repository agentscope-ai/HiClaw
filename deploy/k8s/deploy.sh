#!/bin/bash
# HiClaw K8s 一键部署脚本

set -e

echo "🦞 HiClaw Kubernetes 部署脚本"
echo "=============================="

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 检查 kubectl
if ! command -v kubectl &> /dev/null; then
    echo -e "${RED}错误: kubectl 未安装${NC}"
    echo "请先安装 kubectl: https://kubernetes.io/docs/tasks/tools/"
    exit 1
fi

# 检查集群连接
if ! kubectl cluster-info &> /dev/null; then
    echo -e "${RED}错误: 无法连接到 Kubernetes 集群${NC}"
    echo "请检查 kubeconfig 配置"
    exit 1
fi

echo -e "${GREEN}✓ Kubernetes 集群连接正常${NC}"

# 提示输入配置
echo ""
echo "请输入配置信息："
echo "----------------"

read -p "LLM API Key: " LLM_API_KEY
if [ -z "$LLM_API_KEY" ]; then
    echo -e "${RED}错误: LLM API Key 不能为空${NC}"
    exit 1
fi

read -p "管理员密码 [自动生成]: " ADMIN_PASSWORD
if [ -z "$ADMIN_PASSWORD" ]; then
    ADMIN_PASSWORD=$(openssl rand -base64 16)
    echo -e "${YELLOW}已生成管理员密码: $ADMIN_PASSWORD${NC}"
fi

read -p "MinIO 密码 [minioadmin]: " MINIO_PASSWORD
MINIO_PASSWORD=${MINIO_PASSWORD:-minioadmin}

# 更新 Secret
echo ""
echo "正在更新配置..."
sed -i.bak "s/your-api-key-here/$LLM_API_KEY/g" secrets.yaml
sed -i.bak "s/change-me-secure-password/$ADMIN_PASSWORD/g" secrets.yaml
sed -i.bak "s/minioadmin/$MINIO_PASSWORD/g" secrets.yaml

# 部署
echo ""
echo "开始部署..."
kubectl apply -k .

# 等待 Pod 就绪
echo ""
echo "等待 Pod 就绪..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name -n hiclaw --timeout=300s || true

# 显示状态
echo ""
echo "=============================="
echo -e "${GREEN}✓ 部署完成！${NC}"
echo ""
echo "访问地址："
echo "  Element Web:     http://element.hiclaw.local"
echo "  Higress 控制台:  http://higress-console.hiclaw.local"
echo "  OpenClaw 控制台: http://openclaw-console.hiclaw.local"
echo "  MinIO 控制台:    http://minio-console.hiclaw.local"
echo ""
echo "凭据信息："
echo "  管理员用户名: admin"
echo "  管理员密码: $ADMIN_PASSWORD"
echo "  MinIO 用户名: minioadmin"
echo "  MinIO 密码: $MINIO_PASSWORD"
echo ""
echo "请将以下内容添加到 /etc/hosts："
echo "  <INGRESS_IP> element.hiclaw.local matrix.hiclaw.local gateway.hiclaw.local"
echo "  <INGRESS_IP> higress-console.hiclaw.local openclaw-console.hiclaw.local"
echo "  <INGRESS_IP> minio.hiclaw.local minio-console.hiclaw.local"
echo ""
echo "获取 Ingress IP: kubectl get ingress -n hiclaw"