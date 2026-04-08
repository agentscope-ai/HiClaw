# HiClaw K8s 部署教程

> 基于 Kubernetes 部署 HiClaw 多 Agent 协作平台

> ⚠️ **注意**：HiClaw 官方目前尚未提供原生 K8s 支持，本教程基于 Docker 架构转换为 K8s 部署方案。相关 Issue: [#245](https://github.com/alibaba/hiclaw/issues/245)

---

## 目录

- [架构概览](#架构概览)
- [前置条件](#前置条件)
- [部署步骤](#部署步骤)
- [验证部署](#验证部署)
- [配置说明](#配置说明)
- [故障排查](#故障排查)
- [生产环境建议](#生产环境建议)

---

## 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                       │
│  ┌─────────────────────────────────────────────────────────┐│
│  │                    hiclaw Namespace                      ││
│  │                                                          ││
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐      ││
│  │  │   Manager   │  │   Worker    │  │   Worker    │      ││
│  │  │  (OpenClaw) │  │   Alice     │  │    Bob      │      ││
│  │  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘      ││
│  │         │                │                │              ││
│  │  ┌──────┴────────────────┴────────────────┴──────┐      ││
│  │  │              Matrix (Tuwunel)                  │      ││
│  │  └───────────────────────────────────────────────┘      ││
│  │                                                          ││
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐      ││
│  │  │   MinIO     │  │  Higress    │  │ Element Web │      ││
│  │  │ (文件存储)   │  │ (AI Gateway)│  │  (前端)     │      ││
│  │  └─────────────┘  └─────────────┘  └─────────────┘      ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```

---

## 前置条件

### 集群要求

| 组件 | 版本要求 |
|------|---------|
| Kubernetes | ≥ 1.24 |
| kubectl | ≥ 1.24 |
| Helm | ≥ 3.0（可选） |

### 资源要求

| 资源 | 最低 | 推荐 |
|------|------|------|
| 节点数 | 1 | 3+ |
| CPU | 4 核 | 8 核 |
| 内存 | 8 GB | 16 GB |
| 存储 | 50 GB | 100 GB |

### 存储类

确保集群有默认 StorageClass：

```bash
kubectl get storageclass
```

---

## 部署步骤

### 第一步：创建命名空间

```bash
kubectl create namespace hiclaw
```

### 第二步：创建 Secret

```bash
# 创建 API Key Secret
kubectl create secret generic hiclaw-secrets \
  --from-literal=LLM_API_KEY=your-api-key-here \
  --from-literal=ADMIN_USER=admin \
  --from-literal=ADMIN_PASSWORD=your-secure-password \
  --from-literal=MINIO_USER=minioadmin \
  --from-literal=MINIO_PASSWORD=minioadmin \
  --namespace hiclaw
```

### 第三步：部署 MinIO

```yaml
# minio.yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: minio-pvc
  namespace: hiclaw
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 20Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: minio
  namespace: hiclaw
spec:
  replicas: 1
  selector:
    matchLabels:
      app: minio
  template:
    metadata:
      labels:
        app: minio
    spec:
      containers:
      - name: minio
        image: minio/minio:latest
        args:
        - server
        - /data
        - --console-address
        - ":9001"
        env:
        - name: MINIO_ROOT_USER
          valueFrom:
            secretKeyRef:
              name: hiclaw-secrets
              key: MINIO_USER
        - name: MINIO_ROOT_PASSWORD
          valueFrom:
            secretKeyRef:
              name: hiclaw-secrets
              key: MINIO_PASSWORD
        ports:
        - containerPort: 9000
          name: api
        - containerPort: 9001
          name: console
        volumeMounts:
        - name: data
          mountPath: /data
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: minio-pvc
---
apiVersion: v1
kind: Service
metadata:
  name: minio
  namespace: hiclaw
spec:
  ports:
  - port: 9000
    name: api
  - port: 9001
    name: console
  selector:
    app: minio
```

```bash
kubectl apply -f minio.yaml
```

### 第四步：部署 Matrix (Tuwunel)

```yaml
# matrix.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tuwunel
  namespace: hiclaw
spec:
  replicas: 1
  selector:
    matchLabels:
      app: tuwunel
  template:
    metadata:
      labels:
        app: tuwunel
    spec:
      containers:
      - name: tuwunel
        image: higress-registry.cn-hangzhou.cr.aliyuncs.com/higress/tuwunel:20260216
        ports:
        - containerPort: 6167
          name: matrix
        - containerPort: 8080
          name: well-known
        env:
        - name: SERVER_NAME
          value: "matrix-local.hiclaw.io:8080"
        volumeMounts:
        - name: data
          mountPath: /data
      volumes:
      - name: data
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: tuwunel
  namespace: hiclaw
spec:
  ports:
  - port: 6167
    name: matrix
  - port: 8080
    name: well-known
  selector:
    app: tuwunel
```

```bash
kubectl apply -f matrix.yaml
```

### 第五步：部署 Higress AI Gateway

```yaml
# higress.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: higress
  namespace: hiclaw
spec:
  replicas: 1
  selector:
    matchLabels:
      app: higress
  template:
    metadata:
      labels:
        app: higress
    spec:
      containers:
      - name: higress-gateway
        image: higress-registry.cn-hangzhou.cr.aliyuncs.com/higress/gateway:latest
        ports:
        - containerPort: 8080
          name: http
        - containerPort: 8443
          name: https
        - containerPort: 15020
          name: metrics
      - name: higress-controller
        image: higress-registry.cn-hangzhou.cr.aliyuncs.com/higress/higress:latest
        ports:
        - containerPort: 8001
          name: console
        env:
        - name: LLM_API_KEY
          valueFrom:
            secretKeyRef:
              name: hiclaw-secrets
              key: LLM_API_KEY
---
apiVersion: v1
kind: Service
metadata:
  name: higress
  namespace: hiclaw
spec:
  type: LoadBalancer
  ports:
  - port: 80
    targetPort: 8080
    name: http
  - port: 443
    targetPort: 8443
    name: https
  - port: 8001
    name: console
  selector:
    app: higress
```

```bash
kubectl apply -f higress.yaml
```

### 第六步：部署 Element Web

```yaml
# element.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: element-web
  namespace: hiclaw
spec:
  replicas: 1
  selector:
    matchLabels:
      app: element-web
  template:
    metadata:
      labels:
        app: element-web
    spec:
      containers:
      - name: element-web
        image: vectorim/element-web:latest
        ports:
        - containerPort: 80
        volumeMounts:
        - name: config
          mountPath: /app/config.json
          subPath: config.json
      volumes:
      - name: config
        configMap:
          name: element-config
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: element-config
  namespace: hiclaw
data:
  config.json: |
    {
      "default_home_server": "matrix-local.hiclaw.io:8080",
      "default_server_config": {
        "m.homeserver": {
          "base_url": "http://tuwunel.hiclaw.svc.cluster.local:8080"
        }
      }
    }
---
apiVersion: v1
kind: Service
metadata:
  name: element-web
  namespace: hiclaw
spec:
  ports:
  - port: 80
  selector:
    app: element-web
```

```bash
kubectl apply -f element.yaml
```

### 第七步：部署 Manager Agent

```yaml
# manager.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hiclaw-manager
  namespace: hiclaw
spec:
  replicas: 1
  selector:
    matchLabels:
      app: hiclaw-manager
  template:
    metadata:
      labels:
        app: hiclaw-manager
    spec:
      containers:
      - name: manager
        image: higress-registry.cn-hangzhou.cr.aliyuncs.com/hiclaw/manager:latest
        ports:
        - containerPort: 8080
        env:
        - name: HICLAW_LLM_API_KEY
          valueFrom:
            secretKeyRef:
              name: hiclaw-secrets
              key: LLM_API_KEY
        - name: HICLAW_ADMIN_USER
          valueFrom:
            secretKeyRef:
              name: hiclaw-secrets
              key: ADMIN_USER
        - name: HICLAW_ADMIN_PASSWORD
          valueFrom:
            secretKeyRef:
              name: hiclaw-secrets
              key: ADMIN_PASSWORD
        - name: HICLAW_MATRIX_DOMAIN
          value: "matrix-local.hiclaw.io:8080"
        - name: HICLAW_MINIO_ENDPOINT
          value: "minio.hiclaw.svc.cluster.local:9000"
        - name: HICLAW_MINIO_USER
          valueFrom:
            secretKeyRef:
              name: hiclaw-secrets
              key: MINIO_USER
        - name: HICLAW_MINIO_PASSWORD
          valueFrom:
            secretKeyRef:
              name: hiclaw-secrets
              key: MINIO_PASSWORD
        volumeMounts:
        - name: data
          mountPath: /data
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: manager-pvc
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: manager-pvc
  namespace: hiclaw
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
---
apiVersion: v1
kind: Service
metadata:
  name: hiclaw-manager
  namespace: hiclaw
spec:
  ports:
  - port: 8080
  selector:
    app: hiclaw-manager
```

```bash
kubectl apply -f manager.yaml
```

### 第八步：创建 Ingress

```yaml
# ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hiclaw-ingress
  namespace: hiclaw
  annotations:
    nginx.ingress.kubernetes.io/rewrite-target: /
spec:
  ingressClassName: nginx
  rules:
  - host: element.hiclaw.local
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: element-web
            port:
              number: 80
  - host: matrix.hiclaw.local
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: tuwunel
            port:
              number: 8080
  - host: gateway.hiclaw.local
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: higress
            port:
              number: 80
  - host: console.hiclaw.local
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: higress
            port:
              number: 8001
```

```bash
kubectl apply -f ingress.yaml
```

---

## 验证部署

### 检查 Pod 状态

```bash
kubectl get pods -n hiclaw
```

预期输出：
```
NAME                              READY   STATUS    RESTARTS   AGE
minio-xxx                         1/1     Running   0          5m
tuwunel-xxx                       1/1     Running   0          5m
higress-xxx                       2/2     Running   0          5m
element-web-xxx                   1/1     Running   0          5m
hiclaw-manager-xxx                1/1     Running   0          5m
```

### 检查服务状态

```bash
kubectl get svc -n hiclaw
```

### 访问服务

配置本地 `/etc/hosts`：

```
<INGRESS_IP> element.hiclaw.local matrix.hiclaw.local gateway.hiclaw.local console.hiclaw.local
```

获取 Ingress IP：

```bash
kubectl get ingress -n hiclaw
```

---

## 配置说明

### 环境变量

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| `HICLAW_LLM_API_KEY` | LLM API 密钥 | - |
| `HICLAW_LLM_PROVIDER` | LLM 提供商 | `qwen` |
| `HICLAW_MATRIX_DOMAIN` | Matrix 服务器域名 | `matrix-local.hiclaw.io:8080` |
| `HICLAW_MINIO_ENDPOINT` | MinIO 端点 | `minio:9000` |
| `HICLAW_ADMIN_USER` | 管理员用户名 | `admin` |
| `HICLAW_ADMIN_PASSWORD` | 管理员密码 | - |

### 资源限制

建议为生产环境配置资源限制：

```yaml
resources:
  limits:
    cpu: "2"
    memory: "4Gi"
  requests:
    cpu: "500m"
    memory: "1Gi"
```

---

## 故障排查

### Pod 启动失败

```bash
# 查看 Pod 事件
kubectl describe pod <pod-name> -n hiclaw

# 查看容器日志
kubectl logs <pod-name> -n hiclaw
```

### 服务无法访问

```bash
# 检查 Service 端点
kubectl get endpoints -n hiclaw

# 检查 Ingress 配置
kubectl describe ingress -n hiclaw
```

### Manager 连接失败

```bash
# 进入 Manager Pod 检查网络
kubectl exec -it <manager-pod> -n hiclaw -- /bin/bash
curl http://tuwunel:6167/_matrix/client/versions
curl http://minio:9000/minio/health/live
```

---

## 生产环境建议

### 1. 高可用配置

```yaml
# 多副本部署
spec:
  replicas: 3
  
# Pod 反亲和性
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
    - weight: 100
      podAffinityTerm:
        labelSelector:
          matchLabels:
            app: hiclaw-manager
        topologyKey: kubernetes.io/hostname
```

### 2. 数据持久化

- 使用高可用存储类（如 Ceph、NFS）
- 配置定期备份
- 启用 MinIO 集群模式

### 3. 安全配置

- 启用 TLS
- 配置 NetworkPolicy
- 使用 Secret 管理工具（如 Vault）

### 4. 监控告警

- 部署 Prometheus + Grafana
- 配置 Pod 监控
- 设置资源告警规则

---

## 一键部署脚本

将以上所有配置合并：

```bash
# 克隆配置仓库（待创建）
git clone https://github.com/xxx/hiclaw-k8s.git
cd hiclaw-k8s

# 修改配置
vim config/secrets.env

# 一键部署
./deploy.sh
```

---

## 相关 Issue

- [#245](https://github.com/alibaba/hiclaw/issues/245) - [feature] 希望支持 k8s 部署模式

欢迎在 Issue 中讨论 K8s 部署的最佳实践！