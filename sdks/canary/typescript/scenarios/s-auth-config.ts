// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
// S-AUTH-CONFIG canary — TypeScript SDK

import { Runner, Config, configFromEnv, rawDo, hasField } from '../canary.js';

async function run(r: Runner, cfg: Config): Promise<void> {
  const [s, b] = await rawDo('GET', cfg.apiUrl + '/api/v1/auth/config');
  r.assert(s === 200, `auth/config: 200 (got ${s})`);
  try {
    const obj = JSON.parse(b.toString());
    r.assert(typeof obj.registrationEnabled === 'boolean', 'registrationEnabled: bool');
    r.assert(typeof obj.oidcEnabled === 'boolean', 'oidcEnabled: bool');
    r.assert(typeof obj.instanceName === 'string' && obj.instanceName !== '', 'instanceName: non-empty string', obj.instanceName);
    r.assert('motd' in obj, 'motd: field present');
    r.assert(!('error' in obj), 'no error field on success');
  } catch { r.fail('auth/config: valid JSON'); }
}

async function main() {
  const r = new Runner('auth-config');
  await run(r, configFromEnv());
  r.print();
  process.exit(r.exitCode());
}
main().catch(e => { console.error(e); process.exit(1); });
