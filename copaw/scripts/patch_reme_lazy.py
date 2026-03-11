#!/usr/bin/env python3
"""Patch reme package to lazy-load heavyweight backends.

Copaw only uses ReMeCopaw and CoPawInMemoryMemory — it never touches
chromadb, elasticsearch, qdrant, pandas CacheHandler, or MCP services
directly.  This script rewrites several reme __init__.py files so that
those heavy transitive dependencies are only imported when explicitly
accessed.  Also defers ReMe/ReMeConfigParser and mcp.types.Tool to
prevent loading pandas/mcp/uvicorn at import time.  Net saving: ~100 MB RSS.

Usage:
    python patch_reme_lazy.py /path/to/site-packages/reme
"""

import sys
import textwrap
from pathlib import Path


def patch(site: Path) -> None:
    # ------------------------------------------------------------------ #
    # 1) core/__init__.py — lazy-load file_store, flow, op, service, llm,
    #    vector_store, file_watcher (they pull chromadb / pandas / mcp)
    # ------------------------------------------------------------------ #
    (site / "core/__init__.py").write_text(
        textwrap.dedent("""\
            import importlib

            from . import embedding
            from . import enumeration
            from . import schema
            from . import token_counter
            from . import utils
            from .base_dict import BaseDict
            from .prompt_handler import PromptHandler
            from .registry_factory import R, Registry, RegistryFactory
            from .runtime_context import RuntimeContext
            from .service_context import ServiceContext

            _LAZY_SUBMODULES = {
                "file_store", "file_watcher", "flow", "llm",
                "op", "service", "vector_store",
            }
            _LAZY_CLASSES = {"Application": ".application"}

            __all__ = [
                "embedding", "enumeration", "file_watcher", "flow", "llm",
                "file_store", "op", "schema", "service", "token_counter",
                "utils", "vector_store",
                "Application", "BaseDict", "PromptHandler",
                "R", "Registry", "RegistryFactory",
                "RuntimeContext", "ServiceContext",
            ]

            def __getattr__(name):
                if name in _LAZY_SUBMODULES:
                    mod = importlib.import_module("." + name, __package__)
                    globals()[name] = mod
                    return mod
                if name in _LAZY_CLASSES:
                    mod = importlib.import_module(_LAZY_CLASSES[name], __package__)
                    cls = getattr(mod, name)
                    globals()[name] = cls
                    return cls
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 2) core/utils/__init__.py — lazy-load common_utils (numpy),
    #    cache_handler (pandas), mcp_client, etc.
    # ------------------------------------------------------------------ #
    (site / "core/utils/__init__.py").write_text(
        textwrap.dedent("""\
            from .case_converter import snake_to_camel, camel_to_snake
            from .env_utils import load_env
            from .execute_utils import exec_code, run_shell_command, async_exec_code
            from .logger_utils import init_logger
            from .logo_utils import print_logo
            from .pydantic_config_parser import PydanticConfigParser
            from .singleton import singleton
            from .time import timer, get_now_time

            import importlib as _importlib

            _LAZY_ATTRS = {
                "convert_dashscope_to_agentscope": ".agentscope_utils",
                "CacheHandler": ".cache_handler",
                "chunk_markdown": ".chunking_utils",
                "run_coro_safely": ".common_utils",
                "execute_stream_task": ".common_utils",
                "hash_text": ".common_utils",
                "cosine_similarity": ".common_utils",
                "batch_cosine_similarity": ".common_utils",
                "play_horse_easter_egg": ".horse",
                "HttpClient": ".http_client",
                "extract_content": ".llm_utils",
                "format_messages": ".llm_utils",
                "deduplicate_memories": ".llm_utils",
                "MCPClient": ".mcp_client",
                "create_pydantic_model": ".pydantic_utils",
            }

            def __getattr__(name):
                if name in _LAZY_ATTRS:
                    mod = _importlib.import_module(_LAZY_ATTRS[name], __package__)
                    val = getattr(mod, name)
                    globals()[name] = val
                    return val
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 3) core/vector_store/__init__.py — lazy-load all backends
    # ------------------------------------------------------------------ #
    (site / "core/vector_store/__init__.py").write_text(
        textwrap.dedent("""\
            import importlib
            from .base_vector_store import BaseVectorStore

            _LAZY = {
                "ChromaVectorStore":  ".chroma_vector_store",
                "ESVectorStore":      ".es_vector_store",
                "LocalVectorStore":   ".local_vector_store",
                "PGVectorStore":      ".pgvector_store",
                "QdrantVectorStore":  ".qdrant_vector_store",
            }
            __all__ = ["BaseVectorStore"] + list(_LAZY)

            def __getattr__(name):
                if name in _LAZY:
                    mod = importlib.import_module(_LAZY[name], __package__)
                    cls = getattr(mod, name)
                    globals()[name] = cls
                    try:
                        from ..registry_factory import R
                        tag = name.replace("VectorStore", "").lower()
                        R.vector_stores.register(tag)(cls)
                    except Exception:
                        pass
                    return cls
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 4) core/file_store/__init__.py — lazy-load ChromaFileStore / Sqlite
    # ------------------------------------------------------------------ #
    (site / "core/file_store/__init__.py").write_text(
        textwrap.dedent("""\
            import importlib
            from .base_file_store import BaseFileStore
            from .local_file_store import LocalFileStore
            from ..registry_factory import R

            R.file_stores.register("local")(LocalFileStore)

            _LAZY = {
                "ChromaFileStore": ".chroma_file_store",
                "SqliteFileStore": ".sqlite_file_store",
            }
            __all__ = ["BaseFileStore", "LocalFileStore"] + list(_LAZY)

            def __getattr__(name):
                if name in _LAZY:
                    mod = importlib.import_module(_LAZY[name], __package__)
                    cls = getattr(mod, name)
                    globals()[name] = cls
                    try:
                        tag = name.replace("FileStore", "").lower()
                        R.file_stores.register(tag)(cls)
                    except Exception:
                        pass
                    return cls
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 5) core/op/__init__.py — lazy-load MCPTool (pulls in mcp package)
    # ------------------------------------------------------------------ #
    (site / "core/op/__init__.py").write_text(
        textwrap.dedent("""\
            import importlib
            from .base_op import BaseOp
            from .base_ray_op import BaseRayOp
            from .base_react import BaseReact
            from .base_react_stream import BaseReactStream
            from .base_tool import BaseTool
            from .parallel_op import ParallelOp
            from .sequential_op import SequentialOp
            from ..registry_factory import R

            __all__ = [
                "BaseOp", "BaseRayOp", "BaseReact", "BaseReactStream",
                "BaseTool", "MCPTool", "ParallelOp", "SequentialOp",
            ]

            def __getattr__(name):
                if name == "MCPTool":
                    from .mcp_tool import MCPTool
                    globals()["MCPTool"] = MCPTool
                    return MCPTool
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 6) core/service/__init__.py — lazy-load MCPService
    # ------------------------------------------------------------------ #
    (site / "core/service/__init__.py").write_text(
        textwrap.dedent("""\
            import importlib
            from .base_service import BaseService
            from .cmd_service import CmdService
            from .http_service import HttpService
            from ..registry_factory import R

            R.services.register("cmd")(CmdService)
            R.services.register("http")(HttpService)

            __all__ = ["BaseService", "CmdService", "HttpService", "MCPService"]

            def __getattr__(name):
                if name == "MCPService":
                    from .mcp_service import MCPService
                    globals()["MCPService"] = MCPService
                    R.services.register("mcp")(MCPService)
                    return MCPService
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 7) reme/__init__.py — fully lazy (ReMe + ReMeConfigParser deferred)
    # ------------------------------------------------------------------ #
    (site / "__init__.py").write_text(
        textwrap.dedent("""\
            import importlib

            __version__ = "0.3.0.5"

            _LAZY = {
                "config": ".config",
                "core": ".core",
                "extension": ".extension",
                "memory": ".memory",
                "ReMe": ".reme",
                "ReMeCli": ".reme_cli",
                "ReMeConfigParser": ".config",
            }
            __all__ = ["config", "core", "extension", "memory", "ReMe", "ReMeCli", "ReMeConfigParser"]

            def __getattr__(name):
                if name in _LAZY:
                    mod = importlib.import_module(_LAZY[name], __package__)
                    val = getattr(mod, name, mod)
                    globals()[name] = val
                    return val
                raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
        """)
    )

    # ------------------------------------------------------------------ #
    # 8) core/schema/tool_call.py — lazy-import mcp.types.Tool
    # ------------------------------------------------------------------ #
    tc = site / "core/schema/tool_call.py"
    src = tc.read_text()
    if "from mcp.types import Tool" in src:
        src = src.replace(
            "from mcp.types import Tool\n",
            "",
        )
        # Add TYPE_CHECKING guard
        src = src.replace(
            "from typing import Any, Union, Optional",
            "from typing import Any, Union, Optional, TYPE_CHECKING\n"
            "\n"
            "if TYPE_CHECKING:\n"
            "    from mcp.types import Tool",
        )
        # Fix type annotations that reference Tool at class-definition time
        src = src.replace(
            "def from_mcp_tool(cls, tool: Tool)",
            'def from_mcp_tool(cls, tool: "Tool")',
        )
        src = src.replace(
            "def to_mcp_tool(self) -> Tool:",
            'def to_mcp_tool(self) -> "Tool":',
        )
        # Lazy-import Tool inside to_mcp_tool method body
        src = src.replace(
            "        return Tool(",
            "        from mcp.types import Tool as _McpTool\n"
            "        return _McpTool(",
        )
        tc.write_text(src)

    # ------------------------------------------------------------------ #
    # 9) core/application.py — lazy-import MCPClient
    # ------------------------------------------------------------------ #
    app = site / "core/application.py"
    src = app.read_text()
    if "MCPClient" in src:
        src = src.replace(
            "from .utils import execute_stream_task, PydanticConfigParser, init_logger, MCPClient, print_logo",
            "from .utils import execute_stream_task, PydanticConfigParser, init_logger, print_logo",
        )
        src = src.replace(
            "        mcp_client = MCPClient(",
            "        from .utils import MCPClient\n"
            "        mcp_client = MCPClient(",
        )
        app.write_text(src)

    print("reme patched successfully — lazy-loading enabled for heavy backends")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(f"Usage: {sys.argv[0]} /path/to/site-packages/reme", file=sys.stderr)
        sys.exit(1)
    patch(Path(sys.argv[1]))
