---
name: hiclaw-test
description: HiClaw 完整测试周期：安装、卸载、运行测试、导出 debug log 分析问题。用于：(1) 验证 HiClaw 功能完整性 (2) CI/CD 测试验证 (3) 问题诊断和 debug (4) PR 合并前测试。触发词：测试 HiClaw、运行 HiClaw 测试、hiclaw test、make test、验证 HiClaw 安装。
---

# HiClaw Test Cycle

完整的 HiClaw 测试流程，包括安装验证、功能测试和问题诊断。

## 快速开始

```bash
# 1. 克隆/更新代码
git clone https://github.com/alibaba/hiclaw.git && cd hiclaw

# 2. 创建配置文件（首次）
cp hiclaw-manager.env.example ~/hiclaw-manager.env
# 编辑 ~/hiclaw-manager.env，设置 HICLAW_LLM_API_KEY 等

# 3. 运行完整测试
set -a && . ~/hiclaw-manager.env && set +a && make test
```

## 完整测试流程

### Step 1: 准备环境

```bash
# 克隆最新代码
git clone https://github.com/alibaba/hiclaw.git
cd hiclaw

# 检查配置文件是否存在
ls ~/hiclaw-manager.env
```

### Step 2: 运行完整测试

```bash
# 加载配置并运行测试（自动执行 install → test → uninstall）
set -a && . ~/hiclaw-manager.env && set +a && make test
```

测试用例说明：
- **test-01**: Manager 启动健康检查
- **test-02**: 创建 Worker Alice
- **test-03**: 分配任务给 Worker
- **test-04**: 人类干预补充指令
- **test-05**: 心跳查询机制
- **test-06**: 多 Worker 协作
- **test-08~14**: GitHub/MCP 相关测试（需要 HICLAW_GITHUB_TOKEN）

### Step 3: 单独测试安装/卸载

```bash
# 仅安装
set -a && . ~/hiclaw-manager.env && set +a && HICLAW_YOLO=1 make install

# 仅卸载
make uninstall

# 使用现有安装运行测试（跳过重新安装）
set -a && . ~/hiclaw-manager.env && set +a
./tests/run-all-tests.sh --skip-build --use-existing
```

## 导出 Debug Log

当测试失败或 hang 住时，使用 `hiclaw-debug.sh` 脚本导出日志：

```bash
# 在 hiclaw 仓库目录下
./tests/skill/scripts/hiclaw-debug.sh all

# 仅分析 hang 问题
./tests/skill/scripts/hiclaw-debug.sh analyze
```

### 手动导出日志

```bash
# Manager 容器日志
docker logs --tail 100 hiclaw-manager 2>&1

# Manager Agent 日志
docker exec hiclaw-manager tail -100 /var/log/hiclaw/manager-agent.log

# Manager Agent 错误日志
docker exec hiclaw-manager tail -50 /var/log/hiclaw/manager-agent-error.log

# Worker 容器日志
docker ps --filter "name=hiclaw-worker" --format "table {{.Names}}\t{{.Status}}"
docker logs --tail 50 hiclaw-worker-alice 2>&1

# 测试输出文件
ls tests/output/
cat tests/output/metrics-*.json
```

## 常见问题诊断

### 1. 测试 Hang 住

使用 `hiclaw-debug.sh` 分析 PHASE_DONE 消息的 mention 情况：

```bash
# 在 HiClaw 仓库目录下运行
./tests/skill/scripts/hiclaw-debug.sh analyze 1h

# 或者直接使用 export-debug-log.py
python3 scripts/export-debug-log.py --range 1h
```

`hiclaw-debug.sh` 会检查 Worker 发送的 PHASE_DONE 消息是否包含 `@manager`：
- ✅ 包含 `@manager` → 消息会被 Manager 处理
- ⚠️ 不包含 `@manager` → 消息被忽略，可能导致 hang

**常见原因**：多 phase 协作项目中，Worker 完成某个 phase 后没有 @mention Manager

**解决方案**：已在 v1.0.8+ 修复，Manager 会在 task spec 中添加 Multi-Phase Collaboration Protocol

### 2. Worker 没有响应

```bash
# 检查 Worker 容器是否运行
docker ps --filter "name=hiclaw-worker"

# 检查 Worker Agent 进程
docker exec hiclaw-worker-alice ps aux | grep openclaw
```

### 3. LLM 调用失败

```bash
# 检查错误日志
docker exec hiclaw-manager grep -i "error\|fail" /var/log/hiclaw/manager-agent-error.log
```

### 4. 测试超时

部分测试（如 test-14-git-collab）需要较长时间，可增加 timeout：

```bash
# 直接运行测试脚本，控制 timeout
timeout 1200 ./tests/run-all-tests.sh --skip-build --use-existing
```

## 测试结果解读

### 成功的测试

```
========================================
  Test Summary
========================================
  Total:  12
  [32mPassed: 12[0m
  [31mFailed: 0[0m
========================================
```

### 跳过的测试

```
[36m[TEST INFO][0m SKIP: No GitHub token configured
```

需要设置 `HICLAW_GITHUB_TOKEN` 环境变量。

### Metrics 文件

每个测试会生成 `metrics-XX-testname.json`，包含：
- LLM 调用次数
- Token 使用量
- 执行时间
- 缓存命中情况

## 清理环境

```bash
# 完整卸载
make uninstall

# 删除所有 Worker 容器
docker rm -f $(docker ps -aq --filter "name=hiclaw-worker")

# 删除测试代码
rm -rf ./hiclaw
```

## 参考资料

- [tests/README.md](https://github.com/alibaba/hiclaw/blob/main/tests/README.md) - 测试框架文档
- [install/README.md](https://github.com/alibaba/hiclaw/blob/main/install/README.md) - 安装说明
- [references/troubleshooting.md](references/troubleshooting.md) - 详细问题排查
