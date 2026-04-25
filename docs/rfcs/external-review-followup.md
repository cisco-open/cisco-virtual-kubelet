# External Review Follow-Up

Date: 2026-04-25
Reviewer: Codex
Scope: post-fix review after the remediation commits through `2e83ac9`

This note follows `docs/rfcs/external-review.md` and
`docs/rfcs/external-review-response.md`. The remediation branch has made real
progress: most of the original RBAC, bundle, YANG, conflict-status, polling
drift, and registry-test issues are fixed. However, the current branch still
has two production-blocking semantic gaps and three day-2/transport gaps that
should be resolved before re-claiming full day-2 readiness.

## Current Verdict

The implementation is substantially improved, but not complete.

The per-pod and aggregator topologies are now much closer to the intended
architecture, and the shipped Helm RBAC is no longer the obvious blocker it was
in the prior review. The remaining problems are less broad, but more subtle:
NETCONF transactional semantics still do not work reliably, configPrereqs
teardown cannot converge through the real API/engine path, per-pod gNMI
subscribe notifications are still not consumed, gNMI keyed paths remain
ambiguous for important writer paths, and CiscoDevice credential Secret
rotation is not operationally reconciled.

## Verification Performed

Commands run from the repository root:

```bash
git diff --check
helm lint charts/cisco-virtual-kubelet
helm template cvk charts/cisco-virtual-kubelet --namespace cvk-system --set aggregator.enabled=true --set config.leaseNamespace=cvk-system
GOCACHE=/tmp/cvk-gocache go test ./internal/drivers
GOCACHE=/tmp/cvk-gocache go test ./internal/drivers/iosxe/configdriver/engine ./internal/provider ./internal/controller ./internal/drivers/iosxe/configdriver/transport
GOCACHE=/tmp/cvk-gocache go test -race -count=20 ./internal/drivers
GOCACHE=/tmp/cvk-gocache go test ./...
GOCACHE=/tmp/cvk-gocache go test -race ./...
GOCACHE=/tmp/cvk-gocache go vet ./...
```

Notes:

- The full and transport-related Go test suites require loopback listeners for
  `httptest`; they pass when run outside the network-restricted sandbox.
- The registry race reproduction from the earlier review now passes.
- Passing tests do not cover the semantic gaps below; several fixes need
  stronger regression tests.

## Remaining Findings

### P1: Transactional NETCONF Still Cannot Reliably Commit

Location:

- `internal/drivers/iosxe/configdriver/engine/engine.go:422`
- `internal/drivers/iosxe/configdriver/engine/engine.go:506`

What is fixed:

- The engine now opens a transaction when requested and supported.
- Writer `Mutate` calls now go through a transaction-bound transport wrapper.
- The engine commits on clean success and discards on failure.
- `writeStartup` now calls `SaveStartup` after successful apply.

What is still wrong:

- The verify path still calls `Fetch` through `transactionalView`.
- `transactionalView.Fetch` delegates to the raw transport.
- NETCONF `Fetch` is hard-coded to `running`, not `candidate`.
- After editing candidate, verify reads the old running config, sees residual
  diff, marks the tick failed, and discards instead of committing.
- CLI template blocks call `e.Transport.Mutate(ctx, "", ...)` directly, so
  they bypass the transaction wrapper and write outside the transaction scope.

Why this matters:

Transactional NETCONF is one of the branch's advertised production semantics.
As written, structured NETCONF transactional applies can fail before commit,
and CLI template operations are not atomic with the rest of the reconcile.

Suggested fix shape:

- Add an explicit tx-aware read path for candidate verification. Options:
  - Add a transport method or optional interface for `Fetch` against a
    transaction/candidate datastore.
  - Teach `transactionalView.Fetch` to use that optional interface when present.
  - Implement NETCONF candidate reads with `<get-config><source><candidate/>`.
- Route CLI template apply through the same `applyTransport` used by family
  writers, or explicitly reject/document CLI templates under transactional mode.
- Add regression tests that prove:
  - NETCONF transactional verify reads candidate, not running.
  - A successful transactional apply reaches `Commit`.
  - CLI template ops either use the same tx handle or are rejected before any
    partial write.

### P1: configPrereqs Teardown Cannot Converge

Location:

- `internal/controller/ciscodevice_controller.go:608-613`
- `internal/drivers/iosxe/configdriver/engine/engine.go:309-311`
- `api/config/v1alpha1/iosxeconfig_types.go:73-80`

What is fixed:

- The controller no longer immediately deletes the owned IOSXEConfig when
  `spec.configPrereqs` is removed.
- The owned CR is created with `pruneOnRelinquish: true`.
- The controller now attempts a teardown sequence before deletion.

What is still wrong:

- The teardown sequence sets `managedFamilies=nil`.
- The IOSXEConfig CRD requires `managedFamilies` and enforces `MinItems=1`.
- A real API server should reject this update.
- Even if the update is accepted, the engine rejects empty `ManagedFamilies`.
- Prune only runs inside the per-family loop, so an empty family list gives the
  engine no family to fetch, diff, or prune.
- CiscoDevice deletion can therefore wait forever for the owned CR to become
  `InSync`.

Why this matters:

This is the day-0/day-2 cleanup promise for apphosting prerequisites. If it
does not work, deleting a CiscoDevice or removing configPrereqs can leave
device-side state behind and/or wedge the CiscoDevice finalizer.

Suggested fix shape:

- Do not clear `managedFamilies` during teardown.
- Keep the prereq family set, set `source.inline` to an empty intent for those
  families, and keep `pruneOnRelinquish: true`.
- Confirm every prereq family has a real `PruneDiff` implementation or make the
  controller status explicit when a family cannot be pruned.
- Add an envtest/API-server-style regression or schema-validation test, not
  only a fake-client test, so the MinItems/required-field contract is exercised.
- Add an engine/controller regression that verifies the teardown reconcile runs
  with non-empty families and reaches `InSync` before the CR is deleted.

### P2: gNMI Subscribe Fast Path Remains Unused In Per-Pod Mode

Location:

- `cmd/cisco-vk/config_reconciler.go:175-198`
- `internal/provider/config_reconciler_controller.go:184-239`
- `internal/provider/config_reconciler.go:103-143`

What is fixed:

- Periodic drift detection now honors `spec.driftDetectInterval`.
- `SubscribeNotify` is consumed by the polling `ConfigReconciler.Run` path.
- Aggregator workers use `ConfigReconciler.Run`, so the aggregator topology can
  consume subscribe notifications.

What is still wrong:

- The default per-pod production topology uses controller-runtime
  `SetupWithManager`.
- That path creates `SubscribeNotify` and sets it on `ConfigReconciler`.
- `SetupWithManager`/`Reconcile` never reads from that channel.
- Only the legacy polling `Run` loop consumes `SubscribeNotify`.

Why this matters:

The branch now has working periodic day-2 drift polling, but the advertised gNMI
on-change fast path is still not active in the default per-pod production path.

Suggested fix shape:

- Wire the notify channel into controller-runtime as an event source that
  enqueues IOSXEConfigs targeting the device.
- Alternatively, run a small goroutine beside the manager that reads notify and
  enqueues reconcile requests for all matching IOSXEConfigs.
- Add a test proving a notify event causes a per-pod controller-runtime
  reconcile even when generation/hash/status would otherwise short-circuit.

### P2: gNMI Keyed Path Conversion Is Still Not Production-Safe

Location:

- `internal/drivers/iosxe/configdriver/transport/gnmi.go:485-515`
- `internal/drivers/iosxe/configdriver/transport/gnmi_keys.go`
- `internal/drivers/iosxe/configdriver/schema/index.go:92-118`
- `internal/drivers/iosxe/configdriver/schema/families.yaml`

What is fixed:

- A schema-aware key-name registry was added.
- `parseGNMIPath` can now prefer registered key fields over the old
  string/name and numeric/id heuristic.

What is still wrong:

- `parseGNMIPath` splits on `/` before parsing keyed values.
- Interface paths such as `GigabitEthernet=0/0/0` are therefore split into
  multiple path elements instead of one keyed `GigabitEthernet` element.
- The registry is populated only as a side effect of `schema.LoadFamilies`.
- Production driver setup does not call `schema.LoadFamilies`; it uses
  `iosxebuilder.KeyRulesForXE()` and writer paths instead.
- For `key_fields: [type, name]`, using the first registered field is wrong:
  concrete interface path segments already encode the type, and the gNMI list
  key should be `name`.

Why this matters:

gNMI Set/Delete can target the wrong path or no path at all for important
families such as physical interfaces, switchport, and any key values containing
slashes. This limits gNMI as a real transport for the advertised family set.

Suggested fix shape:

- Avoid parsing key metadata out of ambiguous string paths where key values may
  contain `/`.
- Prefer a structured path representation or key metadata on `transport.Op`
  for gNMI.
- If keeping string paths temporarily, define an unambiguous key syntax and
  update writers consistently.
- Populate key metadata in production startup, not only through doc/schema
  tooling side effects.
- Treat concrete interface paths specially: `GigabitEthernet=<name>` should map
  to `PathElem{Name:"GigabitEthernet", Key:{"name": <name>}}`.
- Add tests for:
  - `GigabitEthernet=0/0/0`
  - `GigabitEthernet=0/0/1/switchport`
  - crypto transform-set keyed by `tag`
  - prefix/list values that include characters the current parser might split
    or misclassify.

### P2: CiscoDevice Credential Secret Rotation Is Not Reconciled

Location:

- `internal/controller/ciscodevice_controller.go:310-318`
- `internal/aggregator/aggregator.go:127-144`
- `internal/aggregator/aggregator.go:258-267`

What is fixed elsewhere:

- IOSXEConfig `spec.secretRefs` now trigger per-pod reconciliation.

What is still wrong:

- This is a different Secret path: `CiscoDevice.spec.credentialSecretRef`.
- Per-pod mode injects the Secret as an environment variable.
- Kubernetes does not restart pods when a Secret-backed environment variable
  changes.
- The CiscoDevice controller does not watch credential Secrets or roll the
  Deployment on Secret changes.
- Aggregator mode resolves the password when starting a worker.
- `specHash` only records whether a password exists, not whether it changed.
- The aggregator watches CiscoDevice events, not Secret events.

Why this matters:

Credential rotation is a normal day-2 operation. With the current branch, a
rotated device password may not be used until an unrelated CiscoDevice edit,
pod restart, or manager restart occurs.

Suggested fix shape:

- In per-pod mode, watch Secrets referenced by CiscoDevices and roll the
  Deployment when the referenced Secret changes.
- A low-leakage rollout key can use the Secret `resourceVersion` rather than
  hashing secret data into the Deployment annotation.
- In aggregator mode, watch credential Secrets and restart affected workers.
- Update `specHash` to include either a password digest or referenced Secret
  resourceVersion so worker refresh is tied to actual credential changes.
- Add tests for:
  - per-pod Deployment template annotation changes after credential Secret
    update;
  - aggregator worker restarts after referenced credential Secret update;
  - unrelated Secret updates do not restart every device unnecessarily.

## Original Findings Status

| Original finding | Current status |
| --- | --- |
| Transactional/writeStartup inert | Partially fixed; transaction/save-startup wired, but NETCONF candidate verify and CLI tx scope remain open |
| Steady-state drift bypassed | Mostly fixed for polling/controller-runtime; per-pod gNMI subscribe still not consumed |
| Aggregator coexists with per-device config loops | Fixed for duplicate config-writer hazard |
| Aggregator Helm RBAC incomplete | Fixed in rendered Helm RBAC |
| VK replay cleanup RBAC missing | Fixed |
| Production YANG defaulting dropped | Fixed |
| Conflict status first-family only | Fixed |
| configPrereqs deletion does not revert | Attempted, but still broken due CRD/engine empty-family semantics |
| Bundle selector membership not watched | Fixed |
| Bundle template requires deviceRef | Fixed |
| IOSXEConfig secretRefs changes not watched | Fixed |
| gNMI keyed paths schema-blind | Attempted, but still not production-safe |
| Registry tests race with global state | Fixed; race reproduction passes |

## Recommended Next Wave

1. Fix NETCONF transaction verification and CLI transaction scope.
2. Redesign configPrereqs teardown so it stays schema-valid and gives the
   engine non-empty families to prune.
3. Wire `SubscribeNotify` into the controller-runtime per-pod path.
4. Rework gNMI path/key handling with structured key metadata rather than
   ambiguous string parsing.
5. Add credential Secret rotation reconciliation for both per-pod and
   aggregator topologies.

After those land, re-run:

```bash
GOCACHE=/tmp/cvk-gocache go test ./...
GOCACHE=/tmp/cvk-gocache go test -race ./...
GOCACHE=/tmp/cvk-gocache go test -race -count=20 ./internal/drivers
helm lint charts/cisco-virtual-kubelet
helm template cvk charts/cisco-virtual-kubelet --namespace cvk-system --set aggregator.enabled=true --set config.leaseNamespace=cvk-system
```
