# HiClaw Aliyun Cloud Deployment

Deploy HiClaw to Alibaba Cloud with a single command. Maps each local component to a managed cloud service.

### Component Mapping

| Local (Docker)       | Cloud (Alibaba Cloud)                  |
|----------------------|----------------------------------------|
| Higress (all-in-one) | AI Gateway (APIG)                      |
| Tuwunel (Matrix)     | SAE (Serverless App Engine)            |
| Element Web          | SAE                                    |
| Manager Agent        | SAE                                    |
| Worker Agent(s)      | SAE (created at runtime by Manager)    |
| MinIO                | OSS (Object Storage Service)           |

## Prerequisites

1. Alibaba Cloud account with AccessKey
2. Python 3.10+
3. Install dependencies:

```bash
pip install -r aliyun/requirements.txt
```

4. Set environment variables:

```bash
export ALIBABA_CLOUD_ACCESS_KEY_ID="your-ak"
export ALIBABA_CLOUD_ACCESS_KEY_SECRET="your-sk"
export HICLAW_LLM_API_KEY="your-qwen-api-key"
export HICLAW_RRSA_OIDC_PROVIDER_ARN="acs:ram::ACCOUNT:oidc-provider/..."  # from SAE RRSA
```

> **Tip**: For `HICLAW_LLM_API_KEY`, we recommend using an API Key from the [Alibaba Cloud Bailian Platform](https://bailian.console.aliyun.com/cn-beijing/?tab=model#/api-key), which supports Qwen and other models.

> **Tip**: `HICLAW_RRSA_OIDC_PROVIDER_ARN` can be found at [RAM Console > OIDC Providers](https://ram.console.aliyun.com/providers?idpType=OIDC). This OIDC Provider cannot be created via SDK — it is automatically created by SAE when you enable RRSA OIDC for the first time on any SAE application. If you don't have one yet, create a temporary SAE application with RRSA OIDC enabled, copy the Provider ARN, then delete the application.

## Usage

All commands run from the project root directory.

### Deploy

```bash
# Full deployment (VPC + OSS + AI Gateway + SAE apps + Manager)
python3 -m aliyun.deploy deploy

# Skip AI Gateway (create it manually later)
python3 -m aliyun.deploy deploy --skip-gateway

# Reuse an existing VPC instead of creating a new one
python3 -m aliyun.deploy deploy --reuse-vpc
```

Deployment takes ~10 minutes. The 9-step process:

1. Verify credentials
2. Create VPC (2 vSwitches + EIP + NAT Gateway + SNAT)
3. Create Security Group
4. Create OSS bucket
5. Setup RRSA OIDC roles (Manager + Worker)
6. Create AI Gateway instance (~3-5 min)
7. Deploy SAE apps (Tuwunel + Element Web) with NLB
8. Configure AI Gateway (services, APIs, routes, consumer)
9. Deploy Manager Agent (SAE)

Credentials are saved to `/tmp/hiclaw-cloud-config.json` after deployment.

### Check Status

```bash
python3 -m aliyun.deploy status
```

### Destroy

```bash
# Dry run (shows what will be deleted)
python3 -m aliyun.deploy destroy

# Actually destroy all resources
python3 -m aliyun.deploy destroy --confirm

# Keep VPC network (only destroy SAE, Gateway, OSS)
python3 -m aliyun.deploy destroy --confirm --keep-vpc

# Also delete IAM roles and policies
python3 -m aliyun.deploy destroy --confirm --include-iam
```

### Show Config

```bash
python3 -m aliyun.deploy config
```

## Optional Environment Variables

| Variable                    | Default        | Description                    |
|-----------------------------|----------------|--------------------------------|
| `HICLAW_REGION`             | `cn-hangzhou`  | Alibaba Cloud region           |
| `HICLAW_REGISTRATION_TOKEN` | auto-generated | Matrix registration token      |
| `HICLAW_ADMIN_PASSWORD`     | auto-generated | Admin password                 |
| `HICLAW_MANAGER_PASSWORD`   | auto-generated | Manager Matrix password        |

## Module Structure

```
aliyun/
├── __init__.py       # Package init
├── config.py         # Centralized configuration (CloudConfig dataclass)
├── deploy.py         # CLI entry point: deploy / status / destroy / config
├── vpc.py            # VPC, vSwitch, NAT Gateway, EIP, SNAT
├── security.py       # Security Group management
├── oss.py            # OSS bucket CRUD
├── rrsa.py           # RRSA OIDC roles (Manager + Worker)
├── gateway.py        # AI Gateway: services, APIs, routes, consumers
├── sae.py            # SAE: namespace, apps, NLB binding, Manager/Worker deploy
├── clients.py        # Shared SDK client constructors
└── requirements.txt  # Python dependencies
```
