# Transport architecture — RESTCONF, NETCONF, gNMI

**Audience:** maintainers and reviewers of the IOS-XE configuration driver. Operators wanting per-mode setup, CRD recipes, and troubleshooting should read [`./deployment-modes.md`](./deployment-modes.md) first.

**Branch:** `pr/johalley/ciscoconfig_xe`
**Source-of-truth packages:** [`internal/drivers/iosxe/configdriver/transport/`](../../internal/drivers/iosxe/configdriver/transport/), [`internal/drivers/iosxe/configdriver/writers/`](../../internal/drivers/iosxe/configdriver/writers/), [`internal/drivers/iosxe/configdriver/engine/`](../../internal/drivers/iosxe/configdriver/engine/).

This document is the single reference for how the three transports behave, where they diverge, and the recent fix bundles that brought them to operational parity. It supersedes scattered "transport details" in earlier RFC drafts.

---

## 1. Architecture overview

The configuration driver is **transport-agnostic at the writer layer**. Writers express intent as a `transport.Op` (verb + path + body + structured PathSpec); transports translate each Op to the underlying protocol (RESTCONF verb, NETCONF edit-config, gNMI SetRequest update). One writer drives all three transports without per-transport branching, modulo the four-fix bundle in commit `88ac685` that closed shape mismatches surfaced by NETCONF candidate-only mode.

```
                     ┌────────────────────────────────────────┐
                     │       configdriver/engine              │
                     │  (Validating → Planning → Applying →   │
                     │   Verifying → terminal phases)         │
                     └───────────────┬────────────────────────┘
                                     │  []transport.Op
                     ┌───────────────▼────────────────────────┐
                     │      configdriver/writers (~52         │
                     │   families: SectionWriter contract,    │
                     │   optional PruneCapable)               │
                     └───────────────┬────────────────────────┘
                                     │  Op{Verb,Path,PathSpec,Body}
                     ┌───────────────▼────────────────────────┐
                     │   configdriver/transport.Interface     │
                     │  + optional TxFetcher,                 │
                     │    ConfirmedCommitter,                 │
                     │    SubscribeCapable                    │
                     └─┬──────────────┬──────────────┬────────┘
                       │              │              │
                ┌──────▼──────┐ ┌─────▼──────┐ ┌─────▼──────┐
                │  RESTCONF   │ │  NETCONF   │ │   gNMI     │
                │ (HTTPS,     │ │ (SSH,      │ │ (gRPC,     │
                │  stateless) │ │  candidate │ │  trans-    │
                │             │ │  + commit) │ │  actional) │
                └─────────────┘ └────────────┘ └────────────┘
```

**Engine ↔ transport contract.** The engine produces an ordered family list (FamilyOrder hook over `schema/families.yaml` `depends_on`) and calls `Mutate` per family; the transport decides whether to batch into a transaction (NETCONF candidate, gNMI Set-atomic) or stream stateless (RESTCONF). The engine's transactional path uses `StartTransaction` / `Commit` / `Discard` only when `spec.transactional: true` AND the transport reports `Capabilities.SupportsTransactions = true`. Confirmed-commit (Wave 10.2) inserts `CommitConfirmed → runningVerify → ConfirmCommit` between candidate-verify and the existing terminal phases when `spec.confirmTimeoutSeconds > 0`.

---

## 2. The `transport.Interface` contract

Defined in [`transport/transport.go`](../../internal/drivers/iosxe/configdriver/transport/transport.go).

| Method | Required behavior |
|---|---|
| `Capabilities() Capabilities` | Static or post-hello derived feature flags (see §3). |
| `Fetch(ctx, path) []byte` | Read config; result is YANG-data-JSON whose top-level key is the **last path segment** qualified — RESTCONF semantics (see §5). Returns 404-equivalent error when the resource is absent so writers can treat empty-state cleanly via `isRESTCONF404`. |
| `StartTransaction(ctx) TxHandle` | Open candidate datastore (NETCONF) or transaction accumulator (gNMI). Returns `ErrUnsupported` on RESTCONF. |
| `Mutate(ctx, tx, ops) error` | Apply ops atomically when `tx != ""`; otherwise apply each op as the transport's natural unit. |
| `Commit(ctx, tx)` / `Discard(ctx, tx)` | Finalize / rollback. RESTCONF stub-implements (no-op + error). |
| `SaveStartup(ctx) error` | Persist running to startup-config. RESTCONF + NETCONF use the Cisco-IA `save-config` RPC; gNMI returns `ErrUnsupported`. |
| `Close() error` | Tear down sockets / sessions. |

Optional interfaces:

| Interface | Implemented by | Purpose |
|---|---|---|
| `TxFetcher` | NETCONF | Engine verify-Fetch reads candidate (not running) so the in-flight transaction's writes are visible mid-transaction (Wave 1A-fu). |
| `ConfirmedCommitter` | NETCONF | RFC 6241 §8.4 tentative commit + auto-revert timer (Wave 10.1/10.2). |
| `SubscribeCapable` | gNMI | Telemetry / on-change drift signal (Wave 6A). |

---

## 3. Capabilities matrix

| Capability | RESTCONF | NETCONF | gNMI |
|---|---|---|---|
| `SupportsTransactions` | ❌ stateless | ✅ when device advertises `:candidate:1.0` (probed via NETCONF hello) | ✅ accumulated then flushed in one SetRequest |
| `SupportsWritableRunning` | ✅ implicit (running is the only surface) | ✅ when device advertises `:writable-running:1.0`. Becomes ❌ when operator enables `netconf-yang feature candidate-datastore` on IOS-XE 17.x — see implicit-tx auto-promote in §6 | ✅ |
| `SupportsConfirmedCommit` | ❌ no protocol equivalent | ✅ when device advertises `:confirmed-commit:1.0` | ❌ Cisco hasn't shipped gNMI confirmed-commit |
| `SupportsSaveStartup` | ✅ (Cisco-IA RPC) | ✅ (Cisco-IA RPC) | ❌ returns ErrUnsupported |
| `SupportsSubscribe` | ❌ | ❌ | ✅ ON_CHANGE + SAMPLE |

The factory ([`transport/factory.go`](../../internal/drivers/iosxe/configdriver/transport/factory.go)) is also where **transport-aware port defaults** live, after commit `b2c1189` moved them out of the global config defaulter:

| Transport | Port default | Special handling |
|---|---|---|
| RESTCONF | 443 (https) / 80 (http) | TLS scheme follows `spec.tls.enabled` |
| NETCONF | 830 | Treats input port `0`, `80`, `443` as "not a NETCONF intent" → falls back to 830. Prevents SSH-on-HTTPS collision when a CR keeps a RESTCONF-shaped port but flips `spec.transport` to `netconf`. |
| gNMI | 50052 | IOS-XE 17.18+ insecure `gnxi` listen port. Older `gnmi-yang` builds on 50051/6030 must set `spec.port` explicitly. |

---

## 4. Per-transport implementation notes

### 4.1 RESTCONF — [`transport/restconf.go`](../../internal/drivers/iosxe/configdriver/transport/restconf.go)

- One HTTPS request per Op. Verb mapping: `Replace=PUT`, `Merge=PATCH-then-PUT-on-404`, `Delete=DELETE`. The PATCH→PUT fallback gives create-if-absent semantics inside a single Op.
- `SaveStartup` posts to `/restconf/operations/cisco-ia:save-config` (commit `61566dc` fixed the previously-wrong `/data/cisco-ia:save-config` path that returned 404).
- VerbCLI sends raw IOS CLI lines through the same `/restconf/operations/cisco-ia:cli-config-data` endpoint.
- Stateless: no candidate, no commit, no confirm. The `StartTransaction` / `Commit` / `Discard` methods all return `ErrUnsupported`.

### 4.2 NETCONF — [`transport/netconf.go`](../../internal/drivers/iosxe/configdriver/transport/netconf.go)

- SSH dial to `<addr>:830` over `golang.org/x/crypto/ssh`. Hello capability negotiation populates `Capabilities` from the server's advertised list.
- `Fetch` issues `<get-config>` with a subtree filter built from the path. The reply XML is converted to YANG-data-JSON via [`netconf_xml2json.go`](../../internal/drivers/iosxe/configdriver/transport/netconf_xml2json.go), then peeled to RESTCONF semantics by `peelToLastPathSegment` (§5).
- `StartTransaction` locks `<candidate/>`. `Mutate(tx="candidate", ops)` writes edit-config with `target=candidate`. `Commit` / `Discard` are standard NETCONF.
- **Implicit-tx auto-promote.** When `tx == ""` AND `caps.SupportsTransactions && !caps.SupportsWritableRunning`, `Mutate` wraps the ops in `lock(candidate)` + edit-config(s) + `commit` + `unlock`. Required because IOS-XE 17.x's `netconf-yang feature candidate-datastore` removes `:writable-running:1.0`, breaking non-transactional reconciles without this auto-promotion. CLI-only batches (VerbCLI) bypass this — Cisco-IA `cli-config-data` is a separate operations-RPC endpoint that doesn't depend on `:writable-running`.
- **Confirmed-commit.** `ConfirmedCommitter` interface emits `<commit><confirmed/><confirm-timeout>N</confirm-timeout></commit>` (CommitConfirmed) and a plain `<commit/>` then `<unlock/>` (ConfirmCommit). Timer clamped 1–600s at the transport boundary. If the engine deliberately omits `ConfirmCommit` after a Verify failure, the deferred Discard still releases the lock and the device's own timer auto-reverts.

### 4.3 gNMI — [`transport/gnmi.go`](../../internal/drivers/iosxe/configdriver/transport/gnmi.go)

- gRPC dial to `<addr>:50052`. Username/password as basic-auth metadata header per Cisco's gNMI auth scheme.
- `StartTransaction` returns a stable handle and switches `Mutate` from "send each Op as its own SetRequest" to "accumulate into the in-progress SetRequest". `Commit` flushes the accumulated SetRequest in one RPC — gNMI Set is atomic by spec.
- `Fetch` uses `GetRequest` with `JSON_IETF` encoding. Result is the raw `Update[0].val.json_ietf_val` — already in writer-friendly RESTCONF-shaped JSON.
- `pathSpecToGNMI` ([`transport/gnmi_keys.go`](../../internal/drivers/iosxe/configdriver/transport/gnmi_keys.go)) converts each writer's structured `PathSpec` to a gNMI `Path` with explicit list-key elements. **Critical for keys containing `/`** — string-path parsing splits `GigabitEthernet=0/0/0` into wrong segments; PathSpec preserves the value verbatim.
- `Subscribe` opens a stream subscription (ON_CHANGE / SAMPLE) for telemetry-driven drift detection. Wired to the per-pod kubelet's drift loop in Wave 6A.

---

## 5. Fetch wire shape — what writers actually see

After commit `88ac685`, all three transports return the same top-level JSON shape so the writers' `unwrapYANGEnvelope` + `leavesEqual` logic works unchanged regardless of `spec.transport`.

The contract: **the top-level JSON key is the last path segment**, qualified iff RESTCONF would qualify it (RFC 7951). Children inheriting their parent's namespace use bare local names; namespace-transitions are prefixed.

```jsonc
// Fetch("/Cisco-IOS-XE-native:native/banner")
// All three transports:
{
  "Cisco-IOS-XE-native:banner": {
    "motd": { "banner": "Welcome" }
  }
}

// Fetch("/Cisco-IOS-XE-native:native/interface/Loopback")
// All three transports:
{
  "Cisco-IOS-XE-native:Loopback": [
    {
      "name": "9997",
      "description": "...",
      "ip": { "address": { "primary": { "address": "10.255.255.97", "mask": "255.255.255.255" }}}
    }
  ]
}
```

What changed in `88ac685` to make NETCONF match this contract:

1. **`peelToLastPathSegment`** ([`netconf.go`](../../internal/drivers/iosxe/configdriver/transport/netconf.go)) — strips intermediate single-key wrappers from the xml→json result so the body starts at the requested resource. NETCONF `<get-config>` with a subtree filter for `/native/banner` returns the entire `<native>` subtree; xml→json wrapped that as `{"Cisco-IOS-XE-native:native": {...}}`. The peel descends through single-key wrappers until the top-level local name matches the path's last segment.

2. **RFC 7951 namespace inheritance** in [`netconf_xml2json.go`](../../internal/drivers/iosxe/configdriver/transport/netconf_xml2json.go) `decodeElement` — children inheriting the parent's XML namespace emit bare local names, not `<module>:<local>`. Cross-namespace children retain the prefix. The synthetic `<_root_>` wrapper used to give the XML decoder a single top-level element is now stripped after parsing instead of leaking out as a JSON key.

3. **`unwrapYANGEnvelope` accepts local-only keys** ([`writers/helpers.go`](../../internal/drivers/iosxe/configdriver/writers/helpers.go)) — same writer accepts both `{"Cisco-IOS-XE-native:banner": {...}}` (RESTCONF) and `{"banner": {...}}` (NETCONF after RFC 7951). The fallback path tries both forms before passing the body through verbatim.

---

## 6. Edit-config wire shape

### Singleton family (banner)

```xml
<edit-config>
  <target><candidate/></target>
  <config>
    <native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"
            xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0"
            nc:operation="merge">
      <banner>
        <motd><banner>Welcome</banner></motd>
      </banner>
    </native>
  </config>
</edit-config>
```

The `pathToSubtreeFilterWithBody` helper ([`netconf.go`](../../internal/drivers/iosxe/configdriver/transport/netconf.go)) unwraps one level of envelope when the writer's body outer key matches the last path segment's local name. Without that unwrap, the device sees `<banner><banner>...` and rejects with `unknown-element <bad-element>banner</bad-element>`. The test 06 retest in commit `d27016d` is the regression seal.

### Keyed-list family (interface_loopback)

After `88ac685`:

```xml
<edit-config>
  <target><candidate/></target>
  <config>
    <native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"
            xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0">
      <interface>
        <Loopback nc:operation="merge">
          <name>9997</name>
          <description>...</description>
          <ip>...</ip>
        </Loopback>
      </interface>
    </native>
  </config>
</edit-config>
```

Before the fix, the legacy path-only builder treated `Loopback=9997` as a literal element name and IOS-XE rejected with `<bad-element>Loopback=9997</bad-element>`. The new `opToSubtreeFilterWithBody` consumes `op.PathSpec` (already populated by `pathSpecForKeyedListEntry` and `pathSpecForInterface` in [`writers/keyed_list.go`](../../internal/drivers/iosxe/configdriver/writers/keyed_list.go)), emits each list entry's keys as inline child elements, and falls back to the path-only builder when PathSpec is empty (singleton families).

---

## 7. Implicit-tx auto-promote (NETCONF)

[`netconf.go`](../../internal/drivers/iosxe/configdriver/transport/netconf.go) `Mutate`:

```
                    ┌── tx == "candidate" ──────────────────────────────────┐
                    │   Engine drives StartTransaction/Commit/Discard.      │
                    │   Mutate writes edit-config to candidate datastore.   │
                    └──────────────────────────────────────────────────────┘
                    ┌── tx == "" AND :writable-running:1.0 advertised ─────┐
                    │   Direct edit-config to running. Legacy non-tx path. │
                    └──────────────────────────────────────────────────────┘
                    ┌── tx == "" AND device is candidate-only ─────────────┐
                    │   IMPLICIT-TX:                                       │
                    │   <lock target=candidate/>                           │
                    │   foreach op: <edit-config target=candidate/>        │
                    │   <commit/>                                          │
                    │   <unlock target=candidate/>                         │
                    │                                                      │
                    │   Engine doesn't know the device-mode shifted —      │
                    │   the non-tx path still gets atomic apply.           │
                    └──────────────────────────────────────────────────────┘
                    ┌── VerbCLI batch ─────────────────────────────────────┐
                    │   Cisco-IA `cli-config-data` RPC bypasses the        │
                    │   implicit-tx promotion entirely; it writes to       │
                    │   running through a separate operations endpoint.    │
                    └──────────────────────────────────────────────────────┘
```

Trigger condition: `hasEditConfigOp && caps.SupportsTransactions && !caps.SupportsWritableRunning`. Caught against live Cat9300 / IOS-XE 17.18.2 in commit `9e08b07` where enabling `netconf-yang feature candidate-datastore` removed `:writable-running` and broke every non-transactional reconcile with `Unsupported capability` until the auto-promote landed.

---

## 8. Wave 10 — confirmed-commit and atomic-replace

### Confirmed-commit (Wave 10.1 + 10.2)

`spec.confirmTimeoutSeconds > 0` opts a CR into the auto-revert safety net. Engine path ([`engine/engine.go`](../../internal/drivers/iosxe/configdriver/engine/engine.go)):

1. Standard candidate-side write
2. **`CommitConfirmed(timeout)`** — tentative commit; device starts an auto-revert timer
3. **runningVerify** — re-Fetch each managed family from running directly (not candidate) and verify no drift
4. If Verify succeeds: **`ConfirmCommit`** — confirm + unlock
5. If Verify fails: **deliberately do NOT call `ConfirmCommit`** — deferred Discard releases the lock; device's timer auto-reverts at `confirmTimeoutSeconds`

Fallback (a `ConfirmedCommitFallback` Warning event with explicit reason):

| Condition | Fallback reason |
|---|---|
| Transport doesn't implement `ConfirmedCommitter` (RESTCONF, gNMI) | `transport does not implement ConfirmedCommitter` |
| Device didn't advertise `:confirmed-commit:1.0` | `device did not advertise confirmed-commit:1.0` |
| `spec.transactional: false` | `non-transactional reconcile` |

Each fallback drops back to plain Commit; the operator sees the Warning event via `kubectl describe iosxeconfig`.

### Atomic replace (Wave 10.3)

`spec.atomicReplace: true` makes the resolved intent authoritative for the managed families: adds + deletes + updates land in **one** transaction with cross-family `depends_on` ordering from [`schema/families.yaml`](../../internal/drivers/iosxe/configdriver/schema/families.yaml). Partial-drift (some families applied, others fail) is the failure mode the field exists to prevent.

Transport eligibility:

| Transport | Atomic-replace supported? |
|---|---|
| NETCONF | ✅ single candidate transaction across all families |
| gNMI | ✅ single SetRequest with all updates |
| RESTCONF | ❌ no transactional surface; engine emits `ConfirmedCommitFallback` + applies non-atomically |

Wave 10.4 adds `cisco_vk_config_atomic_replace_total{outcome=...}` for SLO tracking.

---

## 9. Test coverage

### Unit (within the transport package)

| File | Scope |
|---|---|
| [`restconf_test.go`](../../internal/drivers/iosxe/configdriver/transport/restconf_test.go) | All verbs, save-startup endpoint correctness, VerbCLI |
| [`netconf_test.go`](../../internal/drivers/iosxe/configdriver/transport/netconf_test.go) | Hello / framing / edit-config / candidate lock-commit-unlock / confirmed-commit / implicit-tx auto-promote / fetch-shape (peel + RFC 7951) / PathSpec-aware edit-config / rpc-error formatting |
| [`gnmi_test.go`](../../internal/drivers/iosxe/configdriver/transport/gnmi_test.go) | Set / Get / Subscribe + transaction accumulation + flush |
| [`gnmi_keys_test.go`](../../internal/drivers/iosxe/configdriver/transport/gnmi_keys_test.go) | PathSpec → gNMI conversion (interface keys with `/`) |
| [`netconf_xml2json_test.go`](../../internal/drivers/iosxe/configdriver/transport/netconf_xml2json_test.go) | Namespace-aware XML decode, RFC 7951 inheritance |

### Live-device release-blocker tests

Catalogued under [`final/release-blocker-tests/RUNBOOK.md`](./final/release-blocker-tests/RUNBOOK.md):

| Test | Transport(s) exercised | Wave anchor |
|---|---|---|
| 01 — netconf-transactional | NETCONF | 1A-fu transactional structured |
| 02 — netconf-transactional-cli-rejection | NETCONF | 7A.1 fail-fast guard |
| 03 — configprereqs-cleanup | RESTCONF (default) | 4A-fu + 7A.2 + 7A.4 |
| 04 — gnmi-keyed-path | gNMI | 5A-fu + 7B PathSpec on the wire |
| 05 — credential-rotation-overlap | any | 6B + 7A.3 + 8.2 + 9.2 |
| 06 — driftpolicy-revert-live-write | RESTCONF + NETCONF candidate-only ✅ | drift-detect revert; closure of `88ac685` bundle |
| 07 — write-startup-save-config | RESTCONF + NETCONF candidate-only ✅ | 1A `writeStartup` plumb |
| 08 — confirmed-commit-auto-revert | NETCONF | 10.1 + 10.2 — most invasive (deliberate management-plane break) |
| 09 — atomic-replace-cross-family | NETCONF or gNMI | 10.3 |
| 10 — confirmed-commit-happy-path | NETCONF | 10.2 |
| 11 — confirmed-commit-restconf-fallback | RESTCONF | 10.2 fallback |
| 13 — atomic-replace-with-confirmed-commit | NETCONF | 10.3 + 10.2 composed |

Live-device evidence: see [`final/evidence/`](./final/evidence/) (six bundles as of 2026-04-27).

---

## 10. Recent fix-bundle change log (transport-relevant)

| SHA | Effect |
|---|---|
| `88ac685` | Four-fix bundle: peel intermediate path-segments + RFC 7951 namespace inheritance + drop `_root_` + `unwrapYANGEnvelope` accepts local-only keys + PathSpec-aware NETCONF builder. Closes test 06 + 07 over NETCONF candidate-only. |
| `d27016d` | Body-envelope unwrap in `pathToSubtreeFilterWithBody`. Strips `<banner><banner>` duplicate when envelope key matches last path segment. |
| `4d0d736` | Richer rpc-error: structured `error-info` subtree, app-tag, path. NETCONF candidate-mode tolerance via `decodeYANGList`. |
| `9e08b07` | Implicit-tx auto-promote in `Mutate` — wraps non-tx ops in lock+commit+unlock when device is candidate-only. |
| `b2c1189` | Port defaults moved from global config defaulter to `transport.For` (transport-aware). |
| `971b1e8` | Closes #6(a) NETCONF dial root-cause: `SetDeviceDefaults` was blindly setting port 443 on all transports. |
| Earlier (Wave 1A-fu) | `TxFetcher` so verify-Fetch reads candidate mid-transaction. |
| Earlier (Wave 5A-fu) | Structured `PathSpec` on every keyed-list op; gNMI Set/Delete works for keys containing `/`. |
| Earlier (Wave 10.1) | `ConfirmedCommitter` interface + NETCONF impl. |

---

## 11. Forward work

- **gNMI confirmed-commit.** Cisco hasn't shipped the equivalent over gNMI yet; when they do, the existing `ConfirmedCommitter` interface absorbs it without engine-side changes.
- **Cosmetic relocation** of `internal/drivers/iosxe/configdriver/...` → `internal/configdriver/...` (Watch-item #4) — mechanical; deferred to avoid noisy conflict with v1 CRD cut.
- **Per-family transport overrides** — the current driver picks one transport per CiscoDevice. Future hybrid mode would let banner stay on RESTCONF while interface_loopback uses gNMI; design TBD.

---

## See also

- [`./deployment-modes.md`](./deployment-modes.md) — operator-facing setup guide per mode
- [`./driver-extension-guide.md`](./driver-extension-guide.md) — how to add a new vendor driver against this Interface
- [`./final/release-blocker-tests/RUNBOOK.md`](./final/release-blocker-tests/RUNBOOK.md) — live-device retest playbook
- [`./final/evidence/2026-04-27-live-c9300-netconf-candidate-only/SUMMARY.md`](./final/evidence/2026-04-27-live-c9300-netconf-candidate-only/SUMMARY.md) — closure evidence for the `88ac685` bundle
