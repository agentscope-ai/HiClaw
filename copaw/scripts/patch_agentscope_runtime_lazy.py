#!/usr/bin/env python3
"""Patch agentscope_runtime to lazy-load heavyweight sub-modules.

When COPAW_HEADLESS=1, the runner skips OpenTelemetry tracing entirely
(no-op decorator) and defers the deployers import to the deploy() method
which copaw never calls.  AgentApp (FastAPI/uvicorn/a2a) is also lazy.

Net saving: ~100 MB RSS.

Usage:
    python patch_agentscope_runtime_lazy.py /path/to/site-packages/agentscope_runtime
"""

import sys
import textwrap
from pathlib import Path


def patch(site: Path) -> None:
    # ------------------------------------------------------------------ #
    # 1) engine/__init__.py — lazy-load AgentApp (saves ~4 MB)
    # ------------------------------------------------------------------ #
    (site / "engine/__init__.py").write_text(
        textwrap.dedent("""\
            # -*- coding: utf-8 -*-

            from typing import TYPE_CHECKING

            from .runner import Runner
            from ..common.utils.lazy_loader import install_lazy_loader

            if TYPE_CHECKING:
                from .app import AgentApp
                from .deployers import (
                    DeployManager,
                    LocalDeployManager,
                    KubernetesDeployManager,
                    KnativeDeployManager,
                    ModelstudioDeployManager,
                    AgentRunDeployManager,
                    FCDeployManager,
                )


            install_lazy_loader(
                globals(),
                {
                    "AgentApp": ".app",
                    "DeployManager": ".deployers",
                    "LocalDeployManager": ".deployers",
                    "KubernetesDeployManager": ".deployers",
                    "KnativeDeployManager": ".deployers",
                    "ModelstudioDeployManager": ".deployers",
                    "AgentRunDeployManager": ".deployers",
                    "FCDeployManager": ".deployers",
                },
            )
        """)
    )

    # ------------------------------------------------------------------ #
    # 2) engine/runner.py — conditional tracing + lazy deployers
    #    (saves ~96 MB: tracing=61 MB + deployers=35 MB)
    # ------------------------------------------------------------------ #
    runner = site / "engine/runner.py"
    src = runner.read_text()

    old_imports = (
        "from .deployers import (\n"
        "    DeployManager,\n"
        "    LocalDeployManager,\n"
        ")\n"
        "from .deployers.adapter.protocol_adapter import ProtocolAdapter\n"
        "from .schemas.agent_schemas import (\n"
        "    Event,\n"
        "    AgentRequest,\n"
        "    RunStatus,\n"
        "    AgentResponse,\n"
        "    SequenceNumberGenerator,\n"
        "    Error,\n"
        ")\n"
        "from .schemas.exception import AppBaseException, UnknownAgentException\n"
        "from .tracing import TraceType\n"
        "from .tracing.wrapper import trace\n"
        "from .tracing.message_util import (\n"
        "    merge_agent_response,\n"
        "    get_agent_response_finish_reason,\n"
        ")\n"
        "from .constant import ALLOWED_FRAMEWORK_TYPES"
    )

    new_imports = (
        "import os as _os\n"
        "\n"
        "from .schemas.agent_schemas import (\n"
        "    Event,\n"
        "    AgentRequest,\n"
        "    RunStatus,\n"
        "    AgentResponse,\n"
        "    SequenceNumberGenerator,\n"
        "    Error,\n"
        ")\n"
        "from .schemas.exception import AppBaseException, UnknownAgentException\n"
        "from .constant import ALLOWED_FRAMEWORK_TYPES\n"
        "\n"
        "if _os.getenv(\"COPAW_HEADLESS\"):\n"
        "    class TraceType:\n"
        "        AGENT_STEP = \"agent_step\"\n"
        "\n"
        "    def trace(*_args, **_kwargs):\n"
        "        def _decorator(func):\n"
        "            return func\n"
        "        return _decorator\n"
        "\n"
        "    def merge_agent_response(responses):\n"
        "        return responses[-1] if responses else None\n"
        "\n"
        "    def get_agent_response_finish_reason(response):\n"
        "        return getattr(response, \"status\", None)\n"
        "else:\n"
        "    from .tracing import TraceType\n"
        "    from .tracing.wrapper import trace\n"
        "    from .tracing.message_util import (\n"
        "        merge_agent_response,\n"
        "        get_agent_response_finish_reason,\n"
        "    )"
    )

    if old_imports not in src:
        print("  SKIP engine/runner.py — import block not found (already patched?)")
    else:
        src = src.replace(old_imports, new_imports)

        # Lazy-load deployers in deploy() method
        src = src.replace(
            "    async def deploy(\n"
            "        self,\n"
            "        deploy_manager: DeployManager = LocalDeployManager(),",
            "    async def deploy(\n"
            "        self,\n"
            "        deploy_manager=None,",
        )
        src = src.replace(
            "        deploy_result = await deploy_manager.deploy(",
            "        from .deployers import LocalDeployManager\n"
            "        if deploy_manager is None:\n"
            "            deploy_manager = LocalDeployManager()\n"
            "        deploy_result = await deploy_manager.deploy(",
            1,
        )
        src = src.replace(
            "protocol_adapters: Optional[list[ProtocolAdapter]] = None,",
            "protocol_adapters: Optional[list] = None,",
        )

        runner.write_text(src)

    print("agentscope_runtime patched — lazy engine/deployers/tracing")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(
            f"Usage: {sys.argv[0]} /path/to/site-packages/agentscope_runtime",
            file=sys.stderr,
        )
        sys.exit(1)
    patch(Path(sys.argv[1]))
