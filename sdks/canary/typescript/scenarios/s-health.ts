// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
// S-HEALTH canary — TypeScript SDK

import { Runner, Config, configFromEnv, rawDo, hasField } from '../canary.js';

async function run(r: Runner, cfg: Config): Promise<void> {
  for (const [name, path] of [['livez', '/livez'], ['health-alias', '/health'], ['readyz', '/readyz']]) {
    const [s, b] = await rawDo('GET', cfg.apiUrl + path);
    if (r.assert(s === 200, `${name}: 200`, `got ${s}`)) {
      r.assert(hasField(b, 'status'), `${name}: has status field`);
    }
  }
  // Not rate-limited under 10 rapid requests
  let rateLimited = false;
  for (let i = 0; i < 10; i++) {
    const [s] = await rawDo('GET', cfg.apiUrl + '/livez');
    if (s === 429) { rateLimited = true; break; }
  }
  r.assert(!rateLimited, 'livez: not rate-limited under 10 requests');
}

async function main() {
  const r = new Runner('health');
  await run(r, configFromEnv());
  r.print();
  process.exit(r.exitCode());
}
main().catch(e => { console.error(e); process.exit(1); });
