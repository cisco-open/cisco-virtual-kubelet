# Test 10 — Expected outcome

## Phase + family status

```yaml
status:
  phase: InSync
  observedGeneration: 1
  familyStatus:
    - name: interface_loopback
      state: InSync
      opCount: 1
  conditions:
    - type: Ready
      status: "True"
      reason: Succeeded
```

## Kubernetes events on the CR

A Normal event with reason `ConfirmedCommitUsed` and message:

```
confirmed-commit auto-revert path used; running-verify passed and follow-up confirm fired
```

There must NOT be a Warning event with reason `ConfirmedCommitFallback` — that would mean the engine fell back to plain Commit instead of the auto-revert path.

```sh
kubectl events -n cisco-vk-smoke --for IOSXEConfig/test-10-confirmed-commit-happy --sort-by=.lastTimestamp
```

## Metrics

```
cisco_vk_config_transactions_total{device="cat9k-smoke",transport="netconf",outcome="confirmed"} N+1
cisco_vk_config_transactions_total{device="cat9k-smoke",transport="netconf",outcome="commit"}    unchanged
cisco_vk_config_transactions_total{device="cat9k-smoke",transport="netconf",outcome="auto_reverted"} 0 (or unchanged)
```

`outcome="confirmed"` is the headline assertion. If `outcome="commit"` incremented instead, it means the engine took the plain-Commit fallback even though all four conditions for confirmed-commit were satisfied — that's a behavioural regression in `confirmedCommitDecision`.

## Device-side state

`Loopback9995` exists with description `cisco-vk release-blocker test 10 — confirmed-commit happy path` and IPv4 `10.255.255.95/32`.

```sh
curl --silent --insecure --user "${USER}:${PASS}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9995"
```

Returns 200 with the expected description and IP.

## NETCONF wire-level (operator-attestable)

The cisco-vk pod's debug log should show this RPC sequence within one transaction:

1. `<lock><target><candidate/></target></lock>`
2. `<edit-config><target><candidate/></target>...</edit-config>` (Loopback 9995 add op)
3. **Verify-Fetch on candidate** (Wave 1A-fu's TxFetcher path)
4. `<commit><confirmed/><confirm-timeout>30</confirm-timeout></commit>` (Wave 10 — tentative)
5. **Verify-Fetch on running** (Wave 10.2's `runningVerify` — uses raw transport, not transactionalView)
6. `<commit/>` (the follow-up that cancels the auto-revert timer)
7. `<unlock>`

Pre-Wave-10 the sequence stopped at step 4 with a plain `<commit/>`. Steps 5–7 are the new evidence.

## What would fail

| Failure | Likely cause |
|---|---|
| `ConfirmedCommitFallback` Warning event present | Engine took plain-Commit path. Check `Result.ConfirmedCommitFallback` reason — probably "transport does not implement ConfirmedCommitter" (transport regression) or "device did not advertise confirmed-commit:1.0" (device's NETCONF hello changed). |
| `outcome="commit"` incremented but `outcome="confirmed"` did not | Same as above — fallback was taken. |
| `outcome="auto_reverted"` incremented | running-verify failed unexpectedly. The Loopback add should be benign; investigate the engine's runningVerify against the writer's Diff for `interface_loopback`. |
