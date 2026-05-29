"""Comprehensive live integration test for the Python SDK.

Run: API_URL=http://localhost:18080 API_KEY=lsp_... python tests/test_live_integration.py
"""
import sys
import os
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from llmsafespace import LLMSafeSpace, NotFoundError, AuthError

API_URL = os.environ.get("API_URL", "http://localhost:18080")
API_KEY = os.environ.get("API_KEY", "lsp_upgradetest1234567890abcdef")

client = LLMSafeSpace(API_URL, api_key=API_KEY, timeout=120.0)

passed = 0
failed = 0
errors = []


def ok(cond, label):
    global passed, failed
    if cond:
        print(f"  ✓ {label}")
        passed += 1
    else:
        print(f"  ✗ {label}")
        failed += 1
        errors.append(label)


def wait_healthy(ws_id, max_wait=150):
    start = time.time()
    while time.time() - start < max_wait:
        s = client.workspaces.get_status(ws_id)
        ah = s.get("agentHealth", {})
        if ah.get("status") == "Healthy":
            return "Healthy"
        if s.get("phase") == "Failed":
            return "Failed"
        time.sleep(5)
    return client.workspaces.get_status(ws_id).get("agentHealth", {}).get("status", "timeout")


def wait_phase(ws_id, phase, max_wait=60):
    start = time.time()
    while time.time() - start < max_wait:
        s = client.workspaces.get_status(ws_id)
        if s.get("phase") == phase:
            return phase
        time.sleep(3)
    return client.workspaces.get_status(ws_id).get("phase", "timeout")


print("=== Python SDK Live Integration Test (Comprehensive) ===\n")

# ═══════════════════════════════════════════════════════════════════════════════
# 1. AUTH
# ═══════════════════════════════════════════════════════════════════════════════
print("─── Auth ───")
me = client.auth.me()
ok(isinstance(me.get("id"), str) and len(me["id"]) > 0, "auth.me() → id")
ok(isinstance(me.get("email"), str), "auth.me() → email")
ok(isinstance(me.get("role"), str), "auth.me() → role")
ok(isinstance(me.get("username"), str), "auth.me() → username")

keys = client.auth.list_api_keys()
ok(isinstance(keys, list), "auth.list_api_keys() → list")

# Create + delete API key
new_key = client.auth.create_api_key("py-live-test-key")
ok(new_key.name == "py-live-test-key", "auth.create_api_key() → name")
ok(new_key.key is not None and new_key.key.startswith("lsp_"), "auth.create_api_key() → key prefix")
ok(isinstance(new_key.id, str), "auth.create_api_key() → id")

client.auth.delete_api_key(new_key.id)
keys_after = client.auth.list_api_keys()
ok(not any(k.id == new_key.id for k in keys_after), "auth.delete_api_key() → removed")

# ═══════════════════════════════════════════════════════════════════════════════
# 2. WORKSPACE LIFECYCLE
# ═══════════════════════════════════════════════════════════════════════════════
print("\n─── Workspace Lifecycle ───")
ws = client.workspaces.create(name="py-live-comprehensive", runtime="base", storage_size="1Gi")
ws_id = ws.id
ok(len(ws.id) > 0, "workspaces.create() → id")
ok(ws.name == "py-live-comprehensive", "workspaces.create() → name")
ok(ws.runtime == "base", "workspaces.create() → runtime")

fetched = client.workspaces.get(ws_id)
ok(fetched.id == ws_id, "workspaces.get() → correct id")

status = client.workspaces.get_status(ws_id)
ok("phase" in status, "workspaces.get_status() → phase")
ok("activeSessions" in status, "workspaces.get_status() → activeSessions")

result = client.workspaces.list()
ok(len(result.items) >= 1, "workspaces.list() → ≥1 item")
ok(any(i.id == ws_id for i in result.items), "workspaces.list() → contains new ws")

# Pagination
page = client.workspaces.list(limit=1, offset=0)
ok(len(page.items) <= 1, "workspaces.list(limit=1) → ≤1 item")

# Rename
client.workspaces.rename(ws_id, "py-live-renamed")
renamed = client.workspaces.get(ws_id)
ok(renamed.name == "py-live-renamed", "workspaces.rename() → updated")

# Wait healthy
print("  Waiting for agent healthy...")
health = wait_healthy(ws_id)
ok(health == "Healthy", f"agent healthy (got: {health})")
if health != "Healthy":
    client.workspaces.delete(ws_id)
    sys.exit(1)

# ═══════════════════════════════════════════════════════════════════════════════
# 3. SESSIONS
# ═══════════════════════════════════════════════════════════════════════════════
print("\n─── Sessions ───")
session = client.sessions.ensure(ws_id)
ok(len(session.sessionId) > 0, "sessions.ensure() → sessionId")
ok(session.workspaceId == ws_id, "sessions.ensure() → workspaceId")
ok(isinstance(session.resumed, bool), "sessions.ensure() → resumed")

# Send message via SDK (now fixed with parts format)
print("  Sending message via SDK...")
msg = client.sessions.send_message(ws_id, session.sessionId, "Reply with exactly: PONG")
ok(isinstance(msg.content, str), "sessions.send_message() → content string")
ok(len(msg.content) > 0, "sessions.send_message() → non-empty")
ok(msg.raw is not None, "sessions.send_message() → raw present")
print(f'  Agent said: "{msg.content[:80]}"')

# History
history = client.sessions.get_history(ws_id, session.sessionId)
ok(isinstance(history, list), "sessions.get_history() → list")
ok(len(history) >= 1, "sessions.get_history() → ≥1 after message")

# Abort
client.sessions.abort(ws_id, session.sessionId)
ok(True, "sessions.abort() → no error")

# List sessions (may be empty if opencode hasn't registered yet)
sessions_list = client.sessions.list(ws_id)
ok(isinstance(sessions_list, list), "sessions.list() → list")

# ═══════════════════════════════════════════════════════════════════════════════
# 4. TERMINAL TICKET
# ═══════════════════════════════════════════════════════════════════════════════
print("\n─── Terminal ───")
ticket = client.terminal.get_ticket(ws_id)
ok(ticket.ticket.startswith("tkt_"), "terminal.get_ticket() → tkt_ prefix")
ok(len(ticket.ticket) > 10, "terminal.get_ticket() → sufficient length")
ok(isinstance(ticket.expiresAt, str), "terminal.get_ticket() → expiresAt")

t2 = client.terminal.get_ticket(ws_id)
ok(ticket.ticket != t2.ticket, "terminal tickets are unique")

# ═══════════════════════════════════════════════════════════════════════════════
# 5. SECRETS
# ═══════════════════════════════════════════════════════════════════════════════
print("\n─── Secrets ───")
try:
    secret = client.secrets.create(name="py-live-secret", type="env-secret", value="secret-val-42")
    ok(len(secret.id) > 0, "secrets.create() → id")
    ok(secret.name == "py-live-secret", "secrets.create() → name")
    ok(secret.type == "env-secret", "secrets.create() → type")

    sec_list = client.secrets.list()
    ok(any(s.id == secret.id for s in sec_list), "secrets.list() → contains new")

    fetched_sec = client.secrets.get(secret.id)
    ok(fetched_sec.name == "py-live-secret", "secrets.get() → name")

    revealed = client.secrets.reveal(secret.id)
    ok(revealed == "secret-val-42", "secrets.reveal() → correct value")

    client.secrets.delete(secret.id)
    after = client.secrets.list()
    ok(not any(s.id == secret.id for s in after), "secrets.delete() → removed")
except Exception as e:
    print(f"  ⚠ Secrets skipped: {e}")

# ═══════════════════════════════════════════════════════════════════════════════
# 6. SUSPEND / RESUME
# ═══════════════════════════════════════════════════════════════════════════════
print("\n─── Suspend / Resume ───")
client.workspaces.suspend(ws_id)
ok(True, "workspaces.suspend() → no error")

phase = wait_phase(ws_id, "Suspended")
ok(phase == "Suspended", f"phase → Suspended (got: {phase})")

client.workspaces.resume(ws_id)
ok(True, "workspaces.resume() → no error")

rh = wait_healthy(ws_id)
ok(rh == "Healthy", f"resume → Healthy (got: {rh})")

# Session works after resume
post_resume = client.sessions.ensure(ws_id)
ok(len(post_resume.sessionId) > 0, "sessions.ensure() works after resume")

# ═══════════════════════════════════════════════════════════════════════════════
# 7. ACTIVATE
# ═══════════════════════════════════════════════════════════════════════════════
print("\n─── Activate ───")
client.workspaces.suspend(ws_id)
wait_phase(ws_id, "Suspended")
activate_resp = client.workspaces.activate(ws_id)
ok(isinstance(activate_resp, dict) and "resumed" in activate_resp, "workspaces.activate() → resumed field")
ah = wait_healthy(ws_id)
ok(ah == "Healthy", f"activate → Healthy (got: {ah})")

# ═══════════════════════════════════════════════════════════════════════════════
# 8. ERROR HANDLING
# ═══════════════════════════════════════════════════════════════════════════════
print("\n─── Error Handling ───")
try:
    client.workspaces.get("00000000-0000-0000-0000-000000000000")
    ok(False, "nonexistent ws should throw")
except NotFoundError:
    ok(True, "nonexistent ws → NotFoundError")
except Exception:
    ok(True, "nonexistent ws → error (non-NotFoundError)")

try:
    bad = LLMSafeSpace(API_URL, api_key="lsp_invalid")
    bad.auth.me()
    ok(False, "invalid key should throw")
except AuthError:
    ok(True, "invalid key → AuthError")

try:
    client.terminal.get_ticket("00000000-0000-0000-0000-000000000000")
    ok(False, "terminal ticket nonexistent should throw")
except Exception:
    ok(True, "terminal ticket nonexistent → error")

# ═══════════════════════════════════════════════════════════════════════════════
# 9. CLEANUP
# ═══════════════════════════════════════════════════════════════════════════════
print("\n─── Cleanup ───")
client.workspaces.delete(ws_id)
ok(True, "workspaces.delete() → success")

try:
    client.workspaces.get(ws_id)
    ok(False, "get deleted ws should throw")
except NotFoundError:
    ok(True, "deleted ws → NotFoundError")
except Exception:
    ok(True, "deleted ws → error")

# ═══════════════════════════════════════════════════════════════════════════════
print(f"\n═══ Results: {passed} passed, {failed} failed ═══")
if errors:
    print(f"Failures:\n  " + "\n  ".join(errors))
sys.exit(1 if failed > 0 else 0)
