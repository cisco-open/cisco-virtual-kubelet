# Test 08 — Expected outcome

## Sequence (with rough timings)

- **t=0**: `kubectl apply -f 00-apply.yaml`. CR is created; controller picks it up.
- **t≈1–3s**: engine's transactional path runs. `<lock>` + `<edit-config>` + `<commit><confirmed/><confirm-timeout>30</confirm-timeout></commit>`. Tentative commit applies the broken ACL to running. **The controller's session drops at this point.**
- **t≈3–10s**: engine's `runningVerify` attempts `Fetch` against running, fails (session is dead). Engine declines `ConfirmCommit`. The deferred `Discard` runs and (if the session can re-establish during that window) releases the candidate lock.
- **t≈30s**: device's own confirm-timeout timer fires. Running reverts to pre-commit state. The broken ACL is gone.
- **t≈30–60s**: controller re-establishes RESTCONF/NETCONF reachability. The next reconcile sees the CR's status and writes Phase=Failed with the auto-revert error message.

## Phase + condition

```yaml
status:
  phase: Failed
  observedGeneration: 1
  familyStatus:
    - name: access_list_extended
      state: ApplyError       # or InSync transiently before the auto-revert tick
      message: <varies>
    - name: interface_ethernet
      state: ApplyError
  conditions:
    - type: Ready
      status: "False"
      reason: ReconcileFailed   # engine maps the auto-revert Err
      message: "running-verify failed after CommitConfirmed; device will auto-revert at confirm-timeout"
```

The exact `Reason` and `message` depend on the recorder's translation of `Result.Err`; the substring "auto-revert" must appear somewhere on the CR (status, message, or events).

## Metrics

After the auto-revert window completes, the cisco-vk pod's /metrics endpoint should show:

```
cisco_vk_config_transactions_total{device="cat9k-smoke",transport="netconf",outcome="auto_reverted"} 1
cisco_vk_config_transactions_total{device="cat9k-smoke",transport="netconf",outcome="confirmed"}     0
```

`outcome="confirmed"` MUST be zero — the engine never reached `ConfirmCommit`. If it's non-zero, the auto-revert path was bypassed and the test failed (the device is still in its pre-confirmed state, which means the change did NOT auto-revert).

`outcome="discard"` may be 0 or 1 depending on whether the session re-established in time for the deferred Discard to fire. Either is acceptable; auto-revert happens regardless.

## Device-side state

**Identical to pre-state.txt** after the auto-revert window. RESTCONF check on the management interface's ACL binding:

```sh
curl --silent --insecure --user "${USER}:${PASS}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/${MGMT_INTF_TYPE}=${MGMT_INTF_NAME}/ip/access-group"
```

Should return whatever was there pre-test (typically nothing, or the pre-existing security-baseline ACL). The `TEST-08-MGMT-LOCKOUT` ACL itself should also be absent from the device's ACL list:

```sh
curl --silent --insecure --user "${USER}:${PASS}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/ip/access-list/extended=TEST-08-MGMT-LOCKOUT" \
  --output /dev/null --write-out '%{http_code}'
```

Should return 404 (ACL no longer present).

## What would fail

| Failure | Likely cause |
|---|---|
| ACL still on device after timeout | Auto-revert didn't fire. Older IOS-XE image without proper `:confirmed-commit:1.0` support, OR the device's hello did advertise the capability but the implementation is broken. Operator console rollback required. |
| `outcome="confirmed"` != 0 | The engine somehow reached ConfirmCommit despite the session drop. The `runningVerify` may not have detected the connectivity loss. Investigate the cisco-vk pod's logs for the verify timing. |
| `outcome="commit"` != 0 (legacy outcome from plain Commit) | The engine took the fallback path. Either confirmTimeoutSeconds wasn't propagated, or the transport doesn't satisfy ConfirmedCommitter. Check `Result.ConfirmedCommitFallback` on the CR. |
| Pod logs show "ConfirmCommit: ..." error | Tentative commit landed but the follow-up confirm RPC failed. This is also a safe failure mode (auto-revert still fires) but signals an operational issue worth investigating. |
