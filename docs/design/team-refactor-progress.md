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

- [x] **10. Update service/interfaces.go**: add TeamProvisioner, TeamObserver; reshape WorkerDeployer (add WriteLeaderCoordinationContext)
- [x] **11. Rewrite service/provisioner.go team section**: EnsureTeamRooms, ReconcileTeamRoomMembership, EnsureTeamStorage (moved from Deployer), CleanupTeamInfra
- [x] **12. Update service/deployer.go**: remove InjectCoordinationContext + EnsureTeamStorage, add WriteLeaderCoordinationContext
- [x] **13. Create service/observer.go** implementing TeamObserver + WorkerObservation / HumanObservation
- [x] **12.5 (supporting)**: Extended matrix.Client with InviteRoom, KickRoom, ListRoomMembers required by ReconcileTeamRoomMembership; updated internal/app/app.go to pass OSS client into Provisioner

---

## Stage 3: Webhook Package

- [x] **14. Create internal/webhook/webhook.go** (Validators aggregate + RegisterWithManager)
- [x] **15. Create internal/webhook/worker_validator.go**
- [x] **16. Create internal/webhook/team_validator.go**
- [x] **17. Create internal/webhook/human_validator.go**
- [x] **18. Add webhook validator tests** (worker/team/human `_test.go`) — 30 table-driven cases all passing
- [x] **18.5 (supporting)**: Added internal/webhook/validators.go with shared helpers (validateDNSLabel, validateDuration, validateStringEnum, aggregateErrors)

---

## Stage 4: TeamReconciler 重写

- [x] **19. Delete old team_controller.go** (completed via overwrite with new content)
- [x] **20. Create new team_controller.go** (Reconcile loop + defer-patch status + SetupWithManager with Watches(Worker) + Watches(Human) + mappers + predicates)
- [x] **21. Create team_scope.go** (teamScope: team, patchBase, leader, members, multipleLeader, admins)
- [x] **22. Create team_phase.go** (computeTeamPhase + effectivePeerMentions helper)
- [x] **23. Create team_reconcile_members.go** (reconcileMembers + projectMembers/Leader + countReady + setCondition helper)
- [x] **24. Create team_reconcile_admins.go** (reconcileAdmins → Team.status.admins)
- [x] **25. Create team_reconcile_rooms.go** (reconcileRooms → EnsureTeamRooms + ReconcileTeamRoomMembership with desired sets)
- [x] **26. Create team_reconcile_storage.go** (reconcileStorage non-critical)
- [x] **27. Create team_reconcile_legacy.go** (teams-registry entry derived from status, nil-safe)
- [x] **28. Create team_reconcile_delete.go** (CleanupTeamInfra + registry remove + finalizer; **no Worker deletion**)
- [x] **28.5 (supporting)**: Deleted obsolete team_controller_test.go (tested removed helpers leaderHeartbeatEvery / summarizeTeamWorkerReadiness; replaced by integration tests in Stage 12)

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

[2026-04-17_Batch-2] - executor - Stage 1 完成 (Items 5-9): 重写 api/v1beta1/types.go (新增 Worker.Role/TeamRef, Team 瘦身, Human teamAccess/workerAccess/superAdmin), 在 Makefile 添加 generate target 并重新生成 zz_generated.deepcopy.go, 重写 3 份 CRD YAML - SUCCESSFUL - committed as d99f5b1

[2026-04-17_Batch-3] - executor - Stage 2 完成 (Items 10-13 + 支持改动): 扩展 matrix.Client (InviteRoom/KickRoom/ListRoomMembers); provisioner.go 重命名 ProvisionTeamRooms 为 EnsureTeamRooms 并新增 ReconcileTeamRoomMembership/CleanupTeamInfra/EnsureTeamStorage(移自 Deployer), ProvisionerConfig 新增 OSS 字段; deployer.go 移除 InjectCoordinationContext/CoordinationDeployRequest/EnsureTeamStorage 并新增 WriteLeaderCoordinationContext/LeaderCoordinationRequest; 新建 observer.go 实现 TeamObserver (ListTeamMembers/ListTeamAdmins) + WorkerObservation/HumanObservation 投影类型; 重写 interfaces.go 新增 TeamProvisioner/TeamObserver 接口 + 编译时满足性检查; app.go 向 Provisioner 传递 OSS 客户端 - SUCCESSFUL - committed as bf91b87

[2026-04-17_Batch-4] - executor - Stage 3 完成 (Items 14-18): 新建 internal/webhook 包; validators.go (共享 helpers: validateDNSLabel/validateDuration/validateStringEnum/aggregateErrors); worker_validator.go (WorkerValidator: role/teamRef 一致性 + runtime enum + state enum + DNS-1123 + leader 唯一性 peer check 通过 label selector); team_validator.go (heartbeat/workerIdleTimeout duration + DNS-1123); human_validator.go (superAdmin 排斥 teamAccess/workerAccess + teamAccess role enum + team 唯一性 + 必填校验); webhook.go (Validators 聚合结构 + RegisterWithManager 用于 incluster 模式 ValidatingWebhook); 3 份 table-driven 测试共 30 个 case 全部通过 - SUCCESSFUL - committed as a0b898a

[2026-04-17_Batch-5] - executor - Stage 4 完成 (Items 19-28): TeamReconciler 从 582 行旧单文件完全重写为 9 个 phase-based declarative 文件, 严格对齐 Worker/Manager reconciler 重构后的范式; team_controller.go (Reconcile 主循环 + patchBase + defer-patch status + ObservedGeneration 仅成功时写 + SetupWithManager 挂接 Watches(Worker, workerToTeamsMapper+workerTeamRefPredicates) 和 Watches(Human, humanToTeamsMapper+humanTeamAccessPredicates)); team_scope.go (teamScope 结构); team_phase.go (computeTeamPhase 基于 leader/rooms/multipleLeader 综合判定 Pending/Active/Degraded/Failed + effectivePeerMentions 默认值); team_reconcile_members.go (list Workers 分类 leader/workers, 检测 0/1/2+ leader, 写 LeaderResolved/NoLeader/MultipleLeaders/MembersHealthy conditions, 投影 Team.status.leader/members, setCondition 通用 upsert 工具); team_reconcile_admins.go (list Humans 过滤 teamAccess role=admin → Team.status.admins); team_reconcile_rooms.go (EnsureTeamRooms + ReconcileTeamRoomMembership with desired sets for Team Room + Leader DM Room, LeaderNotReady/TeamRoomReady conditions); team_reconcile_storage.go (非关键 EnsureTeamStorage 调用); team_reconcile_legacy.go (teams-registry 条目从 scope 观察派生而非 spec, nil-safe); team_reconcile_delete.go (finalizer path 只清理 CleanupTeamInfra + legacy registry, 永不删除 Worker CR); 删除过时 team_controller_test.go (测试移除的 helpers) - UNCONFIRMED - 9 份新文件全部编译干净无 lint 错误; controller 包剩余 4 个编译错误全部在 human_controller.go 是 Stage 5 scope

<!-- END EXECUTION LOG -->

---

## Blocker / Issues 记录

（执行中遇到的阻塞问题、需要回 PLAN 模式讨论的偏差等，在此列出。）

---

## 完成度统计

- Stage 0 (Docs)：4 / 4
- Stage 1 (API Types)：5 / 5
- Stage 2 (Service)：4 / 4
- Stage 3 (Webhook)：5 / 5
- Stage 4 (Team Reconciler)：10 / 10
- Stage 5 (Human Reconciler)：0 / 7
- Stage 6 (Worker Reconciler)：0 / 5
- Stage 7 (Manager Reconciler)：0 / 4
- Stage 8 (Webhook Wiring)：0 / 3
- Stage 9 (Mocks/Fixtures)：0 / 7
- Stage 10 (REST API)：0 / 4
- Stage 11 (CLI)：0 / 6
- Stage 12 (Integration Tests)：0 / 5
- Stage 13 (Docs & Validation)：0 / 11

**Total: 28 / 80**
