# Test 07 — Expected outcome

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

The reconciler emits a Normal `SaveStartupOK` event after a successful
running-to-startup copy. Verifiable via:

```sh
kubectl events -n cisco-vk-smoke --for IOSXEConfig/test-07-write-startup --sort-by=.lastTimestamp
```

The event message format is "startup-config saved" (see [`internal/provider/config_reconciler.go`](../../../../internal/provider/config_reconciler.go)).

If `writeStartup=true` but the transport reports `SupportsSaveStartup=false`, the engine silently skips the save and emits no `SaveStartupOK` event. That path is unit-tested but not exercised by this live test (any IOS-XE transport supports save).

If `SaveStartup` is invoked but returns an error, the reconciler emits a Warning `SaveStartupFailed` event with the error message — that is treated as non-fatal (running-config is already in place; only startup persistence failed) but the test fails because the §4 plan requires a successful save.

## Running config

`Loopback9997` exists with the test description and IP. RESTCONF check:

```sh
curl --silent --insecure --user "${USER}:${PASS}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/Loopback=9997"
```

Should return `200 OK`.

## Startup config

`Loopback9997` ALSO exists in startup-config. RESTCONF native models can read startup via the `Cisco-IOS-XE-rpc:save` operation only — the running-vs-startup divergence is what makes the live test necessary. Operator-friendly check via SSH:

```sh
ssh ${USER}@${ADDR} "show startup-config | include Loopback9997"
```

Expected output: a non-empty match for the loopback's description or IP. The verify script does not require SSH; it accepts an operator-attached attestation that startup-config contains the loopback (e.g. a copy-paste of the `show startup-config` excerpt as a comment in the verify output).

## Metrics

- `cisco_vk_config_transactions_total{device=<cat9k-smoke>,transport=<netconf|restconf>,outcome=commit}` increments by 1 (this test also re-exercises the transactional path).
- `cisco_vk_config_save_startup_total{device=<cat9k-smoke>,transport=<netconf|restconf>,outcome=ok}` increments by 1 — the headline metric for this test.
- `cisco_vk_config_save_startup_total{...,outcome=failed}` stays at 0.

## What would fail

- No `SaveStartupOK` event → engine reached InSync but the save call did not run; check `Capabilities.SupportsSaveStartup` for the transport.
- `SaveStartupFailed` event → save was called but returned an error; check the device's flash state (full disk, write-protected) and the error message.
- `save_startup_total{ok}` did not increment but a `SaveStartupOK` event exists → the metric wiring regressed; investigate `engine.go`'s `recordSaveStartup` call.
