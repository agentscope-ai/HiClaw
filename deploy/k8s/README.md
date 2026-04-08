# HiClaw Kubernetes Deployment

> 在 Kubernetes 集群上部署 HiClaw 多 Agent 协作平台

## 快速开始

```bash
# 1. 修改配置
vim secrets.yaml  # 设置 LLM API Key 和密码

# 2. 一键部署
kubectl apply -k .

# 3. 配置 hosts 文件
# 获取 Ingress IP
kubectl get ingress -n hiclaw

# 添加到 /etc/hosts
# <INGRESS_IP> element.hiclaw.local matrix.hiclaw.local gateway.hiclaw.local
```

## 文件说明

| 文件 | 说明 |
|------|------|
| `kustomization.yaml` | Kustomize 配置文件 |
| `namespace.yaml` | 命名空间定义 |
| `secrets.yaml` | 敏感信息 Secret |
| `minio.yaml` | MinIO 文件存储 |
| `matrix.yaml` | Matrix (Tuwunel) IM 服务 |
| `higress.yaml` | Higress AI Gateway |
| `element.yaml` | Element Web 前端 |
| `manager.yaml` | HiClaw Manager |
| `ingress.yaml` | Ingress 路由规则 |
| `deploy.sh` | 一键部署脚本 |

## 详细文档

参见 [K8s 部署教程](../../docs/zh-cn/deployment/hiclaw-k8s-deployment-tutorial.md)

## 相关 Issue

- [#245](https://github.com/alibaba/hiclaw/issues/245) - K8s 部署模式支持请求

## 贡献

欢迎提交 Issue 和 PR 来完善 K8s 部署方案！