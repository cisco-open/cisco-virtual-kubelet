# IOS-XE Configuration Driver — design review

**Branch:** `pr/johalley/ciscoconfig_xe`
**Against:** `main`
**Status:** functional end-to-end for RESTCONF; NETCONF/gNMI reserved
**Reviewer context:** familiarity with
[netascode](https://netascode.cisco.com/docs/data_models/iosxe/overview/),
the `netascode/terraform-iosxe-nac-iosxe` Terraform module, and the
`nac-validate` / `nac-collect` / Yamale tooling is assumed.

The question this branch answers: can `cisco-virtual-kubelet` own IOS-XE
declarative configuration (what netascode models today) in the same
per-device Virtual-Kubelet process that already owns apphosting,
without introducing Terraform, a second control plane, or a second
source of truth? This document is an honest writeup for someone who
knows netascode deeply — so the focus is on what's the same, what's
deliberately different, and what's deferred.

---

## 1. One-line summary

The branch adds a per-device Kubernetes-native config driver that
consumes netascode-shaped YAML and reconciles it against IOS-XE over
RESTCONF. Scope semantics (`defaults`, `device_groups`,
`interface_groups`, `templates`, per-device) match netascode one-to-one;
the Terraform runtime is gone; Kubernetes CRDs + informers are the
control surface.

## 2. Numbers

- 24 commits on the branch (see §13 for the commit progression).
- 206 files changed, ~18.9k insertions.
- 54 netascode families with real writers; every entry on the netascode
  IOS-XE portal has a CVK writer.
- 5 scope CRDs (IOSXEConfig, IOSXEConfigDefaults, IOSXEDeviceGroupConfig,
  IOSXEInterfaceGroupConfig, IOSXETemplate).
- 4 new tools: `cisco-vk-config-lint`, `cisco-vk-config-collect`,
  `cisco-vk-config-docs`, `cisco-vk-yang-sync`.
- `go build ./...`, `go vet ./...`, `go test ./... -count=1`, and
  `make generate` all green on the tip.

## 3. Why this exists

Today an operator running CVK for IOx apphosting also runs a second
pipeline — almost always the
`netascode/terraform-iosxe-nac-iosxe` module — to own the network
configuration those apps depend on (the `VirtualPortGroup`, the DHCP
pool, the inbound ACL). That gives:

- two source-of-truth systems: Kubernetes etcd for pods, Terraform
  state for config;
- two review workflows, two credential paths, two drift loops, two
  sets of operational tooling;
- no natural cross-pillar ordering (an apphosting change can't gate
  on a config change and vice-versa);
- two control-plane surfaces to secure and audit.

The branch's bet: every aspect of a single device is owned by one
per-device `cisco-vk run` process. Node administration, container
deployment, configuration, and observability become four pillars on
one Virtual-Kubelet provider. Git stays the desired-state store;
Kubernetes is the control plane; the device doesn't see two tools.

This is an explicit architectural choice, not a replacement for
netascode's data model. The YAML you write is still netascode YAML —
you paste it into a `ConfigMap` instead of feeding it to Terraform.

## 4. Architecture

```
           Git (source of truth, unchanged from netascode repos)
                              │ push / PR → CI
                              ▼
              ┌────────────────────────────────────────┐
              │ cisco-vk-config-lint  (pre-commit / CI)│
              │   — per-family shape validation        │
              │   — managed-leaf check from writers    │
              │   — nac-validate equivalent            │
              └────────────────────┬───────────────────┘
                              Flux / ArgoCD
                              ▼
                       Kubernetes API server
                              │
    ┌─────────────────────────┼───────────────────────────────┐
    │                         │                               │
    │     CiscoDevice         │     IOSXEConfig et al.        │
    │     watched by          │     consumed by               │
    ▼                         ▼                               ▼
┌────────────────┐   ┌─────────────────────────────────────────────┐
│ cisco-vk       │   │ cisco-vk run  (one pod per CiscoDevice)     │
│  manager       │──►│                                             │
│                │   │  ┌─────────────────────────────────────┐    │
│ reconciles     │   │  │ node administration    (existing)   │    │
│ CiscoDevice    │   │  ├─────────────────────────────────────┤    │
│ only;          │   │  │ IoxDriver              (existing)   │    │
│ spawns VK pod; │   │  ├─────────────────────────────────────┤    │
│ owns the       │   │  │ ConfigReconciler       (new)        │    │
│ per-device     │   │  │   informer-backed, ctrl-runtime     │    │
│ deployment.    │   │  │   → intent.Resolver                 │    │
│                │   │  │   → engine.Engine                   │    │
│                │   │  │   → writers.*  (54 families)        │    │
│                │   │  ├─────────────────────────────────────┤    │
│                │   │  │ topology + /metrics    (existing+)  │    │
│                │   │  └──────────────┬──────────────────────┘    │
│                │   │    shared HTTP client + SessionLock         │
│                │   │    with the IoxDriver (single TLS session)  │
└────────────────┘   └─────────────────┬───────────────────────────┘
                                       │
                                  RESTCONF (YANG 1791)
                                       ▼
                                  IOS-XE device
```

Key architectural moves relative to netascode:

1. **One process per device owns all four pillars.** No external
   coordinator.
2. **Scope CRDs** mirror the netascode scope tree (below) — not a
   rewrite of the data model.
3. **Controller-runtime Reconciler + informers** replace the
   Terraform apply cycle. Every relevant Kubernetes object change
   triggers a targeted reconcile.
4. **Drift is continuous**, not plan-apply-forget. Three declared
   policies (revert/report/pause) give operators cutover control.

## 5. Feature parity against netascode

Exhaustive matrix. "Parity" means structural equivalence, not line-for-line
identical internals.

| netascode concept | CVK equivalent | status |
|---|---|---|
| `iosxe.defaults` | `IOSXEConfigDefaults` (cluster-scoped) | ✅ |
| `iosxe.device_groups[]` | `IOSXEDeviceGroupConfig` | ✅ |
| `iosxe.interface_groups[]` | `IOSXEInterfaceGroupConfig` | ✅ |
| `iosxe.templates[]` | `IOSXETemplate` (+ parameter typing) | ✅ |
| `iosxe.devices[]` | `IOSXEConfig` (per-device) | ✅ |
| precedence: defaults → groups → templates → devices | `intent.Resolver` — defaults → device groups → interface groups → templates → per-device | ✅ (with interface_groups slotted between device groups and templates) |
| rightmost-wins on scalars | `intent.Merge` | ✅ |
| keyed-list merging by name/id | `intent.MergeWithRules` + `KeyRules` from `families.yaml` | ✅ |
| scalar-list replacement | default merger behaviour | ✅ |
| mixed-object-list fallback | treated as opaque (netascode has no defined union) | ✅ |
| YAML parsing | `sigs.k8s.io/yaml` (superset of JSON) | ✅ |
| envelope vs fragment YAML | both accepted (`iosxe.devices[].configuration` extracted for target device) | ✅ |
| family index | `schema/families.yaml` (hand-maintained, 54 entries) | ✅ |
| YANG release pin | `schema/yang-versions.yaml` (17.9.1 = release 1791) | ✅ |
| `nac-validate` | `cisco-vk-config-lint` | ✅ (see §9) |
| `nac-collect` | `cisco-vk-config-collect` | ✅ |
| portal-generated family docs | `cisco-vk-config-docs` | ✅ |
| YANG → ygot Go types | `cisco-vk-yang-sync --yang-dir` | 🟡 wiring present; YANG tree not checked in |
| Terraform-style transactional apply | absent (RESTCONF) | 🟡 deferred to NETCONF in a later phase |
| NETCONF candidate datastore | reserved, factory fails fast | ⏳ deferred |
| gNMI | reserved, factory fails fast | ⏳ deferred |
| RESTCONF transport | `transport.RESTCONF` | ✅ |
| device credentials flow | Kubernetes `Secret` via `CredentialSecretRef` | ✅ |
| SOPS for Git-committed secrets | documented in `examples/gitops-reference/` (not new, netascode has same story) | ✅ |
| per-family lease / contention model | `coordination.k8s.io/v1.Lease` per `(device, family)` | ✅ new — netascode has no direct equivalent |
| drift policy | `IOSXEConfig.spec.driftPolicy: revert\|report\|pause` | ✅ new — netascode drift is scheduled plan-apply |
| canonical-hash short-circuit | `intent.CanonicalHash` in `ConfigReconciler` | ✅ new — saves Fetch/Diff on quiescent CRs |
| runtime | Kubernetes controller | different — Terraform replaced |
| state store | Kubernetes etcd + device | different — no Terraform state |
| brownfield onboarding | `cisco-vk-config-collect` → netascode YAML | ✅ mirrors `nac-collect` |

Two things worth flagging up-front:

- The scope CRD set is a **superset** of netascode's. We added
  `IOSXEInterfaceGroupConfig` as an explicit Kubernetes object because
  netascode's `interface_groups[]` is a first-class layer, not a sub-
  case of `device_groups[]` — worth a reviewer's sanity check on our
  precedence ordering.
- The lease model is additive. netascode solves "two repos touching
  the same device" at the review/process layer (git conflicts); we
  solve it at the reconcile layer (per-family coordination.k8s.io
  Lease). Reviewer input welcome on whether Kubernetes-style leases
  are overkill or just-right here.

## 6. Scope precedence — the exact merge order

```
IOSXEConfigDefaults          (cluster, name-sorted — determinism matters here)
  → IOSXEDeviceGroupConfig   (order: CR's spec.deviceGroups list)
  → IOSXEInterfaceGroupConfig (order: CR's spec.interfaceGroups list)
  → IOSXETemplate            (order: CR's spec.templateRefs list; expanded first)
  → IOSXEConfig source       (inline or ConfigMap-ref; the per-device YAML)
```

Rightmost wins on scalars; keyed lists merge by their declared
`keyField` (from `families.yaml`) with the netascode name>id>sequence>type
fallback heuristic for unknown paths.

**Where we deviated — please audit this:** netascode canonically
orders `defaults → device_groups → templates → devices`. We slot
`interface_groups` **between device groups and templates**. The
reasoning: an interface group's configuration is more specific than a
device group's (it picks a named interface on a matched device) but
less specific than a template's parameterised values (which are
chosen per-use-site). If netascode has a canonical answer on where
`interface_groups` slots, we'd like to match it.

## 7. Family coverage

All 54 families have writers. The set is pinned in three tests so the
index and the writers can't drift apart:

- `internal/drivers/iosxe/configdriver/schema/index_test.go` —
  families.yaml consistency check.
- `internal/drivers/iosxe/configdriver/writers/registry_test.go` —
  registered-writer set.
- `internal/drivers/iosxe/configdriver/writers/schema_test.go` —
  every non-skeleton writer has an extractable `FamilySchema`.

### Phase-1 (8 families — apphosting prerequisites + baseline)

`system`, `vlan`, `vrf`, `interface_ethernet`, `interface_loopback`,
`interface_virtual_port_group`, `dhcp`, `access_list_extended`.

### Phase-2 (15 families — routing and common services)

`access_list_standard`, `aaa`, `banner`, `bgp`, `cdp`,
`interface_switchport`, `line`, `lldp`, `logging`, `ntp`, `ospf`,
`prefix_list`, `route_map`, `snmp_server`, `static_route`.

### Phase-3 (31 families — everything else on the portal)

Management plane: `username`, `clock`, `ip_http`, `ip_ssh`,
`ip_domain`, `ip_name_server`, `tacacs_server`, `radius_server`.
Security: `ipv6_access_list_standard`, `ipv6_access_list_extended`,
`ipv6_prefix_list`, `ip_community_list`, `ip_as_path_access_list`.
Crypto: `crypto_pki_trustpoint`, `crypto_ikev2_profile`,
`crypto_ipsec_transform_set`, `crypto_ipsec_profile`, `crypto_map`.
Interfaces: `interface_vlan`, `interface_port_channel`,
`interface_tunnel`. Routing: `eigrp`, `isis`. QoS: `class_map`,
`policy_map`. NAT: `ip_nat_inside_source`, `ip_nat_pool`.
Tracking/EEM: `track`, `event_manager`. L2 globals:
`spanning_tree`, `errdisable`.

### Depth caveats (important to know before reviewing)

Each writer declares a `managedLeaves` set — the subset of the YANG
container the writer reads/writes. The additive-merge semantics mean
leaves outside this set on the device are preserved. This is the
single biggest source of "it doesn't do what netascode does" surprise.

- **BGP, OSPF, EIGRP, IS-IS:** narrow managed-leaf sets on the
  top-level container; per-neighbor / per-address-family / per-area
  diffing is still opaque (the whole child list is a managed leaf).
  netascode's Terraform provider models these deeper.
- **ACLs:** rule lists are opaque managed leaves; per-sequence
  diffing keyed by `sequence` is deferred to Phase-4.
- **QoS (class-map, policy-map):** class/match/action structure is
  opaque.
- **Crypto families:** profiles and maps model the leaf set that is
  safe-to-ship-without-secrets; PSK/key round-tripping is
  deliberately not attempted.

The reviewer's gut-check: is "narrow managedLeaves + additive merge"
the right default, or should CVK promise full-container ownership
(and therefore leaf deletion) the way netascode effectively does?
The trade-off is brownfield safety vs declarative completeness.

## 8. Design decisions — with rationale

### 8.1 Kubernetes-native over Terraform

No state file. Kubernetes etcd holds current state; Git holds desired
state; the device is the side-effect. An IOSXEConfig CR is the
"row" — the CR's `status` subresource is the "state". GitOps tools
see one coherent control plane.

Consequences a netascode reviewer will notice:
- **No `terraform plan`.** Preview is achieved via `driftPolicy:
  report` (read-only diff rendered on the CR's status) + `flux diff`
  / `argocd app diff` (git-level).
- **No `terraform destroy`.** Deleting an IOSXEConfig removes it from
  the active scope; if `spec.pruneOnRelinquish: true` (Phase-1
  field, Phase-4 behaviour), the writer also deletes from the device.
  Default is "leave in place" to match operator intuition during
  migration.
- **Continuous reconcile, not scheduled.** A drift check is not a
  cron; every CR ticks with the hash short-circuit, re-fetching at
  `spec.driftDetectInterval` (default 5m).

### 8.2 One process per device

The whole branch leans on this. Apphosting + config share the HTTP
client, TLS session, credential, cancel context, logger, and
Prometheus registry. A misbehaving driver can at worst take down
its own device's pod; it cannot affect any other device.

netascode's Terraform provider is a single process touching N
devices; failures fan out. Reviewer question: is per-device
isolation worth the pod-per-device overhead? (Current answer: yes —
it matches the apphosting runtime and gives a clean blast radius.)

### 8.3 Additive-merge semantics

`managedLeaves` is a closed set per writer. A leaf on the device not
in that set is not touched. A leaf in the intent not in `managedLeaves`
is silently dropped. Rationale:
- brownfield onboarding doesn't require the operator to declare
  every leaf they inherited;
- three CRs managing different subsets of a VRF don't fight;
- the writer set can grow one leaf at a time without a breaking
  change.

Cost: a CR can "declare" a leaf that is silently ignored. Mitigation:
`cisco-vk-config-lint` emits per-family advisories when unknown
leaves are present. Reviewer question: should the lint promote these
to errors in `--strict` mode?

### 8.4 Three drift policies (revert / report / pause)

Not an invention — it's the netascode pattern expressed as first-
class CR fields. `report` is the parallel-run mode during cutover
from a Terraform-driven setup: CVK fetches + diffs but never writes.
`pause` is break-glass for live CLI troubleshooting. `revert` is the
steady-state.

Reviewer input welcome: do real netascode operators do cutover with
a read-only mode, or is the cleaner operational story "freeze
Terraform, copy YAML, apply"? If the latter, `report` may be under-
used in practice.

### 8.5 Lease-based per-family arbitration

`coordination.k8s.io/v1.Lease` with key `cvk-<device>-<family>`, TTL
2× reconcile interval. Before applying a family, the reconciler
acquires the lease with `<namespace>/<CR>` as holder identity. Losers
are dropped from ManagedFamilies for the tick and surfaced as
`Skipped` in the family status.

Why Lease and not an HTLM lock / annotation: the Lease primitive is
already maintained for node heartbeat, it's API-server-backed
(survives controller restarts), and it has built-in expiration.

Reviewer question: is this overkill? netascode addresses "two repos
touching the same device" via Git review rather than runtime
coordination. Counter-argument: CI-level coordination is fragile when
ops pipelines multiply.

### 8.6 Canonical-hash short-circuit

`intent.CanonicalHash(ResolvedIntent)` — SHA-256 over a map-key-
sorted JSON serialisation of the semantic fields (excluding the CR's
metadata.generation / resourceVersion so a no-op re-list doesn't
invalidate the cache).

Effect: in steady state, a reconcile tick does zero device I/O. The
reconciler still runs on every informer event, but exits the hot
path immediately when the hash + generation + phase all match.

Reviewer question: does netascode have an equivalent, or does
Terraform's plan compute avoid the problem structurally?

### 8.7 Transport abstraction — even with only RESTCONF today

`transport.Interface` has `Capabilities()`, `StartTransaction` /
`Commit` / `Discard`, `SaveStartup`, and a small `{Verb, Path, Body}`
mutation vocabulary. RESTCONF implements it; NETCONF and gNMI
return `ErrUnsupported` at the factory.

Why design the interface before implementing NETCONF: the writers are
transport-agnostic as a direct consequence. When NETCONF lands, a
single adapter change promotes every writer; no writer refactor
needed. Reviewer input on whether the `Verb: Replace/Merge/Delete`
vocabulary covers what netascode's NETCONF code actually does.

### 8.8 Resolver split into three layers

- `intent.Resolver` composes scope CRs into a `ResolvedIntent` —
  pure Go, no device.
- `engine.Engine` runs the state machine (Validating → Planning →
  Applying → Verifying → InSync/Drifted/Failed/Paused) against the
  intent — has device I/O but no Kubernetes I/O.
- `ConfigReconciler` is the controller-runtime boundary — watches,
  queues, status writes.

Each layer is independently testable with a fake for the next
(controller-runtime fake client → resolver; fake transport →
engine; fake writer lookup → engine). Test count is inflated but
catches regressions early.

Reviewer question: is three layers too many? Would two have worked?

### 8.9 `ManagedFamilies` as an explicit gate

A CR whose inline body declares `vrf:` but whose
`spec.managedFamilies` doesn't include `vrf` will NOT apply the VRFs.
Intentional. It lets operators adopt families one at a time; it
makes CVK's footprint explicit at the CR level; it's the unit of
lease arbitration.

Downside a netascode user will feel: in netascode, the presence of
a family in the YAML is the gate. CVK requires a second declaration.
`cisco-vk-config-lint` could warn on mismatch; currently it does not
(deliberate, so dev-loop noise stays low). Reviewer opinion
requested.

### 8.10 `CiscoDevice.spec.configPrereqs` — apphosting's owned IOSXEConfig

When `spec.configPrereqs` is set on a `CiscoDevice`, the controller
auto-creates and reconciles a `<device>-prereqs` IOSXEConfig CR
with `managedFamilies` pinned to `[interface_virtual_port_group,
dhcp, access_list_extended]`. Owner-ref is the `CiscoDevice`.
Deleting the device garbage-collects the CR; the config driver
reverts those families on the device before the apphosting pod is
torn down.

This is the one concession to "apphosting and config must
coordinate" that needed to be inline in the controller rather than
a separate concern. Reviewer opinion: is this the right abstraction,
or should it be a separate scope layer?

### 8.11 YAML canonicalization

The merger preserves list order (semantically significant for ACLs,
sequence-keyed entries) and is indifferent to map key order (YAML-
authoring flexibility). netascode's `nac-validate` similarly ignores
map ordering. Worth cross-checking.

## 9. Tooling comparison

| concern | netascode | CVK |
|---|---|---|
| data model | netascode YAML | **same** (byte-for-byte via ConfigMap) |
| schema file | `.schema.yaml` (Yamale) | `families.yaml` + `writers.FamilySchema` |
| offline validator | `nac-validate` | `cisco-vk-config-lint` |
| brownfield collector | `nac-collect` | `cisco-vk-config-collect` |
| reference docs | portal (generated from schema) | `cisco-vk-config-docs` (md reference) |
| Go types from YANG | ygot (via module generators) | `cisco-vk-yang-sync --yang-dir` (ygot pipeline) |
| CI hook | pre-commit nac-validate | pre-commit `cisco-vk-config-lint` |
| pipeline | Terraform plan/apply | Flux/ArgoCD → controller reconcile |

### What `cisco-vk-config-lint` does today and doesn't

Does:
- YAML parses and splits multi-document.
- `kind` is recognised (IOSXEConfig family).
- `apiVersion` matches.
- `managedFamilies` is non-empty and every family is in
  `families.yaml`.
- `spec.source` has exactly one of inline / configMapRef.
- `driftPolicy` value is closed-enum.
- For keyed-list families in an inline body: expected inner key
  present, each entry has the declared key field.

Does not (yet):
- Yamale-style per-leaf type validation (e.g. "VLAN id is 1-4094").
  This is the netascode reviewer's most likely callout.
- Unknown-leaf warnings.
- Cross-CR family overlap detection at lint time (the engine does
  it at runtime via Lease).

### What `cisco-vk-config-collect` does

Thin mirror of `nac-collect`: invokes every selected family's
`Fetch` method over RESTCONF, reshapes the result back into
netascode envelope shape (`iosxe.devices[].configuration`), emits
YAML. Useful for brownfield onboarding; output is directly usable
as a `ConfigMap.data[key]` value.

### What `cisco-vk-config-docs` does

Reads `families.yaml` and `writers.Schema`, renders one markdown
page per family plus an index. Not a replacement for the netascode
portal; the point is that CVK-specific reference (managed leaves,
implementation status, YANG paths) stays in lock-step with the
driver because both read the same sources.

## 10. What's deliberately deferred

1. **NETCONF transport.** The transport interface carries
   transaction verbs for this. Adding NETCONF:
   - new dependency (likely `scrapli/scrapligo` or
     `damianoneill/net`);
   - candidate-datastore + edit-config XML; maps
     `VerbReplace/VerbMerge/VerbDelete` to the `operation` attribute;
   - honours `spec.transactional: true` (today a no-op);
   - enables cross-family atomic apply.
   Rough estimate: ~2 weeks plus lab integration.

2. **gNMI transport.** Larger shape mismatch — `gnmi.TypedValue`
   protobuf, `gnmi.Path` vs xpath strings, per-family path dialects
   in `families.yaml`. Rough estimate: ~3 weeks.

3. **YANG tree check-in.** `cisco-vk-yang-sync --yang-dir` drives
   ygot when given a directory, but the full Cisco-IOS-XE YANG
   module tree (thousands of files, licensed Cisco IP) is not
   checked in. The Makefile's existing `ygot-gen` target covers
   apphosting YANG; extending it for the config-driver set is a
   separate licensing + CI decision.

4. **Per-rule diffing inside list leaves.** ACLs, prefix-lists,
   route-maps, OSPF network/redistribute lists, BGP neighbor/AF
   lists. Current: entire nested list is an opaque managed leaf.
   Phase-4: keyed by `sequence` / name / address.

5. **Credentials round-trip.** `cisco-vk-config-collect` does not
   attempt to exfiltrate PSKs, enable secrets, or TACACS/RADIUS
   keys into the emitted YAML — same policy as `nac-collect`.

6. **Yamale-style per-family schema.** Not written. The
   `FamilySchema` registry exposes managed-leaf names but no type
   information. A Yamale schema per family would be ~300 lines and
   is tractable — flagged as a review decision.

## 11. Concrete operator workflow today

1. **Day 0.** Install the CVK Helm chart. CRDs land. Controller
   manager boots. No devices yet.
2. **Day 1.** For each device:
   - `kubectl apply` a `Secret` (SOPS-encrypted if Git-managed).
   - `kubectl apply` a `CiscoDevice` CR — controller spawns
     `cisco-vk run` pod.
   - `kubectl apply` a `ConfigMap` with the device's existing
     netascode YAML (copy from the NaC repo).
   - `kubectl apply` an `IOSXEConfig` CR with
     `driftPolicy: report` during cutover.
3. **Day 1.5.** Parallel-run: CVK is read-only, Terraform keeps
   writing. Confirm `status.phase: Drifted` stays empty across
   multiple ticks.
4. **Day 2.** Flip `driftPolicy: revert`. Decommission the
   Terraform pipeline.
5. **Day N.** Edit the `ConfigMap` (or let Flux/ArgoCD do it).
   The informer triggers a targeted reconcile; status updates;
   events fire. No drift cron, no plan+apply cycle.

A full example is at `examples/gitops-reference/`; it is directly
`kubectl apply -k .`-able.

## 12. Open questions for the reviewer

Concrete items where netascode expertise would change the answer:

1. **Scope precedence.** We slot `interface_groups` between device
   groups and templates. Is that the canonical netascode ordering?
2. **Additive-merge default.** Is "writer owns a narrow leaf set;
   unmanaged leaves preserved" a reasonable default for the netascode
   community, or does it surprise? Should writers own full
   containers by default (and therefore delete unmanaged leaves)?
3. **ManagedFamilies gate.** netascode uses "presence in YAML" as
   the gate. CVK requires an explicit list. Is the explicitness
   worth the friction?
4. **Drift policies.** Is `report` an operationally useful mode
   or cutover theatre? Do real netascode cutovers use anything
   analogous?
5. **Lease-based arbitration.** Runtime lease vs Git-review
   arbitration — which is actually safer at scale?
6. **Lint strictness.** Should unknown inline leaves be errors
   (with `--strict`) or warnings? netascode's `nac-validate` is
   strict.
7. **Schema depth.** Should we ship a Yamale-equivalent per-family
   schema now, or wait for operator requests? The machinery to
   consume it exists.
8. **Crypto/secrets handling.** Current: additive merge, no
   round-trip of secrets in `cisco-vk-config-collect`. Does this
   match netascode's posture?
9. **Per-rule diffing.** Deferred to Phase-4. Is this a day-one
   requirement for the netascode operator or an acceptable phase-2
   follow-up?
10. **YANG source-of-truth story.** We hand-maintain
    `managedLeaves`; netascode-the-portal is YANG-generated. With
    `cisco-vk-yang-sync --yang-dir` we have the pipeline; without
    the YANG tree checked in, we have hand-maintained metadata. Is
    the licensing / repo-size cost of checking YANG in worth the
    automation benefit?

## 13. File-tree diff

New top-level directories:

```
api/config/v1alpha1/                           — new CRD group (5 CRDs)
internal/drivers/iosxe/configdriver/
  ├── doc.go driver.go stub.go types.go        — Driver interface + stub
  ├── engine/                                   — reconcile state machine + Lease
  ├── intent/                                   — resolver + merger + hash
  ├── schema/                                   — families.yaml + yang-versions.yaml
  ├── transport/                                — capability-aware transport + RESTCONF
  └── writers/                                  — 54 family writers
internal/provider/
  ├── config_reconciler.go                     — polling entrypoint (test surface)
  └── config_reconciler_controller.go          — ctrl-runtime Reconcile + SetupWithManager
tools/cisco-vk-config-lint/                    — offline validator
tools/cisco-vk-config-collect/                 — nac-collect equivalent
tools/cisco-vk-config-docs/                    — per-family markdown reference generator
tools/cisco-vk-yang-sync/                      — writer skeleton + ygot driver
docs/reference/families/                       — generated reference tree (55 pages)
examples/gitops-reference/                     — runnable end-to-end example
```

Existing code touched:

```
api/v1alpha1/types.go                          — +spec.transport, +spec.configPrereqs
charts/cisco-virtual-kubelet/templates/        — RBAC extended for config.cisco.vk
charts/cisco-virtual-kubelet/crds/             — 5 new CRDs copied in by helm-sync-crds
cmd/cisco-vk/manager.go                        — scheme registers configv1alpha1
cmd/cisco-vk/run.go                            — startConfigReconciler invoked
cmd/cisco-vk/config_reconciler.go              — new, manager+recorder wiring
internal/controller/ciscodevice_controller.go  — owned IOSXEConfig for configPrereqs
README.md                                       — Phase-0 section added
CHANGELOG.md                                    — n/a (repo had none)
```

## 14. Commit progression (chronological)

| commit | summary |
|---|---|
| `61c20d6` | Phase-0 CRD scaffold: IOSXEConfig + 3 scope CRDs |
| `d76c978` | ConfigDriver interface + stub + SectionWriter registry |
| `9de6263` | Phase-0 polling reconciler wired into cisco-vk run |
| `9111bec` | families.yaml + yang-versions.yaml |
| `decce41` | cisco-vk-yang-sync Phase-0 stub |
| `2e2546e` | GitOps reference example |
| `3b94790` | Helm chart CRD sync + VK pod RBAC |
| `2ac29c0` | README Phase-0 section |
| `a50610c` | intent resolver (scope merge, source loader, template expander, hash) |
| `4871592` | capability-aware transport + RESTCONF + factory |
| `0d20431` | engine reconcile state machine + VLAN writer + drift policies |
| `3b544e2` | Phase-2 families.yaml entries |
| `8b13695` | 7 remaining Phase-1 family writers |
| `5393d30` | coordination.k8s.io Lease per-family arbitration |
| `01651c1` | Prometheus metrics + Kubernetes events |
| `246234e` | cisco-vk-config-lint Phase-1 |
| `15e5dde` | CiscoDevice.spec.configPrereqs + owned IOSXEConfig |
| `12d6e36` | 15 Phase-2 family writers |
| `8b7c1e3` | yang-sync emits skeletons from families.yaml |
| `5f5b4ad` | informer-backed ctrl-runtime Reconciler |
| `14a44ff` | 31 Phase-3 family writers (full family set) |
| `a2e80d5` | IOSXEInterfaceGroupConfig (netascode interface_groups parity) |
| `88bd7dc` | yang-sync invokes ygot when --yang-dir supplied |
| `7285c27` | managed-leaf registry + per-family lint + collect + docs generator |

## 15. What would change the verdict

Signals that would make us reconsider the architecture:

- **netascode has a canonical scope precedence for `interface_groups`
  that differs from ours.** → reorder the resolver.
- **Real netascode operators strongly prefer full-container
  ownership over additive merge.** → flip the default and introduce
  a `spec.preserveUnmanaged` opt-out.
- **The narrow managed-leaf sets on BGP/OSPF/ACLs will not hold
  through first real adoption.** → accelerate per-rule diffing into
  Phase-2 rather than Phase-4.
- **Terraform-style transactional semantics are load-bearing in
  practice and operators do not tolerate best-effort apply.** →
  prioritise NETCONF ahead of Phase-2 family polish.
- **The Kubernetes control plane is itself a regression for
  operators who like Terraform's state-file auditability.** →
  consider a tfstate-style event log on the IOSXEConfig CR's status
  or a separate CR for apply history.

Signals that would affirm it:

- **Cross-pillar ordering (apphosting ↔ config) is valuable in
  real deployments.** → `configPrereqs` and the single-process model
  pay off.
- **Drift reporting from a live controller beats scheduled `terraform
  plan`.** → `driftPolicy: report` and the continuous reconcile are
  a real improvement.
- **GitOps teams prefer `flux diff` as the preview surface.** → the
  Kubernetes-native control surface integrates cleanly with their
  existing tooling; no Terraform pipeline to maintain alongside.
- **Per-device isolation (pod-per-device) is a credible operational
  story at scale.** → the process model aligns with VK's existing
  apphosting approach.

---

*This document lives alongside the code. If a commit changes the
scope model, the resolver precedence, or the family set, expect
this file to be updated in the same change.*
