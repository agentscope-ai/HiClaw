# DeepSeek Harness Worker runtime

This directory packages DeepSeek Harness as an AgentTeams-managed Worker. The Controller still owns the Worker lifecycle and projects `runtime.yaml`; the image translates that contract, including `desired.model.model` and its gateway route, into a DSH profile, listens for Matrix text messages, runs a headless DSH turn, and posts the reply to the same room.

The image is pinned to `@deepseek-ai/dsh@0.1.1-rc.2` and uses the TeamHarness adapter in `plugins/teamharness/adapters/deepseek-harness-prototype` without patching DSH itself.

Build the image from the repository root:

```bash
make build-deepseek-harness-worker
```

Create a Worker with the runtime explicitly selected:

```yaml
apiVersion: agentteams.io/v1beta1
kind: Worker
metadata:
  name: dsh-worker
spec:
  runtime: deepseek-harness
  model: deepseek-v4-flash
```

The test coverage is split by boundary:

- `python -m unittest discover -s deepseek-harness/tests -p "test_*.py"` checks Matrix event filtering, useful error reporting, and gateway URL normalization.
- `plugins/teamharness/adapters/deepseek-harness-prototype/smoke.ps1` checks package install/remove, role-specific prompts and Skills, and TeamHarness MCP registration against the pinned DSH checkout.
- `model-smoke.ps1` makes a real model call and verifies that DSH sees the identity injected from `runtime.yaml`.
- `live-services-smoke.ps1 -WorkerName <existing-worker>` sends and reads back a real Matrix event and pushes, stats, reads back, and removes a real MinIO object.
- `deepseek-harness/tests/k8s_lifecycle_smoke.ps1 -WorkerName <dsh-worker>` verifies a Controller-created Worker, a Matrix-to-model-to-Matrix turn, persisted sync state, Pod replacement, and a second turn without replay.

This is still an initial runtime adapter. Each Matrix message currently starts a new headless DSH turn; DSH session files are mirrored for recovery and inspection, but the bridge does not yet resume a conversational session across messages. The bridge handles text events only and processes them sequentially.
