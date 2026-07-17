# YANG Version Support for IOS-XE Writers

## Overview

IOS-XE YANG models change across firmware versions. The NAC (Network as Code)
writers are authored against a **baseline version** (currently 17.18.2 on
C9300-24P). Earlier versions (17.15, 17.16) diverge in several ways:

- **Module prefixes** required on augmented JSON body keys
- **Different YANG list-key names** (e.g. `ordering-seq` vs `seq`)
- **Empty-leaf encoding** (`[null]` instead of boolean `true`)
- **Different container shapes** (flat ↔ nested, keyed list ↔ container)
- **Different RESTCONF paths** and envelope keys

## Architecture

### Per-family schema validation

CVK uses **goyang-extracted, per-family Go schema packages** as CI-time
validators. For each config family the generator navigates the resolved native
YANG entry tree to the terminal container or list declared in `yang_paths`,
serialises that sub-tree to a gzip-JSON blob, and embeds it as a `ySchema`
byte literal in `schema.go`. This approach avoids ygot code generation
entirely — no 125 MB generated files, no 32 GB memory peaks.

**Runtime is unchanged.** The override table, map-shaped writers, and engine
are not modified by this system. The generated packages are never imported at
runtime — they register a `cfgvalidation.FamilySchemaValidator` from their
`init()` function, and the fixture harness calls `Lookup(family, releaseTag)`
to validate every op body against the schema during `go test`.

```
BUILD / CI TIME
  families.yaml (yang_paths per family)
      │
      ▼
  computeModuleClosure() — goyang BFS over import+include graph
      │  produces minimal module set per family
      ▼
  extractFamilySchemaEntries() — ms.Process() resolves augments;
      │  navigates nativeEntry.Dir to terminal container/list
      ▼
  buildFamilyYSchema() — gzip-JSON blob of the terminal entry sub-tree
      │
      ▼
  internal/drivers/iosxe/configdriver/generated/<release>/<family>/
    schema.go    (goyang-based gzip blob, UnzipSchema function)
    register.go  (calls cfgvalidation.Register from init())

CI VALIDATOR (fixture_test.go)
  writer.Diff() → expected_ops.json op bodies
      │
      ▼
  validateOpsAgainstGeneratedSchema()
      │
      ├── validator registered  → validateJSONEntry() — structural check
      └── no validator          → silently skipped (incremental rollout)
```

**Generated packages** live under
`internal/drivers/iosxe/configdriver/generated/`. They are committed to the
repo so runtime builds do not need the YANG source bundle. CI runs the pinned
generator and verifies that the committed packages have no tracked or
untracked drift. The `make ygot-validate-gen RELEASE=<tag>` target regenerates
one release from a clean output directory.

**Schema coverage limitations.** Each family's schema is extracted from a
module closure computed by tracing import/include graphs outward from the
modules named in `yang_paths`. Modules that *augment* the native tree
(e.g. `Cisco-IOS-XE-aaa`) are not found by this traversal unless they appear
as a `Module:` prefix in the family's `yang_paths`. When augmenting modules
are absent the terminal entry's `Dir` is empty and the validator skips that
container rather than producing false positives.

**Skip policy.** A family is skipped (emits a compilable stub schema.go) when:

- its `yang_paths` resolve to no module prefixes in the YANG directory,
- the extracted schema blob exceeds 512 KB (e.g. `system` with `/native` as
  its target — the entire native tree), or
- the path is not present in the YANG model for that release.

Those are domain limitations, not general error handling. Parser,
schema-builder, filesystem, registration, and validator-index errors fail
generation immediately. The exact expected release/family/reason-code set is
stored in `schema/yang-skip-baseline.yaml`; an added skip, resolved skip, or
reason change fails generation until the baseline and regenerated artifacts
are intentionally updated together. `generated/SKIPLIST.md` is the human
summary. There are currently 35 usable validators and 19 reviewed skip stubs
per supported release.

**Validation strictness mode.** By default the validator is lenient: unknown
fields and structural type mismatches are silently skipped so fixtures that
reference deprecated YANG paths or fields from augmenting modules outside the
closure do not fail CI. Set `CVK_SCHEMA_VALIDATION=strict` to enable strict
mode, which reports these discrepancies as hard errors:

```bash
# Surface all YANG schema discrepancies in fixture data:
CVK_SCHEMA_VALIDATION=strict \
  go test ./internal/drivers/iosxe/configdriver/writers/... -run TestFixtures
```

Strict-mode failures indicate one of:
- a fixture uses a field name not present in the YANG schema for that release
  (e.g. a deprecated or augmented field — fix by updating the fixture),
- the schema closure is incomplete and the augmenting module needs to be added
  to the family's `yang_paths` (e.g. a `Module:field` prefix is missing).

The default lenient mode keeps CI green while these root causes are addressed
incrementally. See `generated/SKIPLIST.md` for the current skip list.

### Intent, Translation, Validation Boundary

CVK keeps the public `IOSXEConfig.spec.source` payload in the canonical
NetAsCode shape. That shape is intentionally release-stable: operators should
not have to change day-0 YAML just because IOS-XE moved a leaf or renamed an
augmented container between 17.18 and 26.01.

The release-specific boundary is therefore:

```text
NetAsCode intent
  -> family writer
  -> version override / body transform
  -> IOS-XE YANG JSON op
  -> validation boundary
  -> RESTCONF / NETCONF / gNMI transport
```

`internal/drivers/iosxe/configdriver/validation` owns the validation boundary.
It runs after writers emit `transport.Op` values and before the engine calls
`Apply`. The default `StructuralValidator` checks common op invariants
(path scope, JSON envelope shape, body presence) and hosts narrow release
profiles for known divergences. IOS-XE 26.01 currently validates that
`ip_domain.name` has been translated to
`name-container.name-no-vrf` before mutation.

This is the seam where generated ygot/ytypes validators belong once per-release
config model packages are available. ygot should validate the device-facing
YANG payload, not replace the public NetAsCode intent model.

Validation is deployment-policy controlled:

| Mode | Effect |
|------|--------|
| `CONFIG_YANG_VALIDATION=disabled` | default; no validation gate |
| `CONFIG_YANG_VALIDATION=warn` | log validation failures and continue |
| `CONFIG_YANG_VALIDATION=strict` | fail the family before mutation |

The Helm value is `config.yangValidationMode`. The controller propagates it to
per-device VK pods so aggregator mode and per-pod mode use the same policy.

### Stable NetAsCode Import Contract

For customer migrations from Terraform-based NetAsCode, CVK imports the
resolved NetAsCode IOS-XE model and records provenance in
`IOSXEConfig.spec.modelSource`. This keeps the public intent model stable while
the writer layer handles IOS-XE release differences.

```yaml
spec:
  modelSource:
    format: netascode-iosxe
    resolved: true
    exporter: terraform-iosxe-nac-iosxe write_model_file
```

`resolved: false` is rejected by the resolver. If a future NetAsCode release
adds fields, CVK should first accept the canonical data model shape, then
either translate the field for the target YANG release or report it as an
unsupported family/field through migration tooling and validation. The CRD
should not fork by IOS-XE release.

### Override Table (`yang_version_override_table.go`)

The **declarative override table** is the central registry of version-
conditional YANG behaviour. Each entry targets a `(family, version_range)`
and describes mutations to apply to the YANG wire representation.

```
┌──────────────────────────────────────┐
│  Override Table Entry                │
│                                      │
│  Family:              "route_map"    │
│  MinVersion:          [17, 0]        │
│  MaxVersion:          [17, 18]       │
│  ElementMap:          { old → new }  │
│  NestedYANGInnerOverride: "..."      │
│  YANGPathOverride:    "..."          │
│  EnvelopeKeyOverride: "..."          │
│  EmptyLeaves:         ["prefer"]     │
│  BodyTransform:       func(...)      │
└──────────────────────────────────────┘
```

The table is resolved once at startup via `ResolveForVersion(major, minor)`.
Writers query the resolved state at Diff/Apply time:

| Query Function           | Purpose                              |
|--------------------------|--------------------------------------|
| `IsLegacyVersion(fam)`   | Is the device on a pre-baseline version? |
| `ResolvedYANGPath(fam, default)` | Version-correct RESTCONF path |
| `ResolvedEnvelopeKey(fam, default)` | Version-correct JSON envelope |
| `ResolvedNestedYANGInner(fam, default)` | Version-correct inner list name |
| `ApplyOverrideToBody(body, override)` | Full transform chain |

### Custom Version-Branched Writers

Three families have structural YANG differences too deep for the override
table alone:

| Family              | Mechanism                                    |
|---------------------|----------------------------------------------|
| `bgp`               | Keyed list (17.16) vs container (17.18+)    |
| `prefix_list`        | Flat compound-keyed (17.16) vs nested (17.18+) |
| `ip_community_list`  | Deprecated groupings (17.16) vs community-list-entry (17.18+) |

These writers use `IsLegacyVersion()` to select their code path, and the
override table provides path/envelope selection. Transform logic is per-writer.

### YAML 1.1 Boolean Key Fix

`sigs.k8s.io/yaml` (YAML 1.1) interprets bare `no` as boolean `false`,
which becomes map key `"false"` in `map[string]any`. This is fixed globally
by `intent.FixYAML11BoolKeys()`, which runs once on the fully-merged
configuration tree at the end of `Resolver.Resolve()`.

## Adding Support for a New IOS-XE Version

### 1. Identify divergences

Compare the YANG schemas between the new version and the baseline:

```bash
# Download YANG models from the device
curl -k https://<device>/restconf/.well-known/host-meta

# Or use Cisco's published YANG repo:
# https://github.com/YangModels/yang/tree/main/vendor/cisco/xe
```

Use `pyang --tree-diff` or `yanglint` to diff specific modules:

```bash
pyang -f tree --tree-path /native/ip/prefix-list \
  Cisco-IOS-XE-native-17.16.yang \
  Cisco-IOS-XE-native-17.18.yang
```

For RESTCONF, probe the device directly:

```bash
# GET the schema for a specific path
curl -k -u admin:pass \
  'https://<device>/restconf/data/Cisco-IOS-XE-native:native/ip/prefix-list' \
  -H 'Accept: application/yang-data+json'
```

### 2. Classify the divergence

| Type | Fix Mechanism |
|------|--------------|
| Module prefix needed on element key | `ElementMap` in override table |
| Different YANG path or envelope | `YANGPathOverride` / `EnvelopeKeyOverride` |
| Boolean → empty leaf encoding | `EmptyLeaves` in override table |
| Simple container shape change | `BodyTransform` function |
| Key field rename | `KeyFieldOverride` or `ElementMap` |
| Deep structural difference | Custom writer with `IsLegacyVersion()` |

### 3. Add the table entry

Add an entry to `yang_version_override_table.go`:

```go
{
    Family:     "new_family",
    MinVersion: [2]int{17, 0},
    MaxVersion: [2]int{17, 20},  // exclusive upper bound
    ElementMap: map[string]string{
        "baseline-name": "Cisco-IOS-XE-module:version-name",
    },
},
```

### 4. Write tests

- Add transform tests to `version_transforms_test.go`
- Add override resolution tests to `yang_version_overrides_test.go`
- Add structural validation coverage for any release-specific YANG payload
  shape in `internal/drivers/iosxe/configdriver/validation`
- Run integration tests on a device of that version

### 5. Verify

```bash
# Unit tests
go test ./internal/drivers/iosxe/configdriver/writers/ -v
go test ./internal/drivers/iosxe/configdriver/validation -v

# Full build
go build ./...

# Integration tests (on lab device)
pytest tests/test_monolithic_router.py -v
```

## Reference: CiscoDevNet/terraform-provider-iosxe

The [Terraform provider for IOS-XE](https://github.com/CiscoDevNet/terraform-provider-iosxe)
is an excellent reference for correct YANG paths and data structures. It
was instrumental in discovering the 17.16 `prefix-lists` (plural) path
with the flat compound-keyed `prefixes[name, no]` list.

## Reference: Known YAML 1.1 Boolean Keys

YAML 1.1 treats these bare tokens as booleans when used as map keys:

| Token | JSON key | Canonical name |
|-------|----------|---------------|
| `no`  | `"false"` | `"no"` |
| `yes` | `"true"` | (not used in IOS-XE schema) |
| `on`  | `"true"` | (not used in IOS-XE schema) |
| `off` | `"false"` | (not used in IOS-XE schema) |

The `yaml11BoolKeyMap` in `intent/yaml11_fix.go` maps only keys that
actually appear in the IOS-XE netascode schema. Add entries there if
future schema keys collide with YAML 1.1 booleans.
