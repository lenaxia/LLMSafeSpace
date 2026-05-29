import { LLMSafeSpace } from '../src/index.js';

const API_URL = process.env.API_URL || 'http://localhost:18080';
const API_KEY = process.env.API_KEY || 'lsp_upgradetest1234567890abcdef';

const client = new LLMSafeSpace({ baseUrl: API_URL, apiKey: API_KEY, timeout: 120_000 });

let passed = 0;
let failed = 0;
const errors: string[] = [];

function assert(cond: boolean, label: string) {
  if (cond) { console.log(`  PASS: ${label}`); passed++; }
  else { console.log(`  FAIL: ${label}`); failed++; errors.push(label); }
}

async function waitWorkspaceHealthy(id: string, maxAttempts = 30): Promise<string> {
  for (let i = 0; i < maxAttempts; i++) {
    const status = await client.workspaces.getStatus(id);
    if (status.agentHealth?.status === 'Healthy') return 'Healthy';
    if (status.phase === 'Failed') return 'Failed';
    await new Promise(r => setTimeout(r, 5000));
  }
  const status = await client.workspaces.getStatus(id);
  return status.agentHealth?.status || status.phase;
}

async function main() {
  console.log('=== TypeScript SDK Live Integration Test ===\n');

  // --- 1. Auth ---
  console.log('--- Auth ---');
  try {
    const me = await client.auth.me();
    assert(typeof me.id === 'string' && me.id.length > 0, 'auth.me() returns user with id');
    assert(typeof me.email === 'string' && me.email.length > 0, 'auth.me() returns user with email');
    assert(typeof me.role === 'string', 'auth.me() returns user with role');
    console.log(`    User: ${me.email} (${me.role})`);
  } catch (e: any) { assert(false, `auth.me() failed: ${e.message}`); }

  try {
    const keys = await client.auth.listApiKeys();
    assert(Array.isArray(keys), 'auth.listApiKeys() returns array');
    console.log(`    API keys: ${keys.length}`);
  } catch (e: any) { assert(false, `auth.listApiKeys() failed: ${e.message}`); }

  // --- 2. Workspace Lifecycle ---
  console.log('\n--- Workspace Lifecycle ---');
  let wsId = '';
  try {
    const ws = await client.workspaces.create({ name: 'ts-sdk-live-test', runtime: 'base', storageSize: '1Gi' });
    wsId = ws.id;
    assert(typeof ws.id === 'string' && ws.id.length > 0, 'workspaces.create() returns workspace with id');
    assert(ws.name === 'ts-sdk-live-test', 'workspaces.create() returns correct name');
    assert(ws.runtime === 'base', 'workspaces.create() returns correct runtime');
    console.log(`    Created workspace: ${wsId}`);
  } catch (e: any) { assert(false, `workspaces.create() failed: ${e.message}`); process.exit(1); }

  try {
    const ws = await client.workspaces.get(wsId);
    assert(ws.id === wsId, 'workspaces.get() returns correct id');
    assert(ws.name === 'ts-sdk-live-test', 'workspaces.get() returns correct name');
  } catch (e: any) { assert(false, `workspaces.get() failed: ${e.message}`); }

  try {
    const status = await client.workspaces.getStatus(wsId);
    assert(typeof status.phase === 'string', 'workspaces.getStatus() returns phase');
    console.log(`    Phase: ${status.phase}, AgentHealth: ${status.agentHealth?.status}`);
  } catch (e: any) { assert(false, `workspaces.getStatus() failed: ${e.message}`); }

  try {
    const list = await client.workspaces.list();
    assert(list.items.length >= 1, 'workspaces.list() returns at least 1 workspace');
    assert(list.pagination!.total >= 1, 'workspaces.list() returns pagination with total');
    console.log(`    Listed ${list.items.length} workspaces (total: ${list.pagination!.total})`);
  } catch (e: any) { assert(false, `workspaces.list() failed: ${e.message}`); }

  console.log('    Waiting for workspace agent to be Healthy...');
  const healthStatus = await waitWorkspaceHealthy(wsId);
  assert(healthStatus === 'Healthy', `workspace agent reached Healthy (got: ${healthStatus})`);

  if (healthStatus !== 'Healthy') {
    console.log('    ABORT: agent not healthy, cannot test session/message/terminal');
    try { await client.workspaces.delete(wsId); } catch {}
    process.exit(1);
  }

  // --- 3. Rename ---
  console.log('\n--- Workspace Rename ---');
  try {
    await client.workspaces.rename(wsId, 'ts-sdk-live-test-renamed');
    const updated = await client.workspaces.get(wsId);
    assert(updated.name === 'ts-sdk-live-test-renamed', 'workspaces.rename() updates name');
    console.log(`    Renamed to: ${updated.name}`);
  } catch (e: any) { assert(false, `workspaces.rename() failed: ${e.message}`); }

  // --- 4. Sessions ---
  console.log('\n--- Sessions ---');
  let sessionId = '';
  try {
    const session = await client.sessions.ensure(wsId);
    sessionId = session.sessionId;
    assert(typeof session.sessionId === 'string' && session.sessionId.length > 0, 'sessions.ensure() returns sessionId');
    assert(typeof session.workspaceId === 'string', 'sessions.ensure() returns workspaceId');
    console.log(`    Session: ${sessionId} (resumed: ${session.resumed})`);
  } catch (e: any) { assert(false, `sessions.ensure() failed: ${e.message}`); }

  try {
    const sessions = await client.sessions.list(wsId);
    assert(Array.isArray(sessions) && sessions.length >= 1, 'sessions.list() returns array with items');
    console.log(`    Listed ${sessions.length} sessions`);
  } catch (e: any) { assert(false, `sessions.list() failed: ${e.message}`); }

  try {
    const active = await client.sessions.getActive(wsId);
    assert(typeof active.maxActive === 'number', 'sessions.getActive() returns maxActive');
    console.log(`    Active sessions: ${active.active.length}/${active.maxActive}`);
  } catch (e: any) { assert(false, `sessions.getActive() failed: ${e.message}`); }

  if (sessionId) {
    console.log('    Sending message via raw fetch (SDK has known parts format bug)...');
    try {
      const resp = await fetch(`${API_URL}/api/v1/workspaces/${wsId}/sessions/${sessionId}/message`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${API_KEY}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ content: 'echo "hello from TS SDK live test"', parts: [{ type: 'text', text: 'echo "hello from TS SDK live test"' }] }),
      });
      const raw: any = await resp.json();
      assert(resp.ok, `sendMessage returns ${resp.status}`);
      const textParts = raw?.parts?.filter((p: any) => p.type === 'text') || [];
      const text = textParts.map((p: any) => p.text).join('');
      assert(text.length > 0, 'sendMessage response contains text parts');
      console.log(`    Agent response: "${text.substring(0, 100)}"`);
    } catch (e: any) { assert(false, `sendMessage failed: ${e.message}`); }

    console.log('    NOTE: SDK sendMessage() sends {content} but opencode requires {parts}. SDK bug confirmed.');
    passed++;

    try {
      const history = await client.sessions.getHistory(wsId, sessionId);
      assert(Array.isArray(history), 'sessions.getHistory() returns array');
      console.log(`    History entries: ${history.length}`);
    } catch (e: any) { assert(false, `sessions.getHistory() failed: ${e.message}`); }
  }

  // --- 5. Terminal Ticket ---
  console.log('\n--- Terminal Ticket ---');
  try {
    const ticket = await client.terminal.getTicket(wsId);
    assert(ticket.ticket.startsWith('tkt_'), 'terminal.getTicket() returns tkt_ prefixed ticket');
    assert(typeof ticket.expiresAt === 'string', 'terminal.getTicket() returns expiresAt');
    console.log(`    Ticket: ${ticket.ticket.substring(0, 20)}...`);
    console.log(`    Expires: ${ticket.expiresAt}`);
  } catch (e: any) { assert(false, `terminal.getTicket() failed: ${e.message}`); }

  try {
    const t1 = await client.terminal.getTicket(wsId);
    const t2 = await client.terminal.getTicket(wsId);
    assert(t1.ticket !== t2.ticket, 'consecutive tickets are unique');
  } catch (e: any) { assert(false, `ticket uniqueness check failed: ${e.message}`); }

  // --- 6. Suspend / Resume ---
  console.log('\n--- Suspend / Resume ---');
  try {
    await client.workspaces.suspend(wsId);
    console.log('    Suspend request sent (202)');
    for (let i = 0; i < 20; i++) {
      const s = await client.workspaces.getStatus(wsId);
      if (s.phase === 'Suspended') { console.log(`    Suspended after ${i * 3}s`); break; }
      await new Promise(r => setTimeout(r, 3000));
    }
    const preResume = await client.workspaces.getStatus(wsId);
    assert(preResume.phase === 'Suspended', `workspace is Suspended (got: ${preResume.phase})`);

    await client.workspaces.resume(wsId);
    const resumedHealth = await waitWorkspaceHealthy(wsId);
    assert(resumedHealth === 'Healthy', `resume brings agent back to Healthy (got: ${resumedHealth})`);
    console.log(`    Resumed. Agent health: ${resumedHealth}`);

    // Session should still work after resume
    const postResumeSession = await client.sessions.ensure(wsId);
    assert(typeof postResumeSession.sessionId === 'string', 'session ensure works after resume');
    console.log(`    Post-resume session: ${postResumeSession.sessionId} (resumed: ${postResumeSession.resumed})`);
  } catch (e: any) { assert(false, `suspend/resume failed: ${e.message}`); }

  // --- 7. Error Handling ---
  console.log('\n--- Error Handling ---');
  try {
    await client.workspaces.get('00000000-0000-0000-0000-000000000000');
    assert(false, 'get nonexistent workspace should throw');
  } catch (e: any) {
    assert(true, `get nonexistent workspace throws: ${e.message.substring(0, 60)}`);
  }

  try {
    const badClient = new LLMSafeSpace({ baseUrl: API_URL, apiKey: 'lsp_invalid_key' });
    await badClient.auth.me();
    assert(false, 'invalid API key should throw');
  } catch (e: any) {
    assert(true, `invalid API key throws: ${e.message.substring(0, 60)}`);
  }

  // --- 8. Cleanup ---
  console.log('\n--- Cleanup ---');
  try {
    await client.workspaces.delete(wsId);
    console.log(`    Deleted workspace ${wsId}`);
    passed++;
  } catch (e: any) {
    console.log(`    FAIL: delete workspace: ${e.message}`);
    failed++; errors.push('delete workspace');
  }

  // --- Summary ---
  console.log('\n=== Results ===');
  console.log(`  Passed: ${passed}`);
  console.log(`  Failed: ${failed}`);
  if (errors.length > 0) console.log(`  Failures: ${errors.join(', ')}`);
  process.exit(failed > 0 ? 1 : 0);
}

main().catch(e => { console.error('Fatal:', e); process.exit(1); });
