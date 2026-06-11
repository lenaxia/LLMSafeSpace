#!/usr/bin/env python3
# Copyright (C) 2026 Michael Kao
# SPDX-License-Identifier: AGPL-3.0-or-later
"""D-SESSION-LIMIT canary — Python SDK"""

from __future__ import annotations

import sys
import os
import time
import threading

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../.."))

from canary import (
    Runner,
    Config,
    config_from_env,
    wait_active,
    ensure_session_with_retry,
    raw_do,
)
from llmsafespace import LLMSafeSpace, RateLimitError


def run(r: Runner, cfg: Config) -> None:
    c = LLMSafeSpace(cfg.api_url, api_key=cfg.api_key, timeout=60.0)
    ws_id = None
    try:
        ok, ws = r.assert_no_error(
            lambda: c.workspaces.create(
                name="canary-py-sess-limit", runtime="base", storage_size="1Gi"
            ),
            "create: no error",
        )
        if not ok:
            return
        ws_id = ws.id

        phase = wait_active(c, ws_id)
        r.assert_(phase == "Active", "reach-active", f"got {phase!r}")
        if phase != "Active":
            return

        try:
            sess = ensure_session_with_retry(c, ws_id, 5)
        except Exception as e:
            r.fail("ensure-session: no error", str(e))
            return
        r.ok("ensure-session: no error")
        sid = sess.sessionId

        results = [None] * 8
        barrier = threading.Barrier(8, timeout=30)
        def send_concurrent(idx):
            try:
                barrier.wait()
                c.sessions.send_message(ws_id, sid, f"Concurrent message {idx}")
                results[idx] = "ok"
            except RateLimitError:
                results[idx] = "429"
            except Exception as e:
                results[idx] = f"err:{e}"

        threads = []
        for i in range(8):
            t = threading.Thread(target=send_concurrent, args=(i,))
            t.start()
            threads.append(t)
        for t in threads:
            t.join(timeout=60)

        hit_429 = any(r == "429" for r in results)
        r.assert_(hit_429, "session-limit: 429 on concurrent messages", f"results={results}")

        streams = []
        for i in range(11):
            import httpx

            try:
                resp = httpx.stream(
                    "GET",
                    f"{cfg.api_url}/api/v1/workspaces/{ws_id}/events",
                    headers={
                        "Authorization": f"Bearer {cfg.api_key}",
                        "Accept": "text/event-stream",
                    },
                    timeout=10,
                )
                resp.__enter__()
                streams.append(resp)
            except Exception:
                pass

        sse_429 = False
        try:
            import httpx as _httpx

            check = _httpx.get(
                f"{cfg.api_url}/api/v1/workspaces/{ws_id}/events",
                headers={
                    "Authorization": f"Bearer {cfg.api_key}",
                    "Accept": "text/event-stream",
                },
                timeout=3.0,
            )
            if check.status_code == 429:
                sse_429 = True
            check.close()
        except Exception:
            pass

        if len(streams) >= 11:
            r.assert_(sse_429, "sse-limit: 11th stream gets 429", "no 429 observed")

        for s in streams:
            try:
                s.__exit__(None, None, None)
            except Exception:
                pass

    finally:
        if ws_id:
            try:
                c.workspaces.delete(ws_id)
            except Exception:
                pass


if __name__ == "__main__":
    r = Runner("session-limit")
    cfg = config_from_env()
    run(r, cfg)
    r.print()
    sys.exit(r.exit_code())
