# Test 07 — `writeStartup: true` live save-config coverage

**§6.D.ii row added per pre-PR-test-enrichment-plan §4:** "writeStartup live coverage — apply with `writeStartup: true`, verify SaveStartupOK event AND device-side `show startup-config` reflects the change."
**Closing wave:** Wave 1A (`writeStartup` plumb) — the engine path is unit-tested in [`internal/drivers/iosxe/configdriver/engine/transactional_test.go`](../../../../internal/drivers/iosxe/configdriver/engine/transactional_test.go) (`TestSaveStartupCalledOnSuccessWhenRequested` / `TestSaveStartupFailureIsNonFatal`); production-readiness needs the live save-config because IOS-XE's running-to-startup copy semantics are device-specific.

## What this test proves

1. **Engine reaches SaveStartup.** Running config converges first (Phase=InSync), then the engine calls `transport.SaveStartup` because `spec.writeStartup=true` AND `Capabilities.SupportsSaveStartup=true`.
2. **Save-startup metric increments** — `cisco_vk_config_save_startup_total{transport=<...>,outcome=ok}` shows a new value, proving the path actually ran (rather than a previous tick's success being remembered).
3. **`SaveStartupOK` Kubernetes event** is emitted on the IOSXEConfig CR.
4. **Startup-config persists the change.** `show startup-config | include <test-marker>` should include the test marker after save; no marker before save.
5. **Rollback** undoes the running-config change AND the startup-config persistence so the device is back to baseline.

## Device surface used

**`Loopback9997`** — chosen to be far outside lab range (0–100), distinct from test 01's `Loopback9999` and test 03's `Loopback9998` so the three loopback tests can in principle run independently in the same window. The test sets a description and IP `10.255.255.97/32`. Running-config receives it; startup-config receives it once `SaveStartup` runs.

This is the test that **modifies startup-config** — re-applying a flash write. The runbook orders this test late in the maintenance window because (a) recovery from an interrupted save-config is slower than a running-config-only revert and (b) the operator should explicitly approve the persisted-write before it runs.

## Pre-state

```sh
bash ./pre-state.sh > pre-state.txt
```

Captures both running-config and startup-config sections for the loopback. Asserts `Loopback9997` is in neither.

## Apply

```sh
kubectl apply -f 00-apply.yaml
```

Creates the `IOSXEConfig` with `transactional: true` (so this test also re-exercises the transactional path with a different surface than test 01) AND `writeStartup: true`. Watches for Phase=InSync, then SaveStartupOK event.

## Expected

See [`expected.md`](./expected.md). Summary:

- `status.phase == InSync`.
- `status.familyStatus[interface_loopback].state == InSync`.
- A Kubernetes event `SaveStartupOK` on the CR.
- `cisco_vk_config_save_startup_total{transport=<...>,outcome=ok}` >= 1.
- Running config: `Loopback9997` present.
- Startup config: `Loopback9997` present (persisted).

## Verify

```sh
bash ./verify.sh
```

## Rollback

```sh
bash ./rollback.sh
```

Two-phase rollback per the §4 recommendation — running config first, then save-config again so startup-config is also clean of the test loopback. The test's whole point is to prove that startup persistence works; the rollback re-uses the same path to clean it.
