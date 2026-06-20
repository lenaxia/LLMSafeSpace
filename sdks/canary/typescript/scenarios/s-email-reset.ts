// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later
// S-EMAIL-RESET canary — tests email endpoints through the real HTTP boundary

import { Runner, Config, configFromEnv, rawDo, containsLeakedInternals } from '../canary.js';

async function run(run: Runner, cfg: Config): Promise<void> {
  const base = cfg.apiUrl.replace(/\/$/, '') + '/api/v1';
  const unique = Date.now();
  const email = `canary-email-${unique}@llmsafespaces.test`;
  const password = 'canary-email-pwd-123456';

  // P1: Register
  const [regStatus, regResp] = await rawDo(base + '/auth/register', 'POST', { username: 'canaryemail', email, password });
  run.assert(regStatus === 201 || regStatus === 409, `register: 201 or 409 (got ${regStatus})`, String(regResp).slice(0, 200));

  // P2: Login
  const [loginStatus] = await rawDo(base + '/auth/login', 'POST', { email, password });
  if (loginStatus === 200) {
    run.ok('login: 200 (noop mode — auto-verified)');
  } else if (loginStatus === 403) {
    run.ok('login: 403 (SES mode — unverified)');
  } else {
    run.fail('login: unexpected status', `got ${loginStatus}`);
  }

  // P3: Password-reset request → 202
  const [resetStatus] = await rawDo(base + '/auth/password-reset/request', 'POST', { email });
  run.assert(resetStatus === 202, `password-reset-request: 202 (got ${resetStatus})`, '');

  // P4: Password-reset request unknown → 202
  const [unknownStatus] = await rawDo(base + '/auth/password-reset/request', 'POST', { email: 'nonexistent-canary@llmsafespaces.test' });
  run.assert(unknownStatus === 202, `password-reset-request-unknown: 202 (got ${unknownStatus})`, '');

  // P5: Password-reset confirm bogus → 404
  const [confirmStatus] = await rawDo(base + '/auth/password-reset/confirm', 'POST', { token: 'canary-bogus', newPassword: 'canary-new-pwd-123456' });
  run.assert(confirmStatus === 404, `password-reset-confirm-bogus: 404 (got ${confirmStatus})`, '');

  // P6: Verify-email bogus → 404
  const [verifyStatus] = await rawDo(base + '/auth/verify-email', 'POST', { token: 'canary-bogus' });
  run.assert(verifyStatus === 404, `verify-email-bogus: 404 (got ${verifyStatus})`, '');

  // P7: Verify-email resend → 202
  const [resendStatus] = await rawDo(base + '/auth/verify-email/resend', 'POST', { email });
  run.assert(resendStatus === 202, `verify-email-resend: 202 (got ${resendStatus})`, '');

  // P8: Resend unknown → 202
  const [resendUnknownStatus] = await rawDo(base + '/auth/verify-email/resend', 'POST', { email: 'ghost-canary@nonexistent.invalid' });
  run.assert(resendUnknownStatus === 202, `verify-email-resend-unknown: 202 (got ${resendUnknownStatus})`, '');

  // P9: No leaked internals in error responses
  const [, leakResp] = await rawDo(base + '/auth/password-reset/confirm', 'POST', { token: 'x', newPassword: 'canary-valid-pwd' });
  run.assert(!containsLeakedInternals(String(leakResp)), 'password-reset-confirm: no leaked internals', '');
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const run = new Runner('email-reset', 'typescript-sdk');
  const cfg = configFromEnv();
  await run(run, cfg);
  const res = run.printResult();
  if (res.failed > 0) process.exit(1);
}
