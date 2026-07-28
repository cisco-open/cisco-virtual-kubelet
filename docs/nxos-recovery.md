# NX-OS Configuration Recovery

`NXOSConfig` writes directly to running configuration through NX-API
REST/DME. The transport has no candidate datastore, transaction, confirmed
commit, revision history, or automatic rollback. A transport error can also be
ambiguous: NX-OS may have accepted a mutation even when CVK did not receive its
response.

CVK never retries a mutation within the same reconcile tick. A later reconcile
starts with a fresh Fetch and Diff, so an accepted write whose acknowledgement
was lost becomes an in-sync no-op instead of being replayed. Operators should
still treat every `ApplyError` or post-apply `Drifted` result as a potentially
partial change until device state has been inspected.

## Recovery procedure

1. Capture the failed object and events before changing its policy. A pause
   reconcile intentionally replaces the active result with `Paused`, so this
   evidence must come first:

   ```bash
   kubectl get nxosconfig <name> -n <namespace> -o yaml
   kubectl events -n <namespace> --for nxosconfig/<name>
   ```

2. Pause new writes, then confirm that the object reached `Paused`:

   ```bash
   kubectl patch nxosconfig <name> -n <namespace> --type merge \
     -p '{"spec":{"driftPolicy":"pause"}}'
   kubectl get nxosconfig <name> -n <namespace> \
     -o jsonpath='{.status.phase}{"\n"}'
   ```

3. Capture the current device state with read-only `DeviceOperation` commands
   or an independently approved NX-API session. Check every managed family
   reported before and at the failure; do not infer device state solely from
   the HTTP result.
4. Compare that state with the resolved source revision. If the intended
   values are already present, no compensating write is needed. If the device
   is only partly changed, update the source to an explicit, reviewed target
   state. Do not send the failed HTTP request again by hand.
5. For an unwanted change, prefer a compensating `NXOSConfig` value over an
   ad-hoc CLI reversal. Use deletion only where the family supports scoped
   prune and status proves that this CR owns the object.
6. Resume reconciliation:

   ```bash
   kubectl patch nxosconfig <name> -n <namespace> --type merge \
     -p '{"spec":{"driftPolicy":"revert"}}'
   kubectl get nxosconfig <name> -n <namespace> -w
   ```

7. Require `status.phase: InSync`, clean per-family status, and a fresh
   observed-state verification before closing the incident. When
   `writeStartup: true`, CVK saves running configuration only after the whole
   reconcile is in sync; confirm that save separately if persistence matters.

If device access, ownership, or the intended pre-change value is uncertain,
leave the CR paused and escalate through the device change-control process.
Removing the CR does not restore previous configuration.

## Family-specific compensation

| Family | Recovery and safety boundary |
|---|---|
| `system` | Set the reviewed hostname and system MTU explicitly, then verify both. There is no generic “delete system value” rollback. A management-path change requires out-of-band access before compensation. |
| `feature` | Restore each affected feature with an explicit Boolean and verify operational feature state. CVK refuses to disable protected management features (`nxapi`, `ssh`, `scp_server`, `sftp_server`, and `tacacs`); use an approved device-native procedure if one of those must be disabled. |
| `feature_set` | Restore `fex`, `mpls`, or `virtualization` explicitly and verify installation/admin state. Check feature dependencies before disabling a set. |
| `vlan` | Restore the VLAN name explicitly. A VLAN can be removed only through scoped prune when status proves CR ownership; VLAN 1 is never deleted. Confirm the VLAN is not carrying production traffic before compensating or pruning. |
| `interface_ethernet` | Restore description, MTU, and shutdown state explicitly. CVK does not delete physical interfaces and will not perform an implicit Layer-3-to-Layer-2 or shutdown-to-up conversion. Confirm an alternate management path before changing admin state or MTU. |

## Deterministic recovery evidence

The offline oracle gate includes a failure-injection test in which the device
accepts a VLAN DME mutation but its acknowledgement is lost. The first
reconcile reports failure; the next Fetch observes the intended state, reaches
`InSync`, and proves that the ambiguous mutation was not replayed.

Run the complete credential-free gate with:

```bash
make check-nxos-oracle
```

The gate also checks semantic DME parity against the pinned Network as Code
oracle, validates its runtime contract, and verifies every fixture against
`SHA256SUMS`.

## Updating the pinned oracle

Oracle refreshes are deliberate reviewed changes, not automatic CI downloads.
Regenerate the canonical, resolved, Terraform plan, and provider DME artifacts
with the exact module/provider revisions recorded in `contract.json`; inspect
the semantic diff; then update `SHA256SUMS` and run
`make check-nxos-oracle`.

A scheduled upstream-drift alert is deferred. The required PR gate remains
offline and reproducible; a future alert should be credential-free,
non-blocking, and report upstream metadata drift without silently rewriting
the qualified fixtures.
