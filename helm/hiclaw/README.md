# HiClaw Helm Chart（阿里云 ACK / ACS）

在同一命名空间部署 **Manager**（协调器）、**Tuwunel**（Matrix）、**Element Web**，并通过 **OSS + RAM（RRSA）** 与 **AI 网关（APIG）** 在云上运行。Worker 由 Manager 在集群内创建为**独立 Pod**（非 Helm 资源）。

**Chart 要点**：默认 **`manager.rrsa.mode: manual`**（Deployment 内 projected OIDC token + `ALIBABA_CLOUD_*`）；可选用 **webhook** + `ack-pod-identity-webhook`。Tuwunel 数据目录需要 **RWX**（ACK 上多为 **NAS**）。细则以本目录 [`values.yaml`](./values.yaml) 为准。

---

## 前置资源准备

完整清单、参数约定与团队内流程见语雀：**[《HiClaw ACK/ACS 云上部署》前置资源准备](https://aliyuque.antfin.com/g/youjin.ls/uzsiws/kighwth7htbw7pa0/collaborator/join?token=MriVAXlhRjRT8KeF&source=doc_collaborator#)**（需协作权限；无法打开时可仅依赖下文与阿里云官方文档完成环境）。

建议按下面各云产品**顺序**准备；每节说明「要做什么」并附对应产品文档。

**本机**：安装 **`kubectl`**、**Helm 3**，并配置好访问目标集群的 **kubeconfig**。

---

### 容器服务 ACK

**要做什么**：在目标地域创建 **Kubernetes 集群**，作为 HiClaw 的运行环境；确保集群版本满足控制台对 **RRSA** 的要求（常见 **Kubernetes ≥ 1.22**，以实际控制台为准）；节点能拉取你在 `values.yaml` 中配置 **Manager / Worker** 容器镜像（公网镜像需节点出网或使用对应镜像仓库凭据）。

**文档**：[创建 ACK 托管集群](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/create-an-ack-managed-cluster) · [ACK 文档中心](https://help.aliyun.com/zh/ack/)

---

### RRSA 与 Pod 身份

**要做什么**：在 ACK 集群上**开启 RRSA**，记下 **OIDC Issuer** 与控制台中的 **OIDC Provider ARN** 等信息，后续要写入 Chart 的 **`manager.rrsa.manual.oidcProviderArn`** 等字段。HiClaw 通过 **RAM OIDC 角色**让 Pod 用 **STS** 访问 OSS 等云服务，一般不在 Pod 内长期放主账号 AK。

与 Chart 对齐时二选一（与 **`manager.rrsa.mode` / `workerServiceAccount.rrsa.mode`** 一致）：

- **Manual（Chart 默认）**：按 ACK 文档在应用模板里挂 **projected `serviceAccountToken`**（`audience: sts.aliyuncs.com`），并配置 **`ALIBABA_CLOUD_ROLE_ARN`**、**`ALIBABA_CLOUD_OIDC_PROVIDER_ARN`**、**`ALIBABA_CLOUD_OIDC_TOKEN_FILE`**。在 `values.yaml` 填写 **`manager.rrsa.manual.roleArn`**、**`manager.rrsa.manual.oidcProviderArn`**、**`workerServiceAccount.rrsa.manual.roleArn`**；Worker Pod 由 **`kubernetes-api.py`** 使用同类方式挂载。**无需**安装 `ack-pod-identity-webhook`。
- **Webhook**：安装 **`ack-pod-identity-webhook`**，在 Chart 中设 **`manager.rrsa.mode: webhook`**、**`workerServiceAccount.rrsa.mode: webhook`**，并填写 RAM 角色**短名** **`manager.rrsa.roleName`** / **`workerServiceAccount.rrsa.roleName`**；若需命名空间级注入，可设 **`global.podIdentity.namespaceInjection: true`** 或为命名空间打 **`pod-identity.alibabacloud.com/injection=on`**。

**文档**：[使用 RRSA 授权 Pod 访问云服务](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/use-rrsa-to-authorize-pods-to-access-different-cloud-services) · [ack-pod-identity-webhook](https://help.aliyun.com/zh/ack/product-overview/ack-pod-identity-webhook)

---

### 对象存储 OSS

**要做什么**：在与 ACK **相同地域**创建 **OSS Bucket**，名称将写入 Helm Secret 的 **`HICLAW_OSS_BUCKET`**。应用侧对象前缀由 `hiclaw-env.sh` 推导为 **`hiclaw/<HICLAW_OSS_BUCKET>/...`**，你在 **RAM 策略**里需覆盖该前缀，避免过大权限。访问方式为 **RAM + STS**，与 RRSA 角色配合。

**文档**：[创建 Bucket](https://help.aliyun.com/zh/oss/user-guide/create-buckets-4) · [OSS 与 RAM](https://help.aliyun.com/zh/oss/developer-reference/use-buckets-after-integrating-with-ram) · [OSS 文档中心](https://help.aliyun.com/zh/oss/)

---

### 文件存储 NAS

**要做什么**：为 **Tuwunel** 持久化目录准备 **ReadWriteMany（RWX）** 存储；ACK 上通常使用 **NAS**。在 Chart 里通过 **`global.platform`** 选择形态：**`ack`** 使用静态 **PV + PVC（selector）**（无默认 StorageClass，Chart 可下发 PV）；**`acs`** 使用带 CSI 注解的 **PVC** 与 **`acs.storageClassName`** 等。**NAS 挂载点域名**填入 **`tuwunel.persistence.nas.server`**（ACK 的 PV `server` 与 ACS 的 `mountpoint` 共用这一条）。若使用已有 PVC，可设 **`tuwunel.persistence.existingClaim`**。

**文档**：[NAS 文档中心](https://help.aliyun.com/zh/nas/) · [通过 NFS 挂载静态 NAS 卷](https://help.aliyun.com/zh/nas/user-guide/mount-a-statically-provisioned-nas-volume-by-using-nfs) · [ACS 挂载 NAS](https://help.aliyun.com/zh/nas/user-guide/acs-mount-file-system-of-alibaba-cloud-container-computing-service)

---

### 访问控制 RAM

**要做什么**：为 **Manager**、**Worker** 各创建一个 **OIDC 信任**的 RAM 角色（Issuer / Subject / Audience 须与**当前集群** RRSA 文档一致，勿照抄旧示例 ARN）。**Manager 角色**：至少覆盖 **`oss://<bucket>/hiclaw/...`** 等对象操作，并按需增加 **APIG / 其它云 API** 权限，遵循最小权限。**Worker 角色**：建议**仅 OSS 对象**相关权限（如 Get/Put/Delete/List/Head 及带 prefix 的 ListBucket 等），**不要**与 Manager 同权。将 **Manager 角色 ARN**、**OIDC Provider ARN** 等写入 Chart；**Worker 角色 ARN** 供 Worker Pod 使用。

Chart 会为 Manager / Worker 各创建一个 **Kubernetes ServiceAccount**；Manager Deployment 会设置 **`HICLAW_K8S_WORKER_SERVICE_ACCOUNT`**，使 Manager 创建的 Worker Pod 绑定 **Worker 专用 SA**；集群内 **RBAC**（创建 Pod 等）只授予 **Manager 的 SA**，见 `templates/rbac.yaml`。

**文档**：[创建 OIDC RAM 角色](https://help.aliyun.com/zh/ram/developer-reference/create-role-oidc) · [为角色授权](https://help.aliyun.com/zh/ram/user-guide/grant-permissions-to-a-role) · [RAM 文档中心](https://help.aliyun.com/zh/ram/)

---

### AI 网关（APIG）

**要做什么**：准备 **LLM / AI 网关**的**出站访问地址**，与 HiClaw 中 **`HICLAW_AI_GATEWAY_URL`** 一致（浏览器侧 **Element Web** 默认也用它作为 **`MATRIX_SERVER_URL`**，除非另设 **`elementWeb.env.MATRIX_SERVER_URL`**）。在网关上配置 **Consumer**，密钥与 **`HICLAW_MANAGER_GATEWAY_KEY`** 等保持一致。该 URL 用于 **LLM 出站**；集群内 **Manager ↔ Tuwunel** 仍走 **Cluster DNS**，不依赖该公网地址。

**文档**：[API 网关](https://help.aliyun.com/zh/api-gateway/)（若你方使用其它 APIG/专属网关产品，以其控制台文档为准。）

---

## 安装步骤

在**仓库根目录**执行。默认 **`-f ./helm/hiclaw/values.yaml`**，用 **`--set`** / **`--set-string`** 覆盖环境与集群相关参数。**勿将真实密钥写入 Git**；生产可用未入库的 `-f extra.yaml` 或 **`manager.envFromSecret`**。

含逗号、URL、`500m`/`2Gi` 等易被 shell 或 Helm 误解析的值，请用 **`--set-string`**。

### `helm upgrade --install` 与 `--set` 参数说明

| `--set` / `--set-string` 路径 | 说明 |
|------------------------------|------|
| `workerServiceAccount.rrsa.roleName` | RRSA **webhook** 模式下的 RAM 角色**短名**（与 `ack-pod-identity-webhook` 注解配合；manual 模式可保留与文档一致） |
| `workerServiceAccount.rrsa.manual.roleArn` | Worker Pod 使用的 **RAM OIDC 角色 ARN**（建议仅 OSS 对象权限） |
| `manager.rrsa.roleName` | Manager **webhook** 模式下的 RAM 角色短名 |
| `manager.rrsa.manual.roleArn` | Manager Pod **AssumeRoleWithOIDC** 使用的 **RAM 角色 ARN**（manual RRSA） |
| `manager.rrsa.manual.oidcProviderArn` | 集群 **RRSA OIDC Provider ARN**（与 ACK 控制台 RRSA 一致） |
| `manager.resources.requests.cpu` | Manager Deployment **requests.cpu** |
| `manager.resources.requests.memory` | Manager Deployment **requests.memory**（如 `2Gi`） |
| `manager.secret.stringData.HICLAW_AI_GATEWAY_URL` | **LLM / AI 网关（APIG）** 出站地址；**同时作为 Element Web 的 `MATRIX_SERVER_URL` 默认值**（除非你另设 `elementWeb.env.MATRIX_SERVER_URL`） |
| `manager.secret.stringData.HICLAW_OSS_BUCKET` | **OSS Bucket** 名称（与 RAM 策略前缀一致） |
| `manager.secret.stringData.HICLAW_MANAGER_PASSWORD` | Manager 在 Matrix 上的账户密码 |
| `manager.secret.stringData.HICLAW_MANAGER_GATEWAY_KEY` | Higress / APIG **Consumer** 密钥（与网关配置一致） |
| `manager.secret.stringData.HICLAW_REGISTRATION_TOKEN` | Matrix **开放注册** Token（与 Tuwunel **`CONDUWUIT_REGISTRATION_TOKEN`** 同源：同一 Secret 的 `HICLAW_REGISTRATION_TOKEN` 键） |
| `manager.secret.stringData.HICLAW_ADMIN_USER` | 管理后台用户名 |
| `manager.secret.stringData.HICLAW_ADMIN_PASSWORD` | 管理后台密码 |
| `tuwunel.resources.requests.cpu` | Tuwunel Deployment **requests.cpu** |
| `tuwunel.resources.requests.memory` | Tuwunel Deployment **requests.memory** |
| `global.platform` | **`ack`** 或 **`acs`**：Tuwunel NAS 挂载形态（全局）；可被 **`tuwunel.persistence.platform`** 覆盖 |
| `tuwunel.persistence.nas.server` | **ACK 静态 PV** 的 `server` 与 **ACS** 的 `mountpoint` **共用**（NAS 挂载点域名，可含 `:/子路径`）；勿再分别设 `pv.server` / `acs.mountpoint`（旧键仍兼容） |
| `elementWeb.env.MATRIX_SERVER_URL` | （可选）覆盖浏览器 Matrix 根 URL；**不设置时与 `HICLAW_AI_GATEWAY_URL` 相同** |

**Tuwunel NAS（`global.platform`）**

| 取值 | 文档 | 做法 |
|------|------|------|
| **`ack`** | [NAS 静态卷](https://help.aliyun.com/zh/nas/user-guide/mount-a-statically-provisioned-nas-volume-by-using-nfs) | **PV + PVC（selector）**，无 StorageClass；Chart 可下发 PV（`pv.enabled`）并填 **`tuwunel.persistence.nas.server`** |
| **`acs`** | [ACS 挂载 NAS](https://help.aliyun.com/zh/nas/user-guide/acs-mount-file-system-of-alibaba-cloud-container-computing-service) | **PVC** 注解 + **`acs.storageClassName`** 等；挂载点用 **`tuwunel.persistence.nas.server`** |

### 安装命令示例（`--set`）

下列命令中 **`'...'`** 内为**说明占位**，请替换为真实值（示例命名空间 **`hiclaw`**、release 名 **`hiclaw`**）：

```bash
helm upgrade --install hiclaw ./helm/hiclaw \
  --namespace hiclaw \
  --create-namespace \
  -f ./helm/hiclaw/values.yaml \
  --set workerServiceAccount.rrsa.roleName='Worker RRSA 角色短名（webhook 用）' \
  --set workerServiceAccount.rrsa.manual.roleArn='Worker RAM OIDC 角色 ARN' \
  --set manager.rrsa.roleName='Manager RRSA 角色短名（webhook 用）' \
  --set manager.rrsa.manual.roleArn='Manager RAM OIDC 角色 ARN' \
  --set manager.rrsa.manual.oidcProviderArn='集群 RRSA OIDC Provider ARN' \
  --set manager.resources.requests.cpu='Manager requests.cpu，如 2' \
  --set-string manager.resources.requests.memory='Manager requests.memory，如 2Gi' \
  --set-string manager.secret.stringData.HICLAW_AI_GATEWAY_URL='AI 网关 APIG 出站 URL' \
  --set-string manager.secret.stringData.HICLAW_OSS_BUCKET='OSS Bucket 名' \
  --set-string manager.secret.stringData.HICLAW_MANAGER_PASSWORD='Manager Matrix 账户密码' \
  --set-string manager.secret.stringData.HICLAW_MANAGER_GATEWAY_KEY='网关 Consumer 密钥' \
  --set-string manager.secret.stringData.HICLAW_REGISTRATION_TOKEN='Matrix 开放注册 Token' \
  --set-string manager.secret.stringData.HICLAW_ADMIN_USER='管理后台用户名' \
  --set-string manager.secret.stringData.HICLAW_ADMIN_PASSWORD='管理后台密码' \
  --set tuwunel.resources.requests.cpu='Tuwunel requests.cpu，如 500m' \
  --set-string tuwunel.resources.requests.memory='Tuwunel requests.memory，如 1Gi' \
  --set global.platform='ack 或 acs' \
  --set-string tuwunel.persistence.nas.server='NAS 挂载点（ACK 静态 PV 与 ACS mountpoint 共用）'
```

说明：

- **`HICLAW_AI_GATEWAY_URL`** 已同时作为 **Element Web `MATRIX_SERVER_URL`**（除非另设 **`elementWeb.env.MATRIX_SERVER_URL`**）。
- **`HICLAW_REGISTRATION_TOKEN`**：Manager 与 Tuwunel **共用**——Tuwunel 通过 **`valueFrom.secretKeyRef`** 读取与 Manager 相同的 Secret 键；无 `envFrom` Secret 时回退为 `manager.secret.stringData` 或 **`manager.env.HICLAW_REGISTRATION_TOKEN`**。
- **`tuwunel.persistence.nas.server`** 同时用于 **ACK**（PV `server`）与 **ACS**（`csi.alibabacloud.com/mountpoint`），**一条即可**。
- **`platform=acs`** 时：配置 **`tuwunel.persistence.acs.storageClassName`** 等（见 `values.yaml`），并设 **`tuwunel.persistence.pv.enabled=false`**（勿再下发 ACK 静态 PV）。
- 镜像、**`manager.env.HICLAW_RUNTIME` / `HICLAW_ALIYUN_WORKER_BACKEND`** 等仍可由 **`values.yaml`** 提供；需要时追加 `--set manager.env.HICLAW_RUNTIME=aliyun` 等。

Chart **不**带 `Namespace` 资源，依赖 **`--create-namespace`**。

### 安装后检查

```bash
kubectl -n hiclaw rollout status deployment/<release 全名>
kubectl -n hiclaw get pods,svc
kubectl -n hiclaw logs deployment/<同上> -f --tail=100
```

`helm get notes` 可查看 release 说明。Manual RRSA 时可抽查 Manager Pod 是否含 `ALIBABA_CLOUD_*`。Worker 由 Manager 创建，长期异常时查 RRSA、Secret、OSS 与 Pod 事件。

---

## Values overview

| Key | Purpose |
|-----|---------|
| `global.namespace` | Target namespace (metadata on resources) |
| `global.platform` | **`ack`** \| **`acs`** — Tuwunel NAS mode |
| `manager.rrsa.mode` (`manual` / `webhook`) | **manual**：projected OIDC token + `ALIBABA_CLOUD_*`（与 ACK 文档「手动 RRSA」一致）；**webhook**：SA `pod-identity` 注解 + 可选 `global.podIdentity.namespaceInjection` |
| `image.*` | Manager image |
| `manager.env` / `manager.secret.stringData` / `manager.envFromSecret` | Runtime configuration (chart Secret vs external Secret) |
| `manager.rrsa.*` | ACK pod identity role name |
| `rbac.create` | In-cluster RBAC for the Manager ServiceAccount (Pod exec, etc.) |
| `workerServiceAccount.*` | Separate Worker SA + `roleName` for webhook / RAM (OSS-only policy recommended) |
| `tuwunel.persistence.pv` | **ACK**：可选 Chart 管理 **PersistentVolume**（`pv.enabled` + `server` / `path`） |
| `tuwunel.persistence.nas.server` | **ACK** PV `server` and **ACS** `mountpoint` (one value) |
| `tuwunel.*` | Homeserver image, NAS persistence |
| `elementWeb.*` | Element Web image; optional **`env.MATRIX_SERVER_URL`** (defaults to **`HICLAW_AI_GATEWAY_URL`**, then in-cluster Tuwunel) |

详见 `values.yaml` 默认值。
