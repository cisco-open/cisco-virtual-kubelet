# How to add a new platform driver (Phase 9)

**Branch:** `pr/johalley/ciscoconfig_xe`
**Audience:** anyone bringing up an NX-OS, IOS-XR, Junos, or other
platform driver against the cisco-virtual-kubelet foundation
**Companion docs:**
[`iosxe-config-driver-review.md`](iosxe-config-driver-review.md) (design)
and [`iosxe-config-driver-appraisal.md`](iosxe-config-driver-appraisal.md)
(branch composition + quality)

The Phase-9 plug-in refactor turned the foundation into a closed
set of files that platform drivers extend without modifying. After
Phase 9, adding a new platform never edits the engine, intent
resolver, transport interfaces, aggregator, bundle controller,
audit log, replay flow, drift policies, lease arbitration, or the
operator-facing tooling.

This document is the "how to do it" walkthrough — pattern, files,
test surface, and gotchas.

---

## 1. Mental model

The foundation has two registries living in
[`internal/drivers/`](../../internal/drivers/):

| Registry | Provides | Backing factory signature |
|---|---|---|
| Apphosting (`drivers.Register`) | one `CiscoKubernetesDeviceDriver` per CiscoDevice | `Factory(ctx, *DeviceSpec) → (Driver, error)` |
| Configdriver (`drivers.RegisterConfigDriver`) | one `ConfigDriverContext` per CiscoDevice (transport + key rules + writer lookup + Subscribe paths) | `ConfigDriverFactory(ctx, *DeviceSpec, password, opts) → (*ConfigDriverContext, error)` |

A platform package wires itself into one or both registries inside
`init()`. The `cisco-vk` binary blank-imports each platform from
[`cmd/cisco-vk/drivers_register.go`](../../cmd/cisco-vk/drivers_register.go);
nothing else in the codebase needs to know the platform exists.

The split is intentional. A platform may ship apphosting first
(Pod lifecycle on the device) and configdriver later (the IOS-XE
Phase 0/1 history) or vice versa (a platform whose apphosting is
"use upstream NXAPI" but which still wants config-side
reconciliation). Two registries means two independent rollouts.

---

## 2. The shape of a platform package

```
internal/drivers/<platform>/
├── doc.go              # package doc; status (placeholder vs registered)
├── register.go         # init() Register() + RegisterConfigDriver()
├── driver.go           # NewAppHostingDriver: Pod lifecycle
├── reconciler.go       # apphosting reconciler internals (optional)
├── topology.go         # optional TopologyProvider implementation
├── client.go           # platform-side device client (RESTCONF/NETCONF/gNMI dialect)
└── configdriver/       # config-driver implementation
    ├── builder.go      # ConfigDriverContext factory + helpers
    ├── intent/         # platform-specific resolver hooks (rare)
    ├── schema/         # families.yaml + yang-versions.yaml
    ├── transport/      # platform-specific transport extensions
    │                   # (Cisco-IA RPC name, NXAPI, Junos exec)
    └── writers/        # one .go per netascode family
```

For a new platform, the existing `internal/drivers/iosxe/`
directory is the reference implementation. The four
placeholders — `nxos/`, `iosxr/`, `junos/` — show the
zero-implementation shape: just `doc.go` + an empty `register.go`.

---

## 3. Step-by-step: bringing up `<platform>`

### 3.1 Add the kind constant

Edit [`api/v1alpha1/types.go`](../../api/v1alpha1/types.go):

```go
// +kubebuilder:validation:Enum=XE;XR;NXOS;JUNOS;<NEW_KIND>;FAKE
type DeviceDriver string

const (
    ...
    DeviceDriverNEW DeviceDriver = "<NEW_KIND>"
)
```

Run `make crd-gen helm-sync-crds` to refresh the CRD's enum
validation. CiscoDevices specifying the new kind now pass the
admission validator (foundation enforces what's enumerable; the
runtime registry enforces what's actually wired).

### 3.2 Place the package skeleton

```
internal/drivers/<platform>/
  doc.go            # mirror nxos/doc.go
  register.go       # mirror nxos/register.go
```

Until `register.go` actually calls `drivers.Register`, the package
compiles but no admission-valid CiscoDevice with this kind reaches
a worker — it surfaces as a clean `driver kind "<NEW_KIND>" is not
registered (registered: ...)` error in logs. That's intentional: a
half-shipped platform stays inert, never silent-fails on the device
side.

### 3.3 Implement apphosting

Write `NewAppHostingDriver(ctx, *DeviceSpec) (*Driver, error)`. The
return type satisfies
[`drivers.CiscoKubernetesDeviceDriver`](../../internal/drivers/registry.go).
Optionally implement
[`drivers.TopologyProvider`](../../internal/drivers/registry.go) if
the device exposes CDP/OSPF/interface stats.

Reference: [`internal/drivers/iosxe/driver.go`](../../internal/drivers/iosxe/driver.go).

### 3.4 Implement the configdriver

The configdriver consumes the platform-agnostic
[`provider.ConfigReconciler`](../../internal/provider/config_reconciler.go).
The platform's job is to provide a `ConfigDriverContext` —
transport, key rules, writer lookup, Subscribe paths.

```
internal/drivers/<platform>/configdriver/
  builder.go             # KeyRulesForPlatform, LoadYANGReleaseTags,
                         # LookupWriter, UnionWriterPaths
  schema/families.yaml   # platform's family list with native + openconfig YANG paths
  schema/yang-versions.yaml
  schema/index.go        # mirror iosxe schema/index.go
  transport/             # platform-specific transport bits
                         # (Cisco-IA → NXAPI, Junos exec, etc.)
                         # Reuse iosxe RESTCONF/NETCONF/gNMI as base
  writers/               # per-family writers
    registry.go          # platform-scoped writer registry (writers.Get)
    aaa.go
    interface_ethernet.go
    ...
```

Reference: [`internal/drivers/iosxe/configdriver/`](../../internal/drivers/iosxe/configdriver/).

#### What's reusable as-is from the iosxe configdriver

- `transport.Interface`, `transport.SubscribeCapable`, `transport.PruneCapable`
  — protocol-bound, vendor-agnostic
- `transport.RESTCONF`, `transport.NETCONF`, `transport.GNMI`
  — work against any RESTCONF/NETCONF/gNMI device. Subclass them
  via composition for platform-specific RPC names (e.g. NXAPI for
  CLI push instead of `cisco-ia:cli-config-data`).
- `intent.Resolver`, `intent.MergeWithRules`, `intent.CanonicalHash`
  — pure-data composition; platform-agnostic.
- `engine.Engine`, `engine.FamilyLeaser`, `engine.RegisterMetrics`
  — entirely platform-agnostic.
- `writers.SectionWriter`, `writers.keyedListWriter`,
  `writers.nestedKeyedListWriter`, `writers.singletonWriter`
  — shape-driven helpers. Per-platform writer registries import
  the helpers from the iosxe path today (a paths-cosmetic relocation
  to `internal/configdriver/writers/` is a clean future cleanup;
  the contract is already platform-agnostic).
- The CRDs (`IOSXEConfig`, `IOSXEConfigDefaults`, …) — the spec
  shape is platform-agnostic. The naming is netascode-style
  per-platform; if the new platform wants its own CR set the
  pattern is parallel CRDs (`NXOSConfig`, …) authored alongside.

#### What you write from scratch

- The `families.yaml` for the platform's YANG model.
- One writer per family — typically a 30-line
  `keyedListWriter{...}` or `nestedKeyedListWriter{...}` instantiation.
- Transport overrides for platform-specific RPC names (CLI push,
  save-config). These are typically <50 LOC each — see
  [`internal/drivers/iosxe/configdriver/transport/restconf.go`](../../internal/drivers/iosxe/configdriver/transport/restconf.go)
  `pushCLI` for the surface.
- `KeyRulesFor<Platform>()` — the merger's path → key map.

### 3.5 Wire the registrations

Edit the new package's `register.go`:

```go
package <platform>

import (
    "context"

    "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers"
    cd "github.com/cisco/virtual-kubelet-cisco/internal/drivers/<platform>/configdriver"
)

func init() {
    drivers.Register(v1alpha1.DeviceDriverNEW,
        func(ctx context.Context, spec *v1alpha1.DeviceSpec) (drivers.CiscoKubernetesDeviceDriver, error) {
            return NewAppHostingDriver(ctx, spec)
        })

    drivers.RegisterConfigDriver(v1alpha1.DeviceDriverNEW,
        cd.BuildContext)  // returns *drivers.ConfigDriverContext
}
```

### 3.6 Blank-import in the binary

Edit [`cmd/cisco-vk/drivers_register.go`](../../cmd/cisco-vk/drivers_register.go):

```go
import (
    _ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe"
    _ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/fake"
    _ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/<platform>"  // ← new
)
```

This is the **only** edit to the foundation. Every other Phase-9
goal — engine untouched, aggregator untouched, intent resolver
untouched, audit log untouched — is preserved.

### 3.7 Test

A new platform should ship with the same test surface IOS-XE has:

- `internal/drivers/<platform>/configdriver/transport/...` —
  protocol-level fakes (httptest for RESTCONF, io.Pipe for NETCONF,
  bufconn for gNMI). The iosxe transport tests are the templates.
- `internal/drivers/<platform>/configdriver/writers/...` — per-family
  Diff/PruneDiff fixtures using the helper-test pattern from
  [`internal/drivers/iosxe/configdriver/writers/family_writers_test.go`](../../internal/drivers/iosxe/configdriver/writers/family_writers_test.go).
- An end-to-end resolver test that exercises one CR with a
  mocked dynamic client.

The platform-agnostic engine, intent, and reconciler tests **don't
need duplication** — they're already covered, and they exercise
the platform via the `Lookup` injection point on
`provider.ConfigReconciler`.

---

## 4. Cost estimate

For a credible MVP (~10 family writers + one transport override
for CLI push):

| Step | Effort |
|---|---|
| 3.1 enum constant + CRD regen | 30 min |
| 3.2 package skeleton | 30 min |
| 3.3 apphosting driver | depends on platform; ~3-5 days for a moderate implementation |
| 3.4 configdriver | ~2 weeks; most of the time in family writers |
| 3.5 + 3.6 registrations | 30 min |
| 3.7 tests | bundled per-component above |

**Total: 3-4 weeks of one engineer for an MVP**, dominated by
writers and per-platform transport quirks. Full netascode-NX-OS or
netascode-IOS-XR family parity (~50 families) tracks closer to the
IOS-XE phasing — **2-3 months for parity-equivalent depth**.

None of those weeks are spent touching foundation code.

---

## 5. The contract the foundation guarantees

Phase 9 makes these guarantees explicit:

1. **`drivers.Register` and `drivers.RegisterConfigDriver` are the
   only foundation surfaces a platform talks to.** Anything else
   imports under `internal/drivers/<platform>/...` and is private to
   that platform.

2. **Duplicate registration panics at process start** — that's a
   build-time guard against two platforms claiming the same
   `DeviceDriver` constant.

3. **Unregistered kind reads as a clear error** — never a silent
   no-op. Operators see "driver kind X is not registered (registered:
   …)" in logs and on the CR's status.

4. **`Registered()` and `ConfigDriverRegistered()` are public** —
   the aggregator and any tooling that wants to enumerate available
   platforms can do so without reaching into the registry's locks.

5. **Apphosting and configdriver registrations are independent.**
   A platform can ship apphosting first; the configdriver registry
   stays empty for that kind until it's ready, and the aggregator
   silently skips it.

6. **The placeholder packages
   ([`nxos/`](../../internal/drivers/nxos/),
   [`iosxr/`](../../internal/drivers/iosxr/),
   [`junos/`](../../internal/drivers/junos/)) are pinned by
   `placeholders_test.go`** to not register. A future PR that
   accidentally adds a `Register` call to one of them fires the
   test, so the placeholder discipline doesn't drift.

---

## 6. The minimum-foundation-edit footprint

The full set of foundation files Phase 9 expects a new platform to
edit is:

| File | Edit | Why |
|---|---|---|
| `api/v1alpha1/types.go` | 1 line in the enum + 1 const | admission-time validation |
| `cmd/cisco-vk/drivers_register.go` | 1 blank import | side-effect register |

Two lines plus an import. Every other line lives in the new
`internal/drivers/<platform>/` package. That's the Phase 9 promise.

---

## 7. Cosmetic future cleanup (out of scope here)

The `internal/drivers/iosxe/configdriver/` namespace currently
hosts the platform-agnostic engine, intent, and transport
packages — that's a historical accident from when the
configdriver was IOS-XE-only. The contracts those packages
expose are vendor-agnostic; only the path is misleading. A future
mechanical relocation to `internal/configdriver/...` (with a
sibling `internal/drivers/iosxe/configdriver/` retaining only
iosxe-specific writers + schema + transport overrides) would
remove the import-path quirk without changing semantics.

That refactor isn't necessary for adding NX-OS / IOSXR / Junos —
the contracts are already platform-agnostic — but it would clean
up the appearance. Tracked as a Phase-10 cosmetic item; doesn't
block any platform ship.
