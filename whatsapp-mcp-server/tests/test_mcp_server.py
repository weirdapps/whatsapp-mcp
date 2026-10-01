"""Surface tests for the WhatsApp MCP server.

These cover the parts the mcp 1.x -> 2.x major bump could break: the server
object, tool registration, schema generation from type hints, and a full
call_tool round trip through the v2 dispatcher (which now runs sync handlers
on worker threads). None of them need the Go bridge or a messages.db.
"""

from datetime import UTC, datetime
from unittest.mock import patch

import pytest
from mcp import Client
from mcp.server import MCPServer

import audio
import main
from whatsapp import Chat, Contact, Message, MessageContext

TS = datetime(2026, 7, 15, 18, 49, 11, tzinfo=UTC)


def _chat():
    return Chat(
        jid="123@s.whatsapp.net",
        name="Test Chat",
        last_message_time=TS,
        last_message="hello",
        last_sender="123",
        last_is_from_me=False,
    )


def _message():
    return Message(
        timestamp=TS,
        sender="123",
        content="hello",
        is_from_me=False,
        chat_jid="123@s.whatsapp.net",
        id="MSGID1",
        chat_name="Test Chat",
    )


EXPECTED_TOOLS = {
    "download_media",
    "get_chat",
    "get_contact_chats",
    "get_direct_chat_by_contact",
    "get_last_interaction",
    "get_message_context",
    "list_chats",
    "list_messages",
    "search_contacts",
    "send_audio_message",
    "send_file",
    "send_message",
}


def test_server_is_v2_mcpserver():
    """The module-level server object is a v2 MCPServer named 'whatsapp'."""
    assert isinstance(main.mcp, MCPServer)
    assert main.mcp.name == "whatsapp"


@pytest.mark.asyncio
async def test_all_twelve_tools_registered():
    tools = await main.mcp.list_tools()
    assert {t.name for t in tools} == EXPECTED_TOOLS


@pytest.mark.asyncio
async def test_tool_schemas_generated_from_type_hints():
    """Optional params, defaults and required lists survive the port."""
    tools = {t.name: t for t in await main.mcp.list_tools()}

    list_messages = tools["list_messages"].input_schema
    props = list_messages["properties"]
    # `limit: int = 20` -> integer with a default, not required
    assert props["limit"]["type"] == "integer"
    assert props["limit"]["default"] == 20
    # `chat_jid: str | None = None` -> nullable union with a null default
    assert props["chat_jid"]["default"] is None
    assert {"type": "string"} in props["chat_jid"]["anyOf"]
    assert list_messages.get("required") in (None, [])

    # Params without defaults stay required.
    assert set(tools["send_message"].input_schema["required"]) == {"recipient", "message"}

    # Docstrings still become tool descriptions.
    assert "Send a WhatsApp message" in tools["send_message"].description


@pytest.mark.asyncio
async def test_call_tool_round_trip_returns_structured_content():
    """Full v2 dispatch: client -> server -> sync handler on a worker thread.

    Uses send_message's empty-recipient guard so nothing touches the network.
    """
    async with Client(main.mcp) as client:
        result = await client.call_tool("send_message", {"recipient": "", "message": "hi"})

    assert result.is_error is False
    assert result.structured_content == {
        "success": False,
        "message": "Recipient must be provided",
    }


def test_audio_conversion_rejects_missing_input(tmp_path):
    """audio.py contract holds without ffmpeg installed."""
    with pytest.raises(FileNotFoundError):
        audio.convert_to_opus_ogg(str(tmp_path / "does-not-exist.m4a"))


# --- Output-contract regressions -------------------------------------------
#
# whatsapp.py hands back dataclasses, but the tools advertise dict/list output
# schemas. Returning the dataclass straight through made every read tool fail
# structured-output validation at runtime ("Input should be a valid
# dictionary"). These lock in the conversion at the tool boundary.


async def _call(tool, args):
    async with Client(main.mcp) as client:
        return await client.call_tool(tool, args)


@pytest.mark.asyncio
async def test_list_chats_returns_plain_dicts_not_dataclasses():
    with patch.object(main, "whatsapp_list_chats", return_value=[_chat()]):
        result = await _call("list_chats", {"limit": 1})

    assert result.is_error is False
    rows = result.structured_content["result"]
    assert isinstance(rows[0], dict)
    assert rows[0]["jid"] == "123@s.whatsapp.net"
    # datetime is serialised, not left as a Python object
    assert rows[0]["last_message_time"].startswith("2026-07-15T18:49:11")


@pytest.mark.asyncio
async def test_search_contacts_returns_plain_dicts_not_dataclasses():
    contact = Contact(phone_number="123", name="Someone", jid="123@s.whatsapp.net")
    with patch.object(main, "whatsapp_search_contacts", return_value=[contact]):
        result = await _call("search_contacts", {"query": "some"})

    assert result.is_error is False
    assert result.structured_content["result"] == [
        {"phone_number": "123", "name": "Someone", "jid": "123@s.whatsapp.net"}
    ]


@pytest.mark.asyncio
async def test_get_chat_returns_null_when_chat_missing():
    """whatsapp.get_chat returns None; the tool must model that, not blow up."""
    with patch.object(main, "whatsapp_get_chat", return_value=None):
        result = await _call("get_chat", {"chat_jid": "nope@s.whatsapp.net"})

    assert result.is_error is False
    assert result.structured_content["result"] is None


@pytest.mark.asyncio
async def test_get_message_context_serialises_nested_dataclasses():
    context = MessageContext(message=_message(), before=[_message()], after=[])
    with patch.object(main, "whatsapp_get_message_context", return_value=context):
        result = await _call("get_message_context", {"message_id": "MSGID1"})

    assert result.is_error is False
    payload = result.structured_content
    assert isinstance(payload["message"], dict)
    assert payload["message"]["id"] == "MSGID1"
    assert isinstance(payload["before"][0], dict)
    assert payload["after"] == []


@pytest.mark.asyncio
async def test_list_messages_is_a_transcript_string():
    """whatsapp.list_messages formats to text; the tool must advertise str."""
    with patch.object(main, "whatsapp_list_messages", return_value="[ts] From: Me: hi\n"):
        result = await _call("list_messages", {"limit": 1})

    assert result.is_error is False
    assert result.structured_content["result"] == "[ts] From: Me: hi\n"


def test_bridge_calls_carry_the_token(tmp_path, monkeypatch):
    """The bridge answers 401 without X-Bridge-Token, so every call must send it."""
    import whatsapp

    token_file = tmp_path / "api_token"
    token_file.write_text("abc123\n")
    monkeypatch.setattr(whatsapp, "BRIDGE_TOKEN_PATH", str(token_file))

    class _Ok:
        status_code = 200

        def json(self):
            return {"success": True, "message": "sent"}

    with patch("whatsapp.requests.post", return_value=_Ok()) as post:
        ok, _ = whatsapp.send_message("123", "hi")

    assert ok is True
    assert post.call_args.kwargs["headers"] == {"X-Bridge-Token": "abc123"}


def test_bridge_headers_tolerate_a_missing_token(tmp_path, monkeypatch):
    """Before the bridge's first start there is no token file; calls fail at the bridge, not here."""
    import whatsapp

    monkeypatch.setattr(whatsapp, "BRIDGE_TOKEN_PATH", str(tmp_path / "absent"))
    assert whatsapp._bridge_headers() == {"X-Bridge-Token": ""}
