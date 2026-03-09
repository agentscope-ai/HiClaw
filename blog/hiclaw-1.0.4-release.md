# HiClaw 1.0.4: Lightweight CoPaw Workers - 80% Less Memory, Direct Local Environment Access

> Release Date: March 10, 2026

---

## What Do We Mean by "Lightweight Workers"?

If you've used HiClaw, you're probably familiar with the Manager + Worker multi-agent collaboration pattern. A Manager acts as your "AI butler," managing multiple specialized Workers — frontend development, backend development, data analysis...

But in practice, we've received quite a bit of feedback:

**"Each Worker needs to run a full container, the memory pressure is significant"** — The default OpenClaw Worker container takes up about 500MB of memory. If you need to run 4-5 Workers simultaneously, an 8GB server starts to feel tight.

**"Workers run in containers and can't access my local environment"** — Some tasks require operating browsers, accessing local file systems, running desktop applications... These are impossible in an isolated container environment.

In version 1.0.4, we have an answer: **CoPaw Worker**.

---

## What is CoPaw?

[CoPaw](https://github.com/agentscope-ai/CoPaw) is a lightweight Python-based AI Agent runtime with these key features:

- **Lightweight**: Python-based, doesn't need the full Node.js stack, uses only 1/5 the memory of OpenClaw Worker
- **Console-friendly**: Built-in web console for real-time viewing of tool calls, thinking output, and execution process
- **Fast execution**: Native Python startup with quick cold starts
- **Easy to extend**: Tool definitions based on OpenAI SDK, low learning curve

HiClaw 1.0.4 integrates CoPaw into the multi-agent collaboration system by implementing a Matrix Channel and configuration bridge layer. The code footprint is small, but it unlocks many new possibilities.

---

## Two Deployment Modes, Solving Two Pain Points

### Mode 1: Docker Container Mode — More Memory-Efficient Workers

If you just need more Workers working in parallel without local environment access, **Docker-mode CoPaw Worker is the best choice**:

| Comparison | OpenClaw Worker | CoPaw Worker (Docker) |
|------------|-----------------|----------------------|
| Base Image | Node.js full stack | Python 3.11-slim |
| Memory Usage | ~500MB | ~100MB |
| Startup Speed | Slower | Faster |
| Security | Container isolation | Container isolation |

**80% reduction in memory usage**, with identical security.

This means you can run more Workers on the same hardware. Previously, 8GB of memory could only run 8-10 OpenClaw Workers; now you can run 40+ CoPaw Workers.

**On-demand Console, Save Another 500MB**

CoPaw Worker starts with a web console by default for easy debugging. But in production, you might not need a console for every Worker.

We provide an `enable-worker-console.sh` script to toggle the console on demand. With the console disabled, **each Worker saves another ~500MB of memory**.

```bash
# Disable Worker console (save memory)
/opt/hiclaw/scripts/enable-worker-console.sh alice disable

# Enable when debugging needed
/opt/hiclaw/scripts/enable-worker-console.sh alice enable
```

### Mode 2: Local Host Mode — Direct Access to Your Computer

Some tasks naturally require local environment access:

- **Browser operations**: Automated testing, web screenshots, data collection
- **Local file access**: Reading files on your desktop, operating local IDEs
- **Running desktop apps**: Automating Figma, Sketch, local database clients

These tasks can't be done in containers because containers are isolated environments.

**CoPaw Worker's local mode is designed for these tasks**:

```bash
# Manager will give you this command to run on your local machine
pip install copaw-worker && copaw-worker --config ... --console-port 8088
```

The Worker runs directly on your local machine with full local access permissions. At the same time, it still communicates with the Manager and other Workers via Matrix, seamlessly integrating into HiClaw's multi-agent collaboration system.

**Architecture Diagram:**

```
┌─────────────────────────────────────────────────────────────┐
│                    HiClaw Manager                            │
│                    (Container Environment)                   │
│                                                             │
│    Worker A (Docker)    Worker B (Docker)                   │
│    Frontend Dev          Backend Dev                        │
└─────────────────────────────────────────────────────────────┘
              ↑ Matrix Communication
              │
┌─────────────┴───────────────────────────────────────────────┐
│                    Your Local Computer                       │
│                                                             │
│    Worker C (CoPaw Local Mode)                              │
│    Browser Ops / Local File Access                          │
└─────────────────────────────────────────────────────────────┘
```

Local mode enables the console by default (`--console-port 8088`), so you can open `http://localhost:8088` to view the Worker's execution process in real-time.

---

## CoPaw Console: Visual Debugging Experience

Whether in Docker mode or local mode, CoPaw Workers can enable a web console.

The console shows real-time:

- **Thinking output**: What the Worker is thinking
- **Tool calls**: Which tools were called and with what parameters
- **Execution results**: What the tools returned
- **Error messages**: Where things went wrong

This is incredibly helpful for debugging and optimizing Agent behavior. Especially when you notice a Worker not behaving as expected, opening the console to check the thinking output often helps quickly identify the problem.

---

## Community-Driven Optimizations

Beyond the major CoPaw Worker feature, 1.0.4 also addresses a series of pain points reported by the community.

### More Controlled Model Switching

Previously, users reported that when switching models, the Manager might "take it upon itself" to modify other configurations, causing unexpected behavior.

1.0.4 extracts Worker model switching into a standalone `worker-model-switch` skill with more focused responsibilities and more predictable behavior. It also fixes the hardcoded model `input` field issue — now it's dynamically set based on whether the model supports vision capabilities.

### Workers No Longer "Chat Among Themselves"

In project group chats, Workers would sometimes have unnecessary conversations, wasting tokens.

1.0.4 optimizes Worker wake-up logic to ensure LLM calls are only triggered when @mentioned. It also fixes an issue where CoPaw MatrixChannel replies weren't carrying sender information, preventing the Manager from ignoring Worker replies and causing duplicate calls.

### AI Identity Awareness

An AI identity section has been added to SOUL.md to ensure Agents clearly know they are AI, not human. This avoids strange identity confusion issues, like Agents pretending to be real users.

```markdown
## My Role

You are an AI assistant powered by HiClaw. You help users complete tasks
through natural language interaction, but you are not a human.
```

### Token Consumption Baseline CI

1.0.4 adds a Token consumption baseline CI workflow to quantitatively analyze each version's token optimization effects.

In key workflows (creating Workers, assigning tasks, multi-Worker collaboration, etc.), CI records token consumption and compares it with the previous version. This enables:

- Quantifying optimization effects
- Detecting unexpected token regressions
- Providing data support for future optimizations

---

## How to Use CoPaw Workers?

### Select Runtime During Installation

The new installation script asks which Worker runtime you want as default:

```
Select default worker runtime:
  1) openclaw (~500MB, full-featured)
  2) copaw (~100MB, lightweight)

Enter your choice [1-2]:
```

After selection, `create-worker.sh` will use your chosen runtime by default.

### Create a CoPaw Worker

Just tell the Manager:

```
You: Help me create a Worker named browser-bot using CoPaw runtime

Manager: Sure, creating...
         Worker browser-bot created, runtime: copaw
         Memory usage: ~100MB
```

Or if you want local mode:

```
You: Help me create a local Worker named local-bot that can operate my browser

Manager: Sure, here's the installation command to run on your local machine:
         
         pip install copaw-worker
         copaw-worker --config ... --console-port 8088
         
         After running, the Worker will automatically connect to HiClaw.
```

---

## Acknowledgments

Thanks to the [CoPaw team](https://github.com/agentscope-ai/CoPaw) for their work! CoPaw is a well-designed lightweight Agent runtime with an especially excellent console experience. HiClaw's integration with CoPaw through Matrix Channel and configuration bridge layer was smooth, with minimal code required.

If you're interested in CoPaw itself, check out the [CoPaw GitHub repository](https://github.com/agentscope-ai/CoPaw).

---

## Upgrade Guide

If you're already using HiClaw 1.0.3 or earlier, upgrading to 1.0.4 is simple:

```bash
cd ~/hiclaw-install/higress  # or your installation directory
docker compose pull
docker compose up -d
```

After upgrading, the Manager will automatically support CoPaw Workers. Existing OpenClaw Workers are unaffected and will continue to run normally.

---

## Closing Thoughts

The core goal of HiClaw 1.0.4 is to make Workers lighter and more flexible:

- **Lighter**: CoPaw Workers use 80% less memory
- **More flexible**: Local mode unlocks new scenarios like browser automation
- **More controllable**: Better control over model switching and token consumption

We especially recommend trying CoPaw Workers if you:

- Need to run many Workers simultaneously but have limited memory
- Need Workers to operate browsers or access local files
- Want a lighter-weight Worker debugging experience

**Get Started Now:**

```bash
bash <(curl -sSL https://higress.ai/hiclaw/install.sh)
```

---

*HiClaw is an open-source project under the Apache 2.0 license. If you find it useful, please consider giving it a Star ⭐ and contributing code!*

**Related Links:**
- [GitHub Repository](https://github.com/alibaba/hiclaw)
- [Changelog v1.0.4](https://github.com/alibaba/hiclaw/blob/main/changelog/v1.0.4.md)
- [CoPaw GitHub](https://github.com/agentscope-ai/CoPaw)
