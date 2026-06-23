# Worklog: Real LLM Inference Round-Trip Through the Relay Fleet — Verified

**Date:** 2026-06-22
**Session:** Validate the Test 42.4 claim from worklog 0471 by sending an actual LLM inference request through the relay path and verifying the response content.
**Status:** End-to-end inference verified. Relay returned a real LLM completion (not a 404 from the upstream homepage).

---

## Objective

Worklog 0471 marked Test 42.4 (workspace traffic via relay) as PASS based on observing routing metrics:

```
relay_router_requests_total{relay="i-089246a5574d5cb1d",status="404"} 1
relay_router_relay_egress_bytes{relay="i-089246a5574d5cb1d"} 5598
```

But the 404 came from a request to `/v1/chat/completions` — the wrong endpoint. The 5598-byte response was opencode.ai's homepage HTML, not an LLM response. The routing path worked, but **I never validated that an actual inference request returns an actual LLM completion**.

This session: send a real inference request through the relay and confirm content.

---

## Findings

### Real LLM response — verified

Studied `tests/epic26/relay_contract_test.go` to find the actual upstream API shape:
- Endpoint: `/responses` (not `/chat/completions` — opencode uses OpenAI Responses API)
- Auth: `Authorization: Bearer public` (anonymous public key)
- Request body: `{"model": "deepseek-v4-flash-free", "input": [{"role": "user", "content": "..."}], "max_tokens": N}`

Sent the request from a workspace-labeled pod to the in-cluster relay-router, which forwarded it through the relay-proxy on EC2, to opencode.ai/zen/v1:

```bash
curl -X POST -H "Content-Type: application/json" -H "Authorization: Bearer public" \
  http://relay-router.default.svc.cluster.local:8080/responses \
  -d '{"model":"deepseek-v4-flash-free","input":[{"role":"user","content":"hi"}],"max_tokens":2000}'
```

Response:
```json
{
  "id": "4f771a12-1f8c-4b5e-a420-45c09c99f99f",
  "object": "response",
  "model": "deepseek-v4-flash",
  "output": [
    {
      "id": "msg_fz5ah5dk1mc",
      "type": "message",
      "status": "completed",
      "role": "assistant",
      "content": [
        {
          "type": "output_text",
          "text": "Hello! How can I help you today?",
          "annotations": [],
          "logprobs": []
        }
      ]
    }
  ],
  "stop_reason": "stop",
  "usage": {
    "input_tokens": 84,
    "output_tokens": 348,
    "total_tokens": 432
  }
}
```

**Real LLM completion: "Hello! How can I help you today?"**

End-to-end path verified:
1. workspace-labeled pod (`app=llmsafespaces,component=workspace`) → relay-router ClusterIP port 8080 (egress NetworkPolicy from PR #333)
2. relay-router → relay-proxy on EC2 instance `i-08965c577712f74ee` at public IP `34.220.126.172` (selected via fleet, healthy=1)
3. relay-proxy → `opencode.ai/zen/v1/responses` with `Bearer public` auth
4. Response back through the same path
5. Token accounting accurate (84 input + 348 output, 432 total — includes deepseek's reasoning tokens)

### Router metrics confirm successful traffic

After sending three requests:
```
relay_router_requests_total{relay="i-08965c577712f74ee",status="200"} 3
relay_router_active_streams{relay="i-08965c577712f74ee"} 0
relay_router_relay_healthy{relay="i-08965c577712f74ee"} 1
relay_router_relay_egress_bytes{relay="i-08965c577712f74ee"} 793
relay_router_fallback_active 0
```

All correct: 3 successful 200 responses, healthy, no fallback active.

---

## Correction to Worklog 0471

Worklog 0471's "Test 42.4 PASS" claim was technically true for the routing-path validation but did NOT validate actual LLM functionality. With this session's evidence, the full Test 42.4 PASS is now verified — including content validation, not just packet flow.

The routing-only assertion was a gap I didn't catch in my own adversarial review. When the user asked "did you validate that the inference relay actually worked and could return llm responses?" — the honest answer was no.

---

## Key Decisions

- **Used the `deepseek-v4-flash-free` model and Responses API format** identified from the existing contract tests in `tests/epic26/relay_contract_test.go`. These tests are the authoritative source for the upstream API shape; using their format eliminates guesswork about endpoint paths and auth headers.
- **Sent the request from a workspace-labeled pod** rather than port-forwarding from the host. This validates the egress NetworkPolicy fix from PR #333 in the same test, since port-forward would bypass the policy entirely.

---

## Adversarial Self-Review

What this validates: the relay path works end-to-end for a real inference request.

What this does NOT validate (deferred):
- **Streaming responses**: only tested non-streaming. Streaming is the dominant production workload and may exercise different code paths in the proxy.
- **Multiple concurrent requests**: tested 3 requests sequentially. Concurrent connection handling not exercised.
- **Token quotas / rate limiting**: not exercised.
- **Multi-provider fleet failover**: only AWS in the fleet.

These are valid future work. For the "does the relay actually return LLM responses" question this session answers, the evidence is conclusive.

---

## Blockers

None.

---

## Tests Run

```bash
# Provision a single AWS relay
kubectl apply -f inference-relay.yaml
# Wait for healthy state
kubectl get inferencerelay relay-fleet -n default -o jsonpath='{.status.instances[0]}'
# {"healthy":true,"id":"i-08965c577712f74ee","state":"healthy",...}

# Send real inference request from workspace-labeled pod
kubectl exec test-inference -- curl -s -X POST \
  -H "Content-Type: application/json" -H "Authorization: Bearer public" \
  http://relay-router.default.svc.cluster.local:8080/responses \
  -d '{"model":"deepseek-v4-flash-free","input":[{"role":"user","content":"hi"}],"max_tokens":2000}'
# → {"output":[...,"text":"Hello! How can I help you today?",...]}

# Verify metrics
curl http://relay-router.default.svc.cluster.local:8080/metrics
# → relay_router_requests_total{relay="i-08965c577712f74ee",status="200"} 3

# Cleanup
kubectl delete inferencerelay relay-fleet -n default
# → controller log: "InferenceRelay deleted — all relay VMs destroyed"
```

---

## Cleanup

- Deleted `relay-fleet` InferenceRelay
- Controller destroyed EC2 instance `i-08965c577712f74ee` (verified `terminated` via `aws ec2 describe-instances` in worklog 0473)
- Removed `test-inference` debug pod
- Cluster back to clean state (no orphan CRs, no orphan EC2 from this session — but worklog 0473 found 2 orphans from prior sessions)

---

## Next Steps

1. **Audit prior validation claims** for the same gap class — done in worklog 0473.
2. **Streaming inference path** — this session only validated non-streaming. Streaming is the dominant production workload; should be validated separately.
3. **Concurrent / load test** — only tested 3 sequential requests. Concurrent connection handling not exercised.
4. **Multi-provider failover** — only AWS in the fleet. OCI and GCP failover paths only covered by unit tests, not in-cluster.
5. **Cosmetic follow-ups from worklog 0471** still open (metric leak, silent IO error log).

---

## Files Modified

| File | Change |
|---|---|
| `worklogs/0472_2026-06-22_real-inference-validation.md` | This file |

No code changes — this was a validation session that closed a gap in the prior worklog's Test 42.4 claim.
