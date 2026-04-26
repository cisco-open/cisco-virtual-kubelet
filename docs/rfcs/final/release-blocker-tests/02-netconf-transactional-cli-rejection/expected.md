# Test 02 — Expected outcome

The engine MUST reject the IOSXEConfig before any RPC is sent to the device.

## Phase + condition

```yaml
status:
  phase: Failed
  observedGeneration: 1
  conditions:
    - type: Ready
      status: "False"
      reason: ErrTransactionalCLIUnsupported  # exact string, set by engine.validate()
      message: # human-readable explanation; substring "transactional" + "CLI" expected
```

## Device-side state

**Identical to pre-state.txt.** No banner change, no hostname change, no edit-config in the device's syslog if the device is configured to log NETCONF events.

## Cisco-vk pod logs

The reconcile log line for `test-02-cli-rejection` should report:

- the resolved-intent hash being computed,
- the engine entering Validating phase,
- engine.validate() rejecting with `ErrTransactionalCLIUnsupported`,
- recordResult writing `Phase=Failed`,
- **no** subsequent `Fetch`, `StartTransaction`, `Mutate`, `Commit`, or any other transport call for this CR.

The `cisco_vk_engine_reconciles_total{phase="Failed"}` counter increments by 1; the `cisco_vk_engine_apply_ops_total` counter does NOT increment.
