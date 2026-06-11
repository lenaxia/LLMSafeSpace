// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
// D-ACCOUNT-RECOVER canary — TypeScript SDK

import { LLMSafeSpace } from '../../src/index.js';
import { Runner, Config, configFromEnv, nodeFetch, rawDo } from '../canary.js';

async function run(r: Runner, cfg: Config): Promise<void> {
  const rotateEmail = process.env.LLMSAFESPACE_ROTATE_EMAIL || 'canary-rotate@llmsafespace.test';
  const rotatePassword = process.env.LLMSAFESPACE_ROTATE_PASSWORD || 'canary-rotate-password!';
  const recoveryNewPassword = 'canary-recover-newpw!';

  const c = new LLMSafeSpace({
    baseUrl: cfg.apiUrl,
    credentials: { email: rotateEmail, password: rotatePassword },
    timeout: 60000,
    fetch: nodeFetch as any,
  });

  const [okMe, me] = await r.assertNoError(() => c.auth.me(), 'auth-me: no error');
  if (!okMe || !me) return;

  const [ok, secret] = await r.assertNoError(
    () => c.secrets.create({ name: 'canary-recover-test', type: 'env-secret', value: 'recover-secret-value' }),
    'create-secret: no error');
  if (!ok || !secret) return;

  try {
    const [ok2, rotResult] = await r.assertNoError(
      () => c.account.rotateKey(rotatePassword),
      'rotate-key-for-recovery: no error');
    if (!ok2 || !rotResult) return;

    r.assert(typeof rotResult.recoveryKey === 'string' && rotResult.recoveryKey.length > 0,
      'rotate-key: recoveryKey present');

    const recoverPassword = 'canary-recovered-pw!';
    const [ok3, recoverResult] = await r.assertNoError(
      () => c.account.recover(me.id, rotResult.recoveryKey, recoverPassword),
      'recover: no error');
    if (ok3 && recoverResult) {
      r.assert(typeof recoverResult.recoveryKey === 'string' && recoverResult.recoveryKey.length > 0,
        'recover: returns new recoveryKey');
    }

    const loginBody = Buffer.from(JSON.stringify({ email: rotateEmail, password: recoverPassword }));
    const [s1, b1] = await rawDo('POST', `${cfg.apiUrl}/api/v1/auth/login`, '', loginBody);
    r.assert(s1 === 200, 'login-after-recover: 200', `got ${s1}`);

    const c2 = new LLMSafeSpace({
      baseUrl: cfg.apiUrl,
      credentials: { email: rotateEmail, password: recoverPassword },
      timeout: 60000,
      fetch: nodeFetch as any,
    });
    const [ok4, revealed] = await r.assertNoError(
      () => c2.secrets.reveal(secret.id, recoverPassword),
      'reveal-after-recover: no error');
    if (ok4 && revealed) {
      r.assert(revealed.value === 'recover-secret-value', 'reveal-after-recover: value correct',
        revealed.value);
    }

    await r.assertError(
      () => c.account.recover(me.id, 'invalid-recovery-key-00000', 'newpw1234'),
      'recover-invalid-key: error');

    const [s2] = await rawDo('POST', `${cfg.apiUrl}/api/v1/account/recover`, '',
      Buffer.from(JSON.stringify({})));
    r.assert(s2 === 400, 'recover-missing-fields: 400', `got ${s2}`);

    try {
      await c2.account.changePassword(recoverPassword, rotatePassword);
      r.ok('reset-password-back: success');
    } catch {
      r.ok('reset-password-back: attempted (may need manual reset)');
    }

  } finally {
    try {
      const cCleanup = new LLMSafeSpace({
        baseUrl: cfg.apiUrl,
        credentials: { email: rotateEmail, password: rotatePassword },
        timeout: 30000,
        fetch: nodeFetch as any,
      });
      await cCleanup.secrets.delete(secret.id);
    } catch {}
  }
}

async function main() {
  const r = new Runner('account-recover');
  await run(r, configFromEnv());
  r.print();
  process.exit(r.exitCode());
}
main().catch(e => { console.error(e); process.exit(1); });
