"""
LLM Agent for CoPaw Worker.
"""

import asyncio
import json
import re
from typing import Any, Optional

import httpx
from pydantic import BaseModel
from rich.console import Console

console = Console()


class Message(BaseModel):
    """Chat message."""

    role: str  # "system" | "user" | "assistant"
    content: str


class ToolCall(BaseModel):
    """Tool call from the model."""

    id: str
    name: str
    arguments: dict[str, Any]


class LLMResponse(BaseModel):
    """Response from LLM."""

    content: Optional[str] = None
    tool_calls: list[ToolCall] = []
    finish_reason: str = ""


class Agent:
    """LLM Agent with tool support."""

    def __init__(
        self,
        gateway_url: str,
        gateway_token: str,
        model: str = "qwen3.5-plus",
        system_prompt: Optional[str] = None,
        max_tokens: int = 4096,
        temperature: float = 0.7,
    ):
        """
        Initialize agent.

        Args:
            gateway_url: AI Gateway URL (e.g., "https://aigw-local.hiclaw.io")
            gateway_token: Gateway authentication token
            model: Model ID to use
            system_prompt: System prompt for the agent
            max_tokens: Maximum tokens in response
            temperature: Temperature for sampling
        """
        self.gateway_url = gateway_url.rstrip("/")
        self.gateway_token = gateway_token
        self.model = model
        self.system_prompt = system_prompt
        self.max_tokens = max_tokens
        self.temperature = temperature

        # Conversation history
        self.messages: list[Message] = []

        # Available tools (functions)
        self.tools: dict[str, callable] = {}

        # HTTP client - trust_env=False to bypass any system proxy settings
        self.http_client = httpx.AsyncClient(timeout=120.0, trust_env=False)

        # Add system prompt if provided
        if self.system_prompt:
            self.messages.append(Message(role="system", content=self.system_prompt))

    def register_tool(self, name: str, func: callable, description: str) -> None:
        """
        Register a tool (function) that the agent can call.

        Args:
            name: Tool name
            func: Async function to call
            description: Tool description for the model
        """
        self.tools[name] = {
            "func": func,
            "description": description,
        }

    def get_tools_schema(self) -> list[dict]:
        """Get OpenAI-compatible tools schema."""
        # TODO: Build proper schema from registered tools
        return []

    async def chat(self, user_message: str) -> str:
        """
        Send a message and get a response.

        Args:
            user_message: User's message

        Returns:
            Agent's response
        """
        # Add user message
        self.messages.append(Message(role="user", content=user_message))

        # Call LLM
        response = await self._call_llm()

        # Handle tool calls if any
        while response.tool_calls:
            # Execute tools
            tool_results = []
            for tool_call in response.tool_calls:
                result = await self._execute_tool(tool_call)
                tool_results.append(
                    {
                        "tool_call_id": tool_call.id,
                        "role": "tool",
                        "content": json.dumps(result),
                    }
                )

            # Add assistant message with tool calls
            self.messages.append(
                Message(
                    role="assistant",
                    content=response.content or "",
                )
            )

            # Add tool results
            for tr in tool_results:
                self.messages.append(
                    Message(
                        role="tool",
                        content=tr["content"],
                    )
                )

            # Get next response
            response = await self._call_llm()

        # Add final response
        if response.content:
            self.messages.append(Message(role="assistant", content=response.content))

        return response.content or ""

    async def _call_llm(self) -> LLMResponse:
        """Call the LLM API."""
        url = f"{self.gateway_url}/v1/chat/completions"

        headers = {
            "Authorization": f"Bearer {self.gateway_token}",
            "Content-Type": "application/json",
        }

        payload = {
            "model": self.model,
            "messages": [{"role": m.role, "content": m.content} for m in self.messages],
            "max_tokens": self.max_tokens,
            "temperature": self.temperature,
        }

        # Add tools if registered
        if self.tools:
            payload["tools"] = self.get_tools_schema()

        try:
            resp = await self.http_client.post(url, headers=headers, json=payload)
            resp.raise_for_status()
            data = resp.json()

            choice = data.get("choices", [{}])[0]
            message = choice.get("message", {})

            # Parse tool calls
            tool_calls = []
            for tc in message.get("tool_calls", []):
                tool_calls.append(
                    ToolCall(
                        id=tc.get("id", ""),
                        name=tc.get("function", {}).get("name", ""),
                        arguments=json.loads(
                            tc.get("function", {}).get("arguments", "{}")
                        ),
                    )
                )

            return LLMResponse(
                content=message.get("content"),
                tool_calls=tool_calls,
                finish_reason=choice.get("finish_reason", ""),
            )

        except httpx.HTTPError as e:
            console.print(f"[red]LLM API error: {e}[/red]")
            return LLMResponse(content="[Error communicating with LLM]", finish_reason="error")

    async def _execute_tool(self, tool_call: ToolCall) -> Any:
        """Execute a tool call."""
        tool_name = tool_call.name

        if tool_name not in self.tools:
            return {"error": f"Unknown tool: {tool_name}"}

        tool_info = self.tools[tool_name]
        func = tool_info["func"]

        try:
            result = await func(**tool_call.arguments)
            return result
        except Exception as e:
            console.print(f"[red]Tool error ({tool_name}): {e}[/red]")
            return {"error": str(e)}

    def set_system_prompt(self, prompt: str) -> None:
        """Update system prompt."""
        # Remove old system prompt if exists
        self.messages = [m for m in self.messages if m.role != "system"]
        self.messages.insert(0, Message(role="system", content=prompt))
        self.system_prompt = prompt

    def clear_history(self, keep_system: bool = True) -> None:
        """Clear conversation history."""
        if keep_system and self.system_prompt:
            self.messages = [Message(role="system", content=self.system_prompt)]
        else:
            self.messages = []

    async def close(self) -> None:
        """Close HTTP client."""
        await self.http_client.aclose()
