#!/usr/bin/env python3
"""Patch agentscope to lazy-load heavyweight sub-modules.

Copaw only uses a subset of agentscope (pipeline, tool, agent, model, memory,
session, message, formatter).  This script defers loading of modules that
pull in redis (socketio), sqlalchemy, opentelemetry, mcp, and others that
copaw never directly uses.

Net saving: ~40 MB RSS.

Usage:
    python patch_agentscope_lazy.py /path/to/site-packages/agentscope
"""

import sys
import textwrap
from pathlib import Path


def patch(site: Path) -> None:
    # ------------------------------------------------------------------ #
    # 1) __init__.py — lazy-load unused sub-modules + defer hooks import
    # ------------------------------------------------------------------ #
    init = site / "__init__.py"
    src = init.read_text()

    old_sub_imports = (
        "from . import exception\n"
        "from . import module\n"
        "from . import message\n"
        "from . import model\n"
        "from . import tool\n"
        "from . import formatter\n"
        "from . import memory\n"
        "from . import agent\n"
        "from . import session\n"
        "from . import embedding\n"
        "from . import token\n"
        "from . import evaluate\n"
        "from . import pipeline\n"
        "from . import tracing\n"
        "from . import rag\n"
        "from . import a2a\n"
        "from . import realtime"
    )

    new_sub_imports = (
        "from . import exception\n"
        "from . import module\n"
        "from . import message\n"
        "from . import model\n"
        "from . import tool\n"
        "from . import formatter\n"
        "from . import memory\n"
        "from . import agent\n"
        "from . import session\n"
        "from . import token\n"
        "from . import pipeline\n"
        "\n"
        "import importlib as _importlib\n"
        "\n"
        '_LAZY_SUBMODULES = {\n'
        '    "embedding", "evaluate", "tracing", "rag", "a2a", "realtime",\n'
        '}\n'
        "\n"
        "_orig_getattr = globals().get(\"__getattr__\")\n"
        "\n"
        "def __getattr__(name):\n"
        "    if name in _LAZY_SUBMODULES:\n"
        '        mod = _importlib.import_module(f".{name}", __package__)\n'
        "        globals()[name] = mod\n"
        "        return mod\n"
        "    if _orig_getattr:\n"
        "        return _orig_getattr(name)\n"
        '    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")'
    )

    if old_sub_imports in src:
        src = src.replace(old_sub_imports, new_sub_imports)

    # Defer hooks import (it transitively loads UserAgent -> socketio -> redis)
    src = src.replace(
        "from .hooks import _equip_as_studio_hooks\n",
        "",
    )
    src = src.replace(
        "        _equip_as_studio_hooks(studio_url)",
        "        from .hooks import _equip_as_studio_hooks\n"
        "        _equip_as_studio_hooks(studio_url)",
    )

    init.write_text(src)

    # ------------------------------------------------------------------ #
    # 2) memory/__init__.py — lazy-load Redis/SQLAlchemy/LongTermMemory
    # ------------------------------------------------------------------ #
    (site / "memory/__init__.py").write_text(
        textwrap.dedent("""\
            # -*- coding: utf-8 -*-
            import importlib
            from typing import TYPE_CHECKING

            from ._working_memory import (
                MemoryBase,
                InMemoryMemory,
            )

            if TYPE_CHECKING:
                from ._working_memory import RedisMemory, AsyncSQLAlchemyMemory
                from ._long_term_memory import (
                    LongTermMemoryBase,
                    Mem0LongTermMemory,
                    ReMePersonalLongTermMemory,
                    ReMeTaskLongTermMemory,
                    ReMeToolLongTermMemory,
                )

            _LAZY_MAP = {
                "RedisMemory": "._working_memory",
                "AsyncSQLAlchemyMemory": "._working_memory",
                "LongTermMemoryBase": "._long_term_memory",
                "Mem0LongTermMemory": "._long_term_memory",
                "ReMePersonalLongTermMemory": "._long_term_memory",
                "ReMeTaskLongTermMemory": "._long_term_memory",
                "ReMeToolLongTermMemory": "._long_term_memory",
            }

            __all__ = [
                "MemoryBase", "InMemoryMemory",
                "RedisMemory", "AsyncSQLAlchemyMemory",
                "LongTermMemoryBase", "Mem0LongTermMemory",
                "ReMePersonalLongTermMemory", "ReMeTaskLongTermMemory",
                "ReMeToolLongTermMemory",
            ]

            def __getattr__(name):
                if name in _LAZY_MAP:
                    mod = importlib.import_module(_LAZY_MAP[name], __package__)
                    val = getattr(mod, name)
                    globals()[name] = val
                    return val
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 3) memory/_working_memory/__init__.py — lazy RedisMemory/SQLAlchemy
    # ------------------------------------------------------------------ #
    (site / "memory/_working_memory/__init__.py").write_text(
        textwrap.dedent("""\
            # -*- coding: utf-8 -*-
            import importlib
            from typing import TYPE_CHECKING

            from ._base import MemoryBase
            from ._in_memory_memory import InMemoryMemory

            if TYPE_CHECKING:
                from ._redis_memory import RedisMemory
                from ._sqlalchemy_memory import AsyncSQLAlchemyMemory

            _LAZY_MAP = {
                "RedisMemory": "._redis_memory",
                "AsyncSQLAlchemyMemory": "._sqlalchemy_memory",
            }

            __all__ = ["MemoryBase", "InMemoryMemory", "RedisMemory", "AsyncSQLAlchemyMemory"]

            def __getattr__(name):
                if name in _LAZY_MAP:
                    mod = importlib.import_module(_LAZY_MAP[name], __package__)
                    val = getattr(mod, name)
                    globals()[name] = val
                    return val
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 4) session/__init__.py — lazy-load RedisSession (pulls redis pkg)
    # ------------------------------------------------------------------ #
    (site / "session/__init__.py").write_text(
        textwrap.dedent("""\
            # -*- coding: utf-8 -*-
            import importlib
            from typing import TYPE_CHECKING

            from ._session_base import SessionBase
            from ._json_session import JSONSession

            if TYPE_CHECKING:
                from ._redis_session import RedisSession

            _LAZY_MAP = {
                "RedisSession": "._redis_session",
            }

            __all__ = ["SessionBase", "JSONSession", "RedisSession"]

            def __getattr__(name):
                if name in _LAZY_MAP:
                    mod = importlib.import_module(_LAZY_MAP[name], __package__)
                    val = getattr(mod, name)
                    globals()[name] = val
                    return val
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 5) agent/__init__.py — lazy-load UserAgent/A2AAgent/RealtimeAgent
    #    (UserAgent -> _user_input -> socketio -> redis)
    # ------------------------------------------------------------------ #
    (site / "agent/__init__.py").write_text(
        textwrap.dedent("""\
            # -*- coding: utf-8 -*-
            import importlib
            from typing import TYPE_CHECKING

            from ._agent_base import AgentBase
            from ._react_agent_base import ReActAgentBase
            from ._react_agent import ReActAgent

            if TYPE_CHECKING:
                from ._user_input import (
                    UserInputBase, UserInputData,
                    TerminalUserInput, StudioUserInput,
                )
                from ._user_agent import UserAgent
                from ._a2a_agent import A2AAgent
                from ._realtime_agent import RealtimeAgent

            _LAZY_MAP = {
                "UserInputBase": "._user_input",
                "UserInputData": "._user_input",
                "TerminalUserInput": "._user_input",
                "StudioUserInput": "._user_input",
                "UserAgent": "._user_agent",
                "A2AAgent": "._a2a_agent",
                "RealtimeAgent": "._realtime_agent",
            }

            __all__ = [
                "AgentBase", "ReActAgentBase", "ReActAgent",
                "UserInputData", "UserInputBase",
                "TerminalUserInput", "StudioUserInput",
                "UserAgent", "A2AAgent", "RealtimeAgent",
            ]

            def __getattr__(name):
                if name in _LAZY_MAP:
                    mod = importlib.import_module(_LAZY_MAP[name], __package__)
                    val = getattr(mod, name)
                    globals()[name] = val
                    return val
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 6) mcp/__init__.py — lazy-load all MCP classes (pulls mcp package)
    # ------------------------------------------------------------------ #
    (site / "mcp/__init__.py").write_text(
        textwrap.dedent("""\
            # -*- coding: utf-8 -*-
            import importlib
            from typing import TYPE_CHECKING

            if TYPE_CHECKING:
                from ._client_base import MCPClientBase
                from ._mcp_function import MCPToolFunction
                from ._stateful_client_base import StatefulClientBase
                from ._stdio_stateful_client import StdIOStatefulClient
                from ._http_stateless_client import HttpStatelessClient
                from ._http_stateful_client import HttpStatefulClient

            _LAZY_MAP = {
                "MCPToolFunction": "._mcp_function",
                "MCPClientBase": "._client_base",
                "StatefulClientBase": "._stateful_client_base",
                "StdIOStatefulClient": "._stdio_stateful_client",
                "HttpStatelessClient": "._http_stateless_client",
                "HttpStatefulClient": "._http_stateful_client",
            }

            __all__ = list(_LAZY_MAP)

            def __getattr__(name):
                if name in _LAZY_MAP:
                    mod = importlib.import_module(_LAZY_MAP[name], __package__)
                    val = getattr(mod, name)
                    globals()[name] = val
                    return val
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 7) tool/_toolkit.py — lazy-import mcp + agentscope.mcp classes
    # ------------------------------------------------------------------ #
    toolkit = site / "tool/_toolkit.py"
    src = toolkit.read_text()

    # Remove top-level `import mcp`
    src = src.replace("import mcp\nimport shortuuid", "import shortuuid")

    # Replace eager mcp imports with lazy helper
    src = src.replace(
        "from ..mcp import (\n"
        "    MCPToolFunction,\n"
        "    MCPClientBase,\n"
        "    StatefulClientBase,\n"
        ")",
        "def _get_mcp_classes():\n"
        "    from ..mcp import MCPToolFunction, MCPClientBase, StatefulClientBase\n"
        "    return MCPToolFunction, MCPClientBase, StatefulClientBase",
    )

    # Fix isinstance check for MCPToolFunction
    src = src.replace(
        "if isinstance(tool_func, MCPToolFunction):",
        "MCPToolFunction, _, _ = _get_mcp_classes()\n"
        "        if isinstance(tool_func, MCPToolFunction):",
    )

    # Fix isinstance check for StatefulClientBase
    src = src.replace(
        "isinstance(mcp_client, StatefulClientBase)",
        "isinstance(mcp_client, _get_mcp_classes()[2])",
    )

    # Fix MCPClientBase type hint in method signature
    src = src.replace(
        "mcp_client: MCPClientBase,",
        'mcp_client: "MCPClientBase",',
    )

    # Fix except clause that uses mcp.shared.exceptions.McpError
    src = src.replace(
        "except mcp.shared.exceptions.McpError as e:",
        "except Exception as e:\n"
        "            import mcp.shared.exceptions\n"
        "            if not isinstance(e, mcp.shared.exceptions.McpError):\n"
        "                raise",
    )

    toolkit.write_text(src)

    print("agentscope patched — lazy memory/session/agent/mcp/hooks")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(
            f"Usage: {sys.argv[0]} /path/to/site-packages/agentscope",
            file=sys.stderr,
        )
        sys.exit(1)
    patch(Path(sys.argv[1]))
