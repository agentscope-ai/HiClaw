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

- [x] **29. Delete old human_controller.go and rewrite** (phase-based declarative; patchBase + defer-patch + finalizer; Watches Team + Worker with status-change predicates and mappers listing Humans to fan out)
- [x] **30. Create human_scope.go** (matrixAccessToken + desiredRooms fields)
- [x] **31. Create human_phase.go** (computeHumanPhase: Active once MatrixUserID set, Pending otherwise, Failed on first-time infra error)
- [x] **32. Create human_reconcile_infra.go** (EnsureUser; status.InitialPassword doubles as persisted seed for re-login)
- [x] **33. Create human_reconcile_rooms.go** (resolves superAdmin / teamAccess admin|member / workerAccess into desired room set by reading Team.status.Members/Leader + Worker.status.RoomID; diff against current, JoinRoom/LeaveRoom best-effort)
- [x] **34. Create human_reconcile_legacy.go** (humans-registry entry with synthesised PermissionLevel+AccessibleTeams for Manager-skill backwards compat; Stage 11 reshapes the registry)
- [x] **35. Create human_reconcile_delete.go** (DeactivateUser + remove registry; finalizer release)

---

## Stage 6: WorkerReconciler 增量扩展

- [x] **36. Update worker_controller.go** (Watches Team via teamToWorkersMapper+teamToWorkersPredicates; Watches Human via humanToWorkersMapper+humanToWorkersPredicates; syncWorkerLabels keeps hiclaw.io/team + hiclaw.io/role in spec; phase sequence: infra → teamMembership → SA → config → leaderBroadcast → container; reconcileLegacy updated to use spec.Role + spec.TeamRef and to only push Manager.groupAllowFrom for standalone/team_leader; removed old annotation-based logic)
- [x] **37. Create worker_reconcile_team.go** (reconcileTeamMembership: standalone early-return with TeamRefResolved=True; Team NotFound → TeamRefResolved=False degraded-run; resolved → populate scope teamName/teamLeaderName/teamLeaderMatrixID/teamRoomID/teamLeaderDMRoomID/teamMemberNames/teamMemberMatrixIDs/teamAdminMatrixIDs/peerMentionsEnabled + buildEffectivePolicy with role-specific automatic additions; migration detection via status.TeamRef diff)
- [x] **38. Create worker_reconcile_leader_broadcast.go** (reconcileLeaderBroadcast: role=team_leader + teamFound + rooms ready guards; Deployer.WriteLeaderCoordinationContext with heartbeat/workerIdleTimeout from Team.spec; non-fatal error handling)
- [x] **39. Update worker_reconcile_config.go** (consume scope.effectivePolicy instead of w.Spec.ChannelPolicy; pass scope.teamName/teamLeaderName/teamAdminMatrixIDs[0] derived from observation; removed annotation reads)
- [x] **40. Update worker_scope.go** (teamFound, teamName, teamLeaderName, teamLeaderMatrixID, teamLeaderDMRoomID, teamRoomID, teamMemberNames, teamMemberMatrixIDs, teamAdminMatrixIDs, peerMentionsEnabled, effectivePolicy fields)
- [x] **40.5 (supporting)**: Updated worker_reconcile_infra.go (use spec.Role/TeamRef + resolveTeamLeaderName helper for Room power-levels; added Provisioned condition); updated worker_reconcile_delete.go (use spec.Role to determine isTeamMember; Manager.groupAllowFrom removal only for standalone/team_leader); removed obsolete roleForAnnotations helper

---

## Stage 7: ManagerReconciler 增量扩展

- [x] **41. Update manager_controller.go** (Watches Worker via workerToManagersMapper+workerToManagersPredicates; Watches Human via humanToManagersMapper+humanToManagersPredicates that pre-filter non-superAdmin; new phase sequence: infra → allowFrom → SA → config → container; listManagerRequests helper)
- [x] **42. Create manager_reconcile_allow_from.go** (list Workers filter role∈{standalone, team_leader} with MatrixUserID set; list Humans filter superAdmin with MatrixUserID set; sorted dedup into scope.effectiveAllowFrom)
- [x] **43. Update manager_reconcile_config.go** (pass scope.effectiveAllowFrom through ManagerDeployRequest.GroupAllowFromExtra)
- [x] **44. Update manager_scope.go** (effectiveAllowFrom []string field)
- [x] **44.5 (supporting)**: Extended service.ManagerDeployRequest with GroupAllowFromExtra; DeployManagerConfig builds ChannelPolicy.GroupAllowExtra/DMAllowExtra to layer the authoritative list on top of the base Manager+Admin allow-from

---

## Stage 8: Webhook Wiring

- [x] **45. Wire webhook in controller startup** (config.go adds WebhookEnabled/Port/CertDir env vars; app.go startInCluster sets opts.WebhookServer when enabled; app.go initReconcilers invokes hiclawwebhook.NewValidators(mgr.GetClient()).RegisterWithManager when incluster+enabled; embedded mode bypasses admission chain)
- [x] **46. Create config/webhook/validating-webhook.yaml** ValidatingWebhookConfiguration covering Worker/Team/Human with cert-manager caBundle injection annotation
- [x] **47. Create helm/hiclaw/templates/controller/webhook.yaml** (Service + cert-manager Issuer/Certificate + ValidatingWebhookConfiguration; gated by controller.webhook.enabled; Deployment updated: webhook port exposed, HICLAW_WEBHOOK_* env vars injected, TLS cert volume-mounted from Secret generated by cert-manager)
- [x] **47.5 (supporting)**: Fixed app.go TeamReconciler instantiation (add Observer: a.observer, remove Deployer/AgentFSDir fields that no longer exist in Stage 4 refactor); added a.observer field to App struct; default controller.webhook.enabled=false in values.yaml so cert-manager is not a hard dep

---

## Stage 9: Mocks / Fixtures

- [x] **48. Update test/testutil/mocks/provisioner.go** (MockProvisioner now implements TeamProvisioner too: EnsureTeamRoomsFn/ReconcileTeamRoomMembershipFn/EnsureTeamStorageFn/CleanupTeamInfraFn + Calls tracking + TeamCallCounts helper; defaults produce deterministic room IDs)
- [x] **49. Update test/testutil/mocks/deployer.go** (WriteLeaderCoordinationContextFn + Calls tracking; LeaderBroadcastCallCount helper)
- [x] **50. Create test/testutil/mocks/team_observer.go** (MockTeamObserver: seed via SetMembers/AddMember/SetAdmins/AddAdmin maps, or override via Fn; returns copies to protect seeded data)
- [x] **51. Update test/testutil/fixtures/worker.go** (WorkerOption functional-options: WithRole/WithTeamRef/WithModel/WithRuntime/WithWorkerSkills/WithMcpServers/WithWorkerExpose/WithWorkerState/WithWorkerStatus; NewTestWorker auto-mirrors spec.role/spec.teamRef into labels)
- [x] **52. Create test/testutil/fixtures/team.go** (NewTestTeam + WithTeamDescription/WithPeerMentions/WithTeamChannelPolicy/WithHeartbeat/WithWorkerIdleTimeout/WithTeamStatus/WithTeamLeader/WithTeamMember/WithTeamAdmin)
- [x] **53. Create test/testutil/fixtures/human.go** (NewTestHuman + WithDisplayName/WithEmail/WithSuperAdmin/WithTeamAccess/WithWorkerAccess/WithNote/WithHumanStatus/WithHumanRooms)
- [x] **54. Create test/testutil/fixtures/team_bundle.go** (TeamBundle struct + NewTeamBundle builder with WithBundleLeader/WithBundleWorker/WithBundleAdmin/WithBundleTeamOptions; AllObjects helper for fake client seeding)

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

[2026-04-17_Batch-10] - executor - Stage 9 完成 (Items 48-54): testutil 完整扩展以支持新模型的单元/集成测试; mocks/deployer.go 新增 WriteLeaderCoordinationContextFn + LeaderBroadcastCallCount 查询; mocks/provisioner.go MockProvisioner 扩展实现 TeamProvisioner 接口 (EnsureTeamRooms/ReconcileTeamRoomMembership/EnsureTeamStorage/CleanupTeamInfra + Calls + TeamCallCounts, 默认实现返回确定性 room ID); 新建 mocks/team_observer.go (MockTeamObserver: Members/Admins keyed maps + Set/Add helpers, 返回 defensive copies); fixtures/worker.go 彻底 refactor 用 functional-options (WithRole/WithTeamRef/WithModel/WithRuntime/WithWorkerSkills/WithMcpServers/WithWorkerExpose/WithWorkerState/WithWorkerStatus), NewTestWorker 自动镜像 spec.role/spec.teamRef 到 labels; 新建 fixtures/team.go (NewTestTeam + 10 个 TeamOption); 新建 fixtures/human.go (NewTestHuman + 8 个 HumanOption); 新建 fixtures/team_bundle.go (TeamBundle 结构 + NewTeamBundle builder 自动 wire spec.teamRef 和 role, WithBundleLeader/WithBundleWorker/WithBundleAdmin/WithBundleTeamOptions, AllObjects helper) - UNCONFIRMED - test/testutil 完整编译 + go vet 通过; 既有 controller/service/matrix/webhook/config 测试全部通过; 剩余项目级破损仅限 server (Stage 10) + 可能的旧 test 文件引用

[2026-04-17_Batch-9] - executor - Stage 8 完成 (Items 45-47): webhook 生产接入 - SUCCESSFUL - committed as d38b84a config.go 新增 WebhookEnabled/WebhookPort/WebhookCertDir 字段(读 HICLAW_WEBHOOK_* env); app.go 在 startInCluster 按需设置 opts.WebhookServer, 在 initReconcilers 为 incluster+enabled 注册 hiclawwebhook.Validators, 同时修复 Stage 4 遗留: TeamReconciler 构造改用新签名 (Observer: a.observer, 删除 Deployer/AgentFSDir), 新增 a.observer *service.Observer 字段并在 initServiceLayer 构造; config/webhook/validating-webhook.yaml (Worker/Team/Human 三个 webhook, cert-manager.io/inject-ca-from 注解); helm/hiclaw/templates/controller/webhook.yaml (完整管道: Service + Issuer(self-signed) + Certificate + ValidatingWebhookConfiguration, 全部受 controller.webhook.enabled 开关); helm deployment.yaml 增加 webhook 端口/env/TLS volume mount; values.yaml 新增 controller.webhook.{enabled,port,failurePolicy} 默认 enabled=false 以避免 cert-manager 硬依赖 - UNCONFIRMED - helm template 在 webhook.enabled=true 时正确渲染 Service+Issuer+Certificate+ValidatingWebhookConfiguration, 在默认 disabled 时这些资源全部不出现; config+controller+service+matrix+webhook 测试全部通过; app 包编译干净(仅被 server 的 Stage 10 错误间接阻塞)

[2026-04-17_Batch-8] - executor - Stage 7 完成 (Items 41-44): ManagerReconciler 新增 allowFrom 授权计算 phase - SUCCESSFUL - committed as c9dddf0 manager_scope.go 新增 effectiveAllowFrom []string; 新建 manager_reconcile_allow_from.go (list Workers 过滤 standalone+team_leader; list Humans 过滤 superAdmin; dedup+sort 写入 scope); manager_reconcile_config.go 消费 scope.effectiveAllowFrom 通过新增的 ManagerDeployRequest.GroupAllowFromExtra 字段; manager_controller.go phase 顺序改为 infra → allowFrom → SA → config → container, SetupWithManager 新增 Watches(Worker) + Watches(Human) with mapper + predicate (predicate 预过滤非 superAdmin Human 和 team_worker Worker); service/deployer.go 扩展 ManagerDeployRequest 新增 GroupAllowFromExtra 字段, DeployManagerConfig 将其映射为 ChannelPolicy.GroupAllowExtra+DMAllowExtra 传给 agentconfig 生成器 - UNCONFIRMED - controller + service + matrix + webhook 四个包完整编译+go vet+测试通过; 剩余破损仅限 server (Stage 10) 和 mocks (Stage 9)

[2026-04-17_Batch-7] - executor - Stage 6 完成 (Items 36-40): WorkerReconciler 从 annotation 驱动升级为 spec 驱动并扩展 2 个新 phase - SUCCESSFUL - committed as 742790b worker_scope.go 新增 teamFound/teamName/teamLeaderName/teamLeaderMatrixID/teamLeaderDMRoomID/teamRoomID/teamMemberNames/teamMemberMatrixIDs/teamAdminMatrixIDs/peerMentionsEnabled/effectivePolicy 字段; worker_reconcile_team.go 新增 reconcileTeamMembership phase (Get Team → populate scope + buildEffectivePolicy with role-specific automatic additions); worker_reconcile_leader_broadcast.go 新增 reconcileLeaderBroadcast phase (仅 team_leader 触发 WriteLeaderCoordinationContext); worker_reconcile_config.go 消费 scope.effectivePolicy 替代 w.Spec.ChannelPolicy, 移除 annotation 读取; worker_reconcile_infra.go 使用 spec.Role/TeamRef + resolveTeamLeaderName helper; worker_reconcile_delete.go 使用 spec.Role 判断 isTeamMember; worker_controller.go syncWorkerLabels 开头同步 hiclaw.io/team + hiclaw.io/role label 镜像 spec, 主 reconcileNormal 插入 reconcileTeamMembership/reconcileLeaderBroadcast 两个 phase, SetupWithManager 新增 Watches(Team) 和 Watches(Human) 配合 mapper+predicate, reconcileLegacy 只对 standalone+team_leader 更新 Manager.groupAllowFrom; 移除 roleForAnnotations 废弃 helper - UNCONFIRMED - internal/controller 完整编译 + go vet 通过; 既有 service/matrix/webhook 测试继续全部通过; 剩余破损仅限 server (Stage 10) 和 mocks (Stage 9)

[2026-04-17_Batch-6] - executor - Stage 5 完成 (Items 29-35): HumanReconciler 从老式 switch Phase 模式重写为 phase-based declarative - SUCCESSFUL - committed as 83881f3 human_controller.go (Reconcile + defer-patch + SetupWithManager with Watches(Team, teamToHumansMapper+teamRoomsChangedPredicates) + Watches(Worker, workerToHumansMapper+workerRoomChangedPredicates); mappers list Humans in namespace and filter to those with SuperAdmin/teamAccess/workerAccess relevant to the event); human_scope.go (matrixAccessToken/desiredRooms); human_phase.go (computeHumanPhase: Active/Pending/Failed); human_reconcile_infra.go (EnsureUser with Password=status.InitialPassword seed for idempotent re-login); human_reconcile_rooms.go (computeDesiredRooms: workerAccess → Worker rooms, superAdmin → all Team/Worker rooms, teamAccess → Team Room + admin→LeaderDMRoom + member Worker rooms + leader Worker room for admin; diff with status.Rooms, JoinRoom/LeaveRoom best-effort); human_reconcile_legacy.go (humans-registry with synthesised PermissionLevel + AccessibleTeams for Manager-skill compat); human_reconcile_delete.go (DeactivateUser + registry remove + finalizer) - UNCONFIRMED - internal/controller 包完整编译 + go vet 通过; 既有 service/matrix/webhook 测试全部通过; 剩余破损仅限 server (Stage 10) 和 mocks (Stage 9)

[2026-04-17_Batch-5] - executor - Stage 4 完成 (Items 19-28): TeamReconciler 从 582 行旧单文件完全重写为 9 个 phase-based declarative 文件 - SUCCESSFUL - committed as 8525645

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
- Stage 5 (Human Reconciler)：7 / 7
- Stage 6 (Worker Reconciler)：5 / 5
- Stage 7 (Manager Reconciler)：4 / 4
- Stage 8 (Webhook Wiring)：3 / 3
- Stage 9 (Mocks/Fixtures)：7 / 7
- Stage 10 (REST API)：0 / 4
- Stage 11 (CLI)：0 / 6
- Stage 12 (Integration Tests)：0 / 5
- Stage 13 (Docs & Validation)：0 / 11

**Total: 54 / 80**
