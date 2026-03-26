# Changelog (Unreleased)

Record image-affecting changes to `manager/`, `worker/`, `openclaw-base/` here before the next release.

---

- feat(shared/manager): Alibaba cloud — `HICLAW_RUNTIME=aliyun` + **`HICLAW_ALIYUN_WORKER_BACKEND`**=`sae`（默认）|`k8s`（ACK）；legacy `HICLAW_RUNTIME=k8s` → `aliyun`+`k8s`; removed `HICLAW_CONTAINER_BACKEND`; Helm ACK sets `k8s`; `Dockerfile.aliyun` defaults `sae`
- fix(manager): ACK/K8s cloud coordinator — `HICLAW_CLOUD_COORDINATOR` in `start-manager-agent.sh` (validation, OSS pull/sync, skip local Higress/MinIO, `openclaw.json` overlay) for `HICLAW_RUNTIME=k8s`
- fix(shared): `hiclaw-env.sh` preserves pre-set `HICLAW_RUNTIME` so Helm can force `k8s` over OIDC auto-detection
- fix(manager): `gateway-api.sh` treats `k8s` like SAE for AI Gateway (APIG) consumer flow
- fix(manager): `kubernetes.sh` invokes `kubernetes-api.py`; cloud Worker Pods get SAE-equivalent env, no pseudo-MinIO on `:9000`, optional `HICLAW_K8S_WORKER_SERVICE_ACCOUNT`, automount SA token when using OSS
- fix(manager): `create-worker.sh` / `lifecycle-worker.sh` — shared `k8s-worker-env.sh`, skip MinIO admin Step 1b for `k8s`, persist `WORKER_MATRIX_TOKEN` for Pod recreate; `ensure-ready` uses `worker_backend` when Docker socket absent
- fix(helm): **`global.platform`**（`ack`\|`acs`）— Tuwunel NAS 形态；可选 **`tuwunel.persistence.platform`** 覆盖；**`CONDUWUIT_REGISTRATION_TOKEN`** 与 **`HICLAW_REGISTRATION_TOKEN`** 同源（Secret `valueFrom` 或 `stringData`/`manager.env`）
- fix(helm): **`tuwunel.persistence.nas.server`** — ACK PV `server` 与 ACS `mountpoint` 共用；**Element** `MATRIX_SERVER_URL` 默认 **`HICLAW_AI_GATEWAY_URL`**
- fix(helm): Tuwunel NAS — **ACK** [静态 NAS](https://help.aliyun.com/zh/nas/user-guide/mount-a-statically-provisioned-nas-volume-by-using-nfs)（可选 Chart **PV** + **PVC** selector，无 StorageClass）；**ACS** [挂载 NAS](https://help.aliyun.com/zh/nas/user-guide/acs-mount-file-system-of-alibaba-cloud-container-computing-service)（注解 **PVC**）；删除 Chart **StorageClass** 模板
- fix(helm): default `manager.env` `HICLAW_RUNTIME=aliyun` + `HICLAW_ALIYUN_WORKER_BACKEND=k8s`, inject `HICLAW_K8S_NAMESPACE` from downward API
- fix(helm): separate `workerServiceAccount` with RRSA OSS-only RAM role; Manager gets `HICLAW_K8S_WORKER_SERVICE_ACCOUNT` for Worker Pods (not Manager SA)
- refactor(helm): RRSA — rely on **ack-pod-identity-webhook** for `ALIBABA_CLOUD_*` / OIDC on Manager and Worker Pods; Chart only sets SA `pod-identity.alibabacloud.com/role-name` (removed manual projected volume / ARN env from Deployment)
- refactor(manager): `kubernetes-api.py` — remove manual Worker Pod RRSA volumes/env; webhook mutates Worker Pods that use the Worker ServiceAccount
- docs(manager): `create-worker.sh` / `kubernetes.sh` / `kubernetes-api.py` / `k8s-worker-env.sh` — document ACK vs SAE cloud Worker flow (Steps 1–8 shared; Step 9 k8s-create ↔ sae-create); skip MinIO Step 1b for `HICLAW_RUNTIME=k8s`
- docs(manager): `aliyun-api.py` / `aliyun-sae.sh` / `kubernetes.sh` / `kubernetes-api.py` / `gateway-api.sh` — clarify APIG consumer (gw-*) is shared by ACK+SAE via aliyun-sae.sh; only Worker spawn differs (sae-create vs k8s-create)
- docs(shared): `hiclaw-env.sh` comment — Matrix URL may be in-cluster Service DNS, not only NLB
- docs(helm): merge `deploy-ack-aliyun.md` install steps into `helm/hiclaw/README.md`（前置资源准备：语雀 + 云产品官方文档）；`deploy-ack-aliyun.md` 重定向至 README
- feat(helm): Tuwunel + Element Web always deployed (removed `enabled` toggles); `elementWeb.env.MATRIX_SERVER_URL` optional (defaults to in-cluster Tuwunel URL); without `manager.envFromSecret`, inject `HICLAW_MATRIX_*` and default `CONDUWUIT_SERVER_NAME`
- fix(helm): drop chart `Namespace` template; rely on `helm --create-namespace` only (fixes install failure when `hiclaw` already exists)
- fix(helm): Tuwunel — skip `CONDUWUIT_DATABASE_PATH` from `tuwunel.env` range (avoid duplicate env key vs mountPath)
- fix(helm): Manager Service + probes use OpenClaw gateway port **18799** (was 9000; nothing listened → Not Ready)
- fix(helm): optional `Namespace` with `pod-identity.alibabacloud.com/injection=on` for ack-pod-identity-webhook RRSA (`global.podIdentity.namespaceInjection`, default off — use with `manager.rrsa.mode=webhook`); `namespace.yaml` tolerates omitted `global.podIdentity`
- feat(helm): RRSA **manual** mode (default) — Manager Deployment projected token + `ALIBABA_CLOUD_*` env per ACK doc; **webhook** mode keeps SA annotations; `global.podIdentity.namespaceInjection` default off
- fix(manager): `kubernetes-api.py` — Worker Pods get manual RRSA volume/env when `HICLAW_K8S_WORKER_RRSA_ROLE_ARN` + `ALIBABA_CLOUD_OIDC_PROVIDER_ARN` on Manager; `cloud_worker` treats `HICLAW_RUNTIME=k8s`

### Security

- **fix(security): restrict cloud worker OSS access with STS inline policy** — In cloud mode (Alibaba Cloud SAE), all workers shared the same RRSA role with unrestricted OSS bucket access, allowing any worker to read/write other workers' and manager's files. Now `oss-credentials.sh` injects an inline policy into the STS `AssumeRoleWithOIDC` request when `HICLAW_WORKER_NAME` is set, restricting the STS token to `agents/{worker}/*` and `shared/*` prefixes only — matching the per-worker MinIO policy used in local mode. Manager (which does not set `HICLAW_WORKER_NAME`) retains full access.

### Cloud Runtime
- fix(manager): `Dockerfile.aliyun` — `pip install kubernetes` so ACK (`kubernetes-api.py`) works out of the box; previously only SAE/APIG SDKs were installed and Worker Pod creation fell back to remote/install hints
- **fix(cloud): auto-refresh STS credentials for all mc invocations** — wrap mc binary with `mc-wrapper.sh` that calls `ensure_mc_credentials` before every invocation, preventing token expiry after ~50 minutes in cloud mode. Affects: manager, worker, copaw.
- fix(copaw): refresh STS credentials in Python sync loops to prevent MinIO sync failure after token expiry

- fix(cloud): set `HICLAW_RUNTIME=aliyun` explicitly in Dockerfile.aliyun instead of relying on OIDC file detection at runtime
- fix(cloud): respect pre-set `HICLAW_RUNTIME` in hiclaw-env.sh — only auto-detect when unset
- fix: add explicit Matrix room join with retry before sending welcome message to prevent race condition

