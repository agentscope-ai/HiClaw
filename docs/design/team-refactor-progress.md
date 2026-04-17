# Team Refactor Progress Tracker

> 跟踪 `team-refactor-plan.md` 中 IMPLEMENTATION CHECKLIST 的执行进度。
> 每完成一步，更新对应项为 `[x]`，并在下方添加一行执行记录。

- 创建日期：2026-04-17
- 方案文档：[`team-refactor-plan.md`](./team-refactor-plan.md)
- 状态总览：**Planning Completed / Execution Not Started**

---

## 状态图例

- `[ ]` Pending — 未开始
- `[~]` In Progress — 执行中
- `[x]` Completed — 已完成并验证
- `[!]` Blocked — 阻塞（下方说明原因）
- `[-]` Skipped / Deferred — 跳过或延后（下方说明原因）

---

## Stage 0: 方案与文档

- [x] **1. Create docs/design/team-refactor-plan.md** —— 方案主文档已完成
- [x] **2. Create docs/design/team-refactor-progress.md** —— 进度跟踪文档（本文件）
- [x] **3. Update docs/design/team-worker-proposal.md top banner** (superseded)
- [x] **4. Update docs/design/team-worker-ownership-issues.md top banner** (resolved)

---

## Stage 1: API Types 与 CRD Schema

- [x] **5. Rewrite hiclaw-controller/api/v1beta1/types.go**
    - Worker: add `Role`, `TeamRef`; status add `TeamRef`, `Conditions`
    - Team: remove `Leader`, `Workers`, `Admin`; add `Heartbeat`, `WorkerIdleTimeout`; rewrite status with observations
    - Human: remove `PermissionLevel`, `AccessibleTeams`, `AccessibleWorkers`; add `SuperAdmin`, `TeamAccess`, `WorkerAccess`
    - Add: `TeamHeartbeatSpec`, `TeamLeaderObservation`, `TeamMemberObservation`, `TeamAdminObservation`, `TeamAccessEntry`
    - Remove: `LeaderSpec`, `TeamWorkerSpec`, `TeamAdminSpec`, `TeamLeaderHeartbeatSpec`
- [x] **6. Regenerate zz_generated.deepcopy.go** (`make generate` — added generate target to Makefile)
- [x] **7. Rewrite config/crd/workers.hiclaw.io.yaml** with new fields + printer columns
- [x] **8. Rewrite config/crd/teams.hiclaw.io.yaml** removing leader/workers/admin
- [x] **9. Rewrite config/crd/humans.hiclaw.io.yaml** with new fields

---

## Stage 2: Service 层

- [ ] **10. Update service/interfaces.go**: add TeamProvisioner, TeamObserver; reshape WorkerDeployer (add WriteLeaderCoordinationContext)
- [ ] **11. Rewrite service/provisioner.go team section**: EnsureTeamRooms, ReconcileTeamRoomMembership, CleanupTeamInfra
- [ ] **12. Update service/deployer.go**: remove InjectCoordinationContext, add WriteLeaderCoordinationContext
- [ ] **13. Create service/observer.go** implementing TeamObserver

---

## Stage 3: Webhook Package

- [ ] **14. Create internal/webhook/webhook.go** (dispatcher + RegisterWithManager)
- [ ] **15. Create internal/webhook/worker_validator.go**
- [ ] **16. Create internal/webhook/team_validator.go**
- [ ] **17. Create internal/webhook/human_validator.go**
- [ ] **18. Add webhook validator tests** (worker/team/human `_test.go`)

---

## Stage 4: TeamReconciler 重写

- [ ] **19. Delete old team_controller.go**
- [ ] **20. Create new team_controller.go** (main loop + SetupWithManager with Watches)
- [ ] **21. Create team_scope.go**
- [ ] **22. Create team_phase.go**
- [ ] **23. Create team_reconcile_members.go**
- [ ] **24. Create team_reconcile_admins.go**
- [ ] **25. Create team_reconcile_rooms.go**
- [ ] **26. Create team_reconcile_storage.go**
- [ ] **27. Create team_reconcile_legacy.go**
- [ ] **28. Create team_reconcile_delete.go** (no Worker deletion)

---

## Stage 5: HumanReconciler 重写

- [ ] **29. Delete old human_controller.go and rewrite**
- [ ] **30. Create human_scope.go**
- [ ] **31. Create human_phase.go**
- [ ] **32. Create human_reconcile_infra.go**
- [ ] **33. Create human_reconcile_rooms.go** (superAdmin + teamAccess + workerAccess → desired rooms)
- [ ] **34. Create human_reconcile_legacy.go**
- [ ] **35. Create human_reconcile_delete.go**

---

## Stage 6: WorkerReconciler 增量扩展

- [ ] **36. Update worker_controller.go** (Watches Team+Human, insert new phases, label sync)
- [ ] **37. Create worker_reconcile_team.go** (reconcileTeamMembership)
- [ ] **38. Create worker_reconcile_leader_broadcast.go** (reconcileLeaderBroadcast)
- [ ] **39. Update worker_reconcile_config.go** (consume scope.effectivePolicy)
- [ ] **40. Update worker_scope.go** (add team-related fields)

---

## Stage 7: ManagerReconciler 增量扩展

- [ ] **41. Update manager_controller.go** (Watches Worker+Human, insert reconcileManagerAllowFrom phase)
- [ ] **42. Create manager_reconcile_allow_from.go**
- [ ] **43. Update manager_reconcile_config.go** (consume scope.effectiveAllowFrom)
- [ ] **44. Update manager_scope.go** (add effectiveAllowFrom field)

---

## Stage 8: Webhook Wiring

- [ ] **45. Wire webhook in cmd/controller/main.go** (HICLAW_WEBHOOK_ENABLED)
- [ ] **46. Create config/webhook/ ValidatingWebhookConfiguration manifests**
- [ ] **47. Create helm/hiclaw/templates/controller/webhook.yaml** (Service + TLS)

---

## Stage 9: Mocks / Fixtures

- [ ] **48. Update test/testutil/mocks/provisioner.go** (new team Fn fields)
- [ ] **49. Update test/testutil/mocks/deployer.go** (WriteLeaderCoordinationContextFn)
- [ ] **50. Create test/testutil/mocks/team_observer.go**
- [ ] **51. Update test/testutil/fixtures/worker.go** (WithRole, WithTeamRef)
- [ ] **52. Create test/testutil/fixtures/team.go**
- [ ] **53. Create test/testutil/fixtures/human.go**
- [ ] **54. Create test/testutil/fixtures/team_bundle.go**

---

## Stage 10: REST API

- [ ] **55. Rewrite internal/server/types.go** (slim CreateTeamRequest, new TeamBundleRequest, new Human types)
- [ ] **56. Update internal/server/resource_handler.go** (slim team handlers, inline webhook validation)
- [ ] **57. Create internal/server/bundle_handler.go** (CreateTeamBundle, DeleteTeamBundle)
- [ ] **58. Update internal/server/http.go** (register bundle routes)

---

## Stage 11: CLI

- [ ] **59. Update cmd/hiclaw/create.go** (createTeamCmd → bundle; createWorkerCmd --role --team; createHumanCmd new flags)
- [ ] **60. Update cmd/hiclaw/delete.go** (deleteTeamCmd cascade default + --orphan-workers)
- [ ] **61. Update cmd/hiclaw/update.go** (slim updateTeamCmd, updateWorkerCmd --role --team, promoteWorkerCmd)
- [ ] **62. Update cmd/hiclaw/get.go** (teamDetail new fields)
- [ ] **63. Update cmd/hiclaw/output.go** (printer helpers)
- [ ] **64. Update internal/service/legacy.go** (registry entry field shapes)

---

## Stage 12: 集成测试

- [ ] **65. Update test/integration/controller/suite_test.go** (register TeamReconciler, HumanReconciler, TeamObserver mock)
- [ ] **66. Create test/integration/controller/team_test.go** (10 scenarios)
- [ ] **67. Extend worker_test.go** (invalid teamRef, role transition)
- [ ] **68. Create human_test.go**
- [ ] **69. Create bundle_test.go**

---

## Stage 13: 文档与验证

- [ ] **70. Update docs/design/team-worker-proposal.md top banner**
- [ ] **71. Update docs/design/team-worker-ownership-issues.md top banner**
- [ ] **72. Update AGENTS.md** (Key Design Patterns + navigation)
- [ ] **73. Update manager/agent/skills/team-management/SKILL.md**
- [ ] **74. Update manager/agent/skills/human-management/SKILL.md**
- [ ] **75. Audit manager/agent/worker-skills/** for Worker CR field references
- [ ] **76. Add changelog/current.md entry**
- [ ] **77. Run make test + make test-integration**, fix all failures
- [ ] **78. Run make generate + make manifests**, ensure consistency
- [ ] **79. Manual verification in local kind cluster** (create/delete team, promote worker, multi-doc apply, kubectl workflows)
- [ ] **80. Mark all items above as completed**

---

## 执行记录 Log

格式：`[DATETIME] - <Actor> - <Action> - <Status> - <Notes>`

---

<!-- BEGIN EXECUTION LOG -->

[2026-04-17] - planner - Planning phase completed, ready for execution - PENDING APPROVAL - Awaiting "ENTER EXECUTE MODE" signal

[2026-04-17_Batch-1] - executor - Stage 0 完成 (Items 3-4): 为 team-worker-proposal.md 添加 SUPERSEDED banner, 为 team-worker-ownership-issues.md 添加 RESOLVED banner - SUCCESSFUL - committed as 5ae23f3

[2026-04-17_Batch-2] - executor - Stage 1 完成 (Items 5-9): 重写 api/v1beta1/types.go (新增 Worker.Role/TeamRef, Team 瘦身, Human teamAccess/workerAccess/superAdmin), 在 Makefile 添加 generate target 并重新生成 zz_generated.deepcopy.go, 重写 3 份 CRD YAML - UNCONFIRMED - api 包编译通过且无 lint 错误。项目其他包预期会编译失败，待 Stage 2-11 修复。

<!-- END EXECUTION LOG -->

---

## Blocker / Issues 记录

（执行中遇到的阻塞问题、需要回 PLAN 模式讨论的偏差等，在此列出。）

---

## 完成度统计

- Stage 0 (Docs)：4 / 4
- Stage 1 (API Types)：5 / 5
- Stage 2 (Service)：0 / 4
- Stage 3 (Webhook)：0 / 5
- Stage 4 (Team Reconciler)：0 / 10
- Stage 5 (Human Reconciler)：0 / 7
- Stage 6 (Worker Reconciler)：0 / 5
- Stage 7 (Manager Reconciler)：0 / 4
- Stage 8 (Webhook Wiring)：0 / 3
- Stage 9 (Mocks/Fixtures)：0 / 7
- Stage 10 (REST API)：0 / 4
- Stage 11 (CLI)：0 / 6
- Stage 12 (Integration Tests)：0 / 5
- Stage 13 (Docs & Validation)：0 / 11

**Total: 9 / 80**
