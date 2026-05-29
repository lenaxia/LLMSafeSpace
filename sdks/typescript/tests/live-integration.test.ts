import { LLMSafeSpace, NotFoundError, AuthError } from '../src/index.js';

const API_URL = process.env.API_URL || 'http://localhost:18080';
const API_KEY = process.env.API_KEY || 'lsp_upgradetest1234567890abcdef';

const client = new LLMSafeSpace({ baseUrl: API_URL, apiKey: API_KEY, timeout: 120_000 });

let passed = 0;
let failed = 0;
const errors: string[] = [];

function assert(cond: boolean, label: string) {
  if (cond) { console.log(`  ✓ ${label}`); passed++; }
  else { console.log(`  ✗ ${label}`); failed++; errors.push(label); }
}

async function waitHealthy(id: string, maxWait = 150): Promise<string> {
  const start = Date.now();
  while (Date.now() - start < maxWait * 1000) {
    const s = await client.workspaces.getStatus(id);
    if (s.agentHealth?.status === 'Healthy') return 'Healthy';
    if (s.phase === 'Failed') return 'Failed';
    await new Promise(r => setTimeout(r, 5000));
  }
  return (await client.workspaces.getStatus(id)).agentHealth?.status || 'timeout';
}

async function waitPhase(id: string, phase: string, maxWait = 60): Promise<string> {
  const start = Date.now();
  while (Date.now() - start < maxWait * 1000) {
    const s = await client.workspaces.getStatus(id);
    if (s.phase === phase) return phase;
    await new Promise(r => setTimeout(r, 3000));
  }
  return (await client.workspaces.getStatus(id)).phase;
}

async function main() {
  console.log('=== TypeScript SDK Live Integration Test (Comprehensive) ===\n');

  // ═══════════════════════════════════════════════════════════════════════════
  // 1. AUTH
  // ═══════════════════════════════════════════════════════════════════════════
  console.log('─── Auth ───');
  const me = await client.auth.me();
  assert(typeof me.id === 'string' && me.id.length > 0, 'auth.me() → user.id');
  assert(typeof me.email === 'string', 'auth.me() → user.email');
  assert(typeof me.role === 'string', 'auth.me() → user.role');
  assert(typeof me.username === 'string', 'auth.me() → user.username');
  assert(typeof me.active === 'boolean', 'auth.me() → user.active');

  const keys = await client.auth.listApiKeys();
  assert(Array.isArray(keys), 'auth.listApiKeys() → array');

  // Create + delete API key
  const newKey = await client.auth.createApiKey('live-test-key');
  assert(newKey.name === 'live-test-key', 'auth.createApiKey() → correct name');
  assert(newKey.key!.startsWith('lsp_'), 'auth.createApiKey() → key starts with lsp_');
  assert(typeof newKey.id === 'string', 'auth.createApiKey() → has id');

  await client.auth.deleteApiKey(newKey.id);
  const keysAfter = await client.auth.listApiKeys();
  assert(!keysAfter.find(k => k.id === newKey.id), 'auth.deleteApiKey() → key removed');

  // ═══════════════════════════════════════════════════════════════════════════
  // 2. WORKSPACE LIFECYCLE
  // ═══════════════════════════════════════════════════════════════════════════
  console.log('\n─── Workspace Lifecycle ───');
  const ws = await client.workspaces.create({ name: 'ts-live-comprehensive', runtime: 'base', storageSize: '1Gi' });
  const wsId = ws.id;
  assert(ws.id.length > 0, 'workspaces.create() → id');
  assert(ws.name === 'ts-live-comprehensive', 'workspaces.create() → name');
  assert(ws.runtime === 'base', 'workspaces.create() → runtime');

  const fetched = await client.workspaces.get(wsId);
  assert(fetched.id === wsId, 'workspaces.get() → correct id');

  const status = await client.workspaces.getStatus(wsId);
  assert(typeof status.phase === 'string', 'workspaces.getStatus() → phase');
  assert(typeof status.activeSessions === 'number', 'workspaces.getStatus() → activeSessions');

  const list = await client.workspaces.list();
  assert(list.items.length >= 1, 'workspaces.list() → ≥1 item');
  assert(list.items.some(i => i.id === wsId), 'workspaces.list() → contains new workspace');

  // Pagination
  const page = await client.workspaces.list(1, 0);
  assert(page.items.length <= 1, 'workspaces.list(limit=1) → ≤1 item');

  // Rename
  await client.workspaces.rename(wsId, 'ts-live-renamed');
  const renamed = await client.workspaces.get(wsId);
  assert(renamed.name === 'ts-live-renamed', 'workspaces.rename() → name updated');

  // Wait for healthy
  console.log('  Waiting for agent healthy...');
  const health = await waitHealthy(wsId);
  assert(health === 'Healthy', `agent healthy (got: ${health})`);
  if (health !== 'Healthy') { await client.workspaces.delete(wsId); process.exit(1); }

  // ═══════════════════════════════════════════════════════════════════════════
  // 3. SESSIONS
  // ═══════════════════════════════════════════════════════════════════════════
  console.log('\n─── Sessions ───');
  const session = await client.sessions.ensure(wsId);
  assert(session.sessionId.length > 0, 'sessions.ensure() → sessionId');
  assert(session.workspaceId === wsId, 'sessions.ensure() → workspaceId matches');
  assert(typeof session.resumed === 'boolean', 'sessions.ensure() → resumed field');

  const active = await client.sessions.getActive(wsId);
  assert(typeof active.maxActive === 'number' && active.maxActive > 0, 'sessions.getActive() → maxActive');
  assert(Array.isArray(active.active), 'sessions.getActive() → active array');

  // Rename session
  await client.sessions.rename(wsId, session.sessionId, 'test-session-title');
  // No error = success (void return)
  assert(true, 'sessions.rename() → no error');

  // Send message (using SDK method — now fixed with parts format)
  console.log('  Sending message via SDK...');
  const msgResp = await client.sessions.sendMessage(wsId, session.sessionId, 'Reply with exactly: PONG');
  assert(typeof msgResp.content === 'string', 'sessions.sendMessage() → content string');
  assert(msgResp.content.length > 0, 'sessions.sendMessage() → non-empty content');
  assert(msgResp.raw !== undefined, 'sessions.sendMessage() → raw present');
  console.log(`  Agent said: "${msgResp.content.substring(0, 80)}"`);

  // Get history
  const history = await client.sessions.getHistory(wsId, session.sessionId);
  assert(Array.isArray(history), 'sessions.getHistory() → array');
  assert(history.length >= 1, 'sessions.getHistory() → ≥1 entry after message');

  // Abort (should not error even if nothing running)
  await client.sessions.abort(wsId, session.sessionId);
  assert(true, 'sessions.abort() → no error');

  // ═══════════════════════════════════════════════════════════════════════════
  // 4. TERMINAL TICKET
  // ═══════════════════════════════════════════════════════════════════════════
  console.log('\n─── Terminal ───');
  const ticket = await client.terminal.getTicket(wsId);
  assert(ticket.ticket.startsWith('tkt_'), 'terminal.getTicket() → tkt_ prefix');
  assert(ticket.ticket.length > 10, 'terminal.getTicket() → sufficient length');
  assert(typeof ticket.expiresAt === 'string', 'terminal.getTicket() → expiresAt');

  const t2 = await client.terminal.getTicket(wsId);
  assert(ticket.ticket !== t2.ticket, 'terminal tickets are unique');

  // ═══════════════════════════════════════════════════════════════════════════
  // 5. SECRETS
  // ═══════════════════════════════════════════════════════════════════════════
  console.log('\n─── Secrets ───');
  try {
    const secret = await client.secrets.create({ name: 'live-test-secret', type: 'env-secret', value: 'test-value-123' });
    assert(secret.id.length > 0, 'secrets.create() → id');
    assert(secret.name === 'live-test-secret', 'secrets.create() → name');
    assert(secret.type === 'env-secret', 'secrets.create() → type');

    const secretList = await client.secrets.list();
    assert(secretList.some(s => s.id === secret.id), 'secrets.list() → contains new secret');

    const fetched2 = await client.secrets.get(secret.id);
    assert(fetched2.name === 'live-test-secret', 'secrets.get() → correct name');

    const revealed = await client.secrets.reveal(secret.id);
    assert(revealed.value === 'test-value-123', 'secrets.reveal() → correct value');

    await client.secrets.delete(secret.id);
    const afterDelete = await client.secrets.list();
    assert(!afterDelete.find(s => s.id === secret.id), 'secrets.delete() → removed');
  } catch (e: any) {
    // Secrets may not be available in all deployments
    console.log(`  ⚠ Secrets tests skipped: ${e.message}`);
  }

  // ═══════════════════════════════════════════════════════════════════════════
  // 6. SUSPEND / RESUME
  // ═══════════════════════════════════════════════════════════════════════════
  console.log('\n─── Suspend / Resume ───');
  await client.workspaces.suspend(wsId);
  assert(true, 'workspaces.suspend() → no error (202)');

  const suspPhase = await waitPhase(wsId, 'Suspended');
  assert(suspPhase === 'Suspended', `phase → Suspended (got: ${suspPhase})`);

  await client.workspaces.resume(wsId);
  assert(true, 'workspaces.resume() → no error (202)');

  const resumeHealth = await waitHealthy(wsId);
  assert(resumeHealth === 'Healthy', `resume → Healthy (got: ${resumeHealth})`);

  // Verify session works after resume
  const postResume = await client.sessions.ensure(wsId);
  assert(postResume.sessionId.length > 0, 'sessions.ensure() works after resume');

  // ═══════════════════════════════════════════════════════════════════════════
  // 7. ACTIVATE (suspend + activate in one call)
  // ═══════════════════════════════════════════════════════════════════════════
  console.log('\n─── Activate ───');
  await client.workspaces.suspend(wsId);
  await waitPhase(wsId, 'Suspended');
  const activateResp = await client.workspaces.activate(wsId);
  assert(typeof activateResp.resumed === 'string', 'workspaces.activate() → resumed field');
  const activateHealth = await waitHealthy(wsId);
  assert(activateHealth === 'Healthy', `activate → Healthy (got: ${activateHealth})`);

  // ═══════════════════════════════════════════════════════════════════════════
  // 8. ERROR HANDLING
  // ═══════════════════════════════════════════════════════════════════════════
  console.log('\n─── Error Handling ───');
  try {
    await client.workspaces.get('00000000-0000-0000-0000-000000000000');
    assert(false, 'nonexistent workspace should throw');
  } catch (e) {
    assert(e instanceof NotFoundError, 'nonexistent workspace → NotFoundError');
  }

  try {
    const bad = new LLMSafeSpace({ baseUrl: API_URL, apiKey: 'lsp_invalid' });
    await bad.auth.me();
    assert(false, 'invalid key should throw');
  } catch (e) {
    assert(e instanceof AuthError, 'invalid key → AuthError');
  }

  try {
    await client.terminal.getTicket('00000000-0000-0000-0000-000000000000');
    assert(false, 'terminal ticket for nonexistent ws should throw');
  } catch (e) {
    assert(e instanceof NotFoundError || e instanceof Error, 'terminal ticket nonexistent → error');
  }

  // ═══════════════════════════════════════════════════════════════════════════
  // 9. CLEANUP
  // ═══════════════════════════════════════════════════════════════════════════
  console.log('\n─── Cleanup ───');
  await client.workspaces.delete(wsId);
  assert(true, 'workspaces.delete() → success');

  // Verify deleted
  try {
    await client.workspaces.get(wsId);
    assert(false, 'get deleted workspace should throw');
  } catch (e) {
    assert(e instanceof NotFoundError, 'deleted workspace → NotFoundError');
  }

  // ═══════════════════════════════════════════════════════════════════════════
  console.log(`\n═══ Results: ${passed} passed, ${failed} failed ═══`);
  if (errors.length) console.log(`Failures:\n  ${errors.join('\n  ')}`);
  process.exit(failed > 0 ? 1 : 0);
}

main().catch(e => { console.error('Fatal:', e); process.exit(1); });
