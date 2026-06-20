// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// ---------------------------------------------------------------------------
// [ws-timing] startup latency console logger
//
// All timing lines are prefixed with "[ws-timing]" so they can be isolated
// in the browser DevTools console using the filter box.
//
// Format:
//   [ws-timing] <event> | ws=<id_short> | t=<perf_ms>ms | wall=<iso> | <extra>
//
// t=  is performance.now() relative to page load — sub-millisecond precision,
//     monotonic, not affected by system clock changes.
// wall= is Date.toISOString() for correlation with backend / benchmark logs.
//
// Safe in test environments: performance.now() falls back to Date.now() when
// the Performance API is not available (e.g. jsdom without fake timers).
// ---------------------------------------------------------------------------

function perfNow(): number {
  if (typeof performance !== "undefined" && typeof performance.now === "function") {
    return performance.now();
  }
  return Date.now();
}

export function wsLog(
  event: string,
  workspaceId: string | undefined,
  extra?: string,
): void {
  const short = workspaceId ? workspaceId.slice(0, 8) : "?";
  const ts = `t=${perfNow().toFixed(1)}ms | wall=${new Date().toISOString()}`;
  console.log(
    `[ws-timing] ${event} | ws=${short} | ${ts}${extra ? ` | ${extra}` : ""}`,
  );
}
