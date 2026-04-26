# Release-blocker test runbook — Cat9K live-device retests

**Authoritative target:** the six §6.D.ii live-device retests in [`../architectural-review-final.md`](../architectural-review-final.md), classified as release-tag blockers per the Wave-9-status reviewer round.
**Reference device:** `cat9k-smoke` / 10.1.1.1 / `C9300-24UX` (serial FCW2247C1AJ, IOS-XE 17.18.1) — adapt the device-specific fields below to your lab.
**Audience:** an operator with lab access, a maintenance window, and the ability to revert device configuration changes.

This runbook turns the six release-tag blockers into linear, scripted operator work. Each test has its own subdirectory with a self-contained manifest set, pre-state capture, expected outcome, verify, and rollback. Execute the tests in the order given (§3 below) — the order is least-disruptive to most-disruptive, so an early failure stops you before the high-impact tests run.

---

## 1. Prerequisites

Before starting the maintenance window, confirm:

- [ ] **kubectl context** is the kind cluster that runs the cisco-vk pod. `kubectl config current-context` should report `kind-kind` (or whatever your local cluster name is).
- [ ] **CiscoDevice CR exists** and is `Ready`: `kubectl get ciscodevice cat9k-smoke -n cisco-vk-smoke` shows `PHASE=Ready`.
- [ ] **cisco-vk pod is healthy**: `kubectl get pod -n cisco-vk-smoke -l app=cisco-vk -o wide` shows `1/1 Running`.
- [ ] **Device password** is set in the operator's environment: `export CVK_CONFIG_LINT_PASSWORD='...'`.
- [ ] **`cisco-vk-config-lint` binary** is on PATH (`which cisco-vk-config-lint`) or you can run it via `go run ./tools/cisco-vk-config-lint`.
- [ ] **`jq` and `kubectl`** are on PATH.
- [ ] **Maintenance window confirmed** with whoever owns the device. Tests 04 and 06 modify a physical interface description and a system-level field respectively; tests 01, 03, and 06 add and remove device-side state (Loopbacks, VLANs).
- [ ] **Rollback console reachable** independently of the cisco-vk pod (e.g. direct SSH/console). If anything goes wrong, the operator must be able to revert manually.

---

## 2. Preflight gate (mandatory before any device touch)

Before running the snapshot or any per-test apply, run the preflight gate. It fails fast if `kubectl` is on the wrong context, the device is not Ready, the cisco-vk pod is not Ready, the device password is unset, the required transports (RESTCONF/NETCONF/gNMI) aren't reachable, or the test 04 target interface hasn't been explicitly approved.

```sh
cd docs/rfcs/final/release-blocker-tests
./preflight.sh --intf-approved=GigabitEthernet0/0/0   # confirm the test 04 port
```

Save the preflight output as the first artefact in your evidence bundle (see §6).

---

## 3. Capture the pre-test baseline

Run once at the start of the window, **after preflight passes**. The snapshot is the diff baseline every test rolls back against.

```sh
mkdir -p _snapshots/window-$(date +%Y%m%d-%H%M)
./fetch-running-config.sh _snapshots/window-$(date +%Y%m%d-%H%M)
```

Output goes into a timestamped directory. Keep it for the duration of the window — every per-test verify step diffs against it.

---

## 4. Execution order

Ordered least-disruptive to most-disruptive. **Stop and investigate** at the first test whose verify step fails — do not push through to the next test until the current one is back to baseline.

| Order | Test | What it touches | Why ordered here |
|---|---|---|---|
| 1 | [`02-netconf-transactional-cli-rejection/`](./02-netconf-transactional-cli-rejection/) | Engine boundary check; **the engine should reject before any device write** | Safest — by design no device-side change occurs. Confirms the Wave 7A.1 fail-fast guard. |
| 2 | [`04-gnmi-keyed-path/`](./04-gnmi-keyed-path/) | Adds/removes a description on a chosen interface | Cosmetic; one-line revert; proves Wave 5A-fu + 7B PathSpec on the wire and `cisco_vk_config_mutate_ops_total{transport=gnmi}` >= 1. |
| 3 | [`05-credential-rotation-overlap/`](./05-credential-rotation-overlap/) | Rolls the cisco-vk Deployment ReplicaSet | No device-side write; tests pod-side lease handover and sub-TTL requeue (Waves 6B + 7A.3 + 8.2 + 9.2). |
| 4 | [`01-netconf-transactional/`](./01-netconf-transactional/) | Adds a benign Loopback interface via NETCONF candidate + commit | Clean rollback (delete the loopback); proves Wave 1A-fu transactional path and `cisco_vk_config_transactions_total{transport=netconf,outcome=commit}` >= 1. |
| 5 | [`07-write-startup-save-config/`](./07-write-startup-save-config/) | Adds Loopback9997 then persists to startup-config via `writeStartup=true` | Last test that adds device state; runs before the deletion-finalizer test. Proves the Wave 1A `writeStartup` plumb live-end-to-end via `SaveStartupOK` event + `cisco_vk_config_save_startup_total{outcome=ok}` >= 1. **Modifies startup-config** — needs explicit operator approval. |
| 6 | [`06-driftpolicy-revert-live-write/`](./06-driftpolicy-revert-live-write/) | Flips a managed family from `report` to `revert`, then back | Visible but reversible; the live-revert only touches the family the test names. |
| 7 | [`11-confirmed-commit-restconf-fallback/`](./11-confirmed-commit-restconf-fallback/) | Non-transactional CR with `confirmTimeoutSeconds=30` set anyway | **Lowest-risk Wave-10 live test** — no auto-revert path engaged; proves the engine surfaces `ConfirmedCommitFallback` Warning event with reason `non-transactional reconcile`. Run it before any test that engages the auto-revert path so a fallback regression surfaces early. |
| 8 | [`10-confirmed-commit-happy-path/`](./10-confirmed-commit-happy-path/) | `confirmTimeoutSeconds=30`, NETCONF transport, clean apply | Wave 10 happy-path proof. Asserts `ConfirmedCommitUsed` event AND `outcome="confirmed"` metric increment. Catches the silent-fallback regression where the engine should engage auto-revert but doesn't. |
| 9 | [`09-atomic-replace-cross-family/`](./09-atomic-replace-cross-family/) | Establishes VLAN/VRF/Loopback then atomically removes all three with `atomicReplace=true` | Two-phase test of Wave 10.3. The atomic removal is one transaction; partial-drift is the failure mode the test exists to prevent. Cross-family ordering exercised. |
| 10 | [`13-atomic-replace-with-confirmed-commit/`](./13-atomic-replace-with-confirmed-commit/) | Same shape as test 09 but with both Wave 10 safety nets engaged simultaneously | The recommended-default proof — both Wave 10 features compose. Two `ConfirmedCommitUsed` events expected (one per phase), zero fallbacks, zero auto-reverts. |
| 11 | [`08-confirmed-commit-auto-revert/`](./08-confirmed-commit-auto-revert/) | Deliberately submits a management-plane-breaking ACL with `confirmTimeoutSeconds=30`; device must auto-revert | Wave 10.1+10.2 headline. **Most invasive in the runbook** — out-of-band console must be attached before applying. The test's whole point is to break the controller's session and prove the device's own timer recovers it. |
| 12 | [`03-configprereqs-cleanup/`](./03-configprereqs-cleanup/) | Most invasive: removes device-side configPrereqs state on CiscoDevice deletion | Last because it exercises the deletion finalizer end-to-end (Waves 4A-fu + 7A.2 + 7A.4); only run after the other tests pass. |

Each subdirectory has its own `README.md` with the closing-wave anchors, exact device surface used, and pre-state/run/verify/rollback steps. Each test's `verify.sh` sources [`lib/baseline.sh`](./lib/baseline.sh) for the §5-stricter assertions (observedGeneration matches generation, no ApplyError, no stale LeaseBlocked, transport-aware metric proofs).

---

## 5. Per-test execution template

Every test follows the same six-step pattern. The per-test `README.md` may add specifics on top of this template.

```sh
cd docs/rfcs/final/release-blocker-tests/<NN-test-name>

# 4.1 — Read what the test does and what device surface it uses.
cat README.md

# 4.2 — Capture per-test pre-state (specific to the test's surface).
./pre-state.sh > pre-state.txt
cat pre-state.txt

# 4.3 — Apply the test manifest.
kubectl apply -f 00-apply.yaml
# Some tests (02, 05) require additional kubectl actions — see README.

# 4.4 — Wait for the reconciler to reach a terminal phase. The expected
#       phase is in expected.md. Useful watcher:
kubectl get iosxeconfig -n cisco-vk-smoke -w
# Stop with Ctrl+C once the test CR is in its terminal phase.

# 4.5 — Verify post-state matches expected.md.
./verify.sh   # exits 0 on success, non-zero on assertion failure

# 4.6 — Rollback. Restore the device to the pre-state.
./rollback.sh
# Then confirm the device is clean:
./pre-state.sh > post-state.txt
diff pre-state.txt post-state.txt   # should be empty
```

If `verify.sh` fails:
1. Capture `kubectl describe iosxeconfig -n cisco-vk-smoke <test-cr-name>`.
2. Capture `kubectl logs -n cisco-vk-smoke <cisco-vk-pod-name> --tail 200`.
3. Run `./rollback.sh` to restore the device.
4. Re-run `pre-state.sh` and confirm the device is back to baseline.
5. File the test failure as a bug; do not advance to the next test.

---

## 6. End-of-window close

After the last test:

```sh
# Capture the post-window snapshot.
./fetch-running-config.sh _snapshots/window-end-$(date +%Y%m%d-%H%M)

# Diff against the start-of-window snapshot.
diff -ru _snapshots/window-<start>/native.json _snapshots/window-end-*/native.json | head -100
```

The diff should be **empty** (the rollback after each test brings the device back to baseline). If it is not, identify which test's rollback was incomplete and revert manually before closing the window.

Then update [`../architectural-review-final.md`](../architectural-review-final.md) §6.D.ii: change each row's disposition from `🔒 release blocker` to `✅ verified live` and reference the snapshot directory + window date as the closing evidence.

---

## 7. Failure modes worth pre-thinking

| Failure | Likely cause | Recovery |
|---|---|---|
| `verify.sh` reports `Phase=Failed` instead of expected phase | engine rejected the intent; check `kubectl describe` for the reason. | Run `rollback.sh`; do not advance. |
| Device-side state diverges after rollback | a parallel reconcile happened between apply and rollback; another CR may target the same family | Pause all other reconciles (`kubectl annotate iosxeconfig --all paused=true` if you have such an annotation, or scale the cisco-vk pod to 0); revert manually via console. |
| `cisco-vk-config-lint` cannot reach 10.1.1.1 | network path to the device is down | Stop the window. Tests cannot continue. |
| cisco-vk pod stuck in `LeaseBlocked` after test 05 | aggregator-vs-per-pod overlap or stale lease holder | `kubectl get lease -A | grep cvk-` to inspect; `kubectl delete lease <stale-lease>` if the holder is a deleted pod. |
| Test 03 cleanup leaves device-side prereq state | finalizer didn't run because pod was killed mid-deletion | Re-create the CiscoDevice transiently to complete the empty-intent reconcile, then delete cleanly; or revert manually via console. |

---

## 8. Source authority

This runbook is derived from:

- The architectural review's §6.D.ii enumeration: [`../architectural-review-final.md`](../architectural-review-final.md).
- The wave-by-wave RFCs that describe the expected behaviour for each closing wave (cited in each test's `README.md`).
- The project's reference example manifests under [`../../../../examples/gitops-reference/`](../../../../examples/gitops-reference/) and [`../../../../examples/configs/`](../../../../examples/configs/).
- The schema registry [`../../../../internal/drivers/iosxe/configdriver/schema/families.yaml`](../../../../internal/drivers/iosxe/configdriver/schema/families.yaml).

If anything in a per-test `README.md` disagrees with the closing-wave RFC it cites, **the wave RFC wins** — patch the test, not the wave.
