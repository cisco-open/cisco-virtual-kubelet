# IOS-XE Configuration Driver — review feedback & action plan

**Branch:** `pr/johalley/ciscoconfig_xe`
**Date:** 2026-04-24
**Status:** feedback received, actions pending

This document captures the reviewer's feedback on the
[design review RFC](iosxe-config-driver-review.md) and the agreed
action plan for each item. Items are numbered for cross-referencing in
PRs and commit messages (e.g. "Addresses feedback-2").

---

## Feedback 1 — Drop `cisco-vk-config-collect`

**Reviewer position:** Brownfield conversion is a one-time activity.
After the initial onboard the NAC data model is the sole source of
truth. There is no ongoing need for a VK-side `nac-collect` equivalent;
operators should use the existing `nac-collect` tool directly.

**Analysis:** The current `tools/cisco-vk-config-collect/` is a faithful
port of `nac-collect` semantics — it calls every writer's `Fetch`, maps
back to netascode shape, and emits YAML. Since onboarding is one-shot
per device, maintaining a parallel tool in the VK repo adds code surface
with no ongoing runtime value. The output YAML shape is already
identical, so `nac-collect` output is directly usable as
`IOSXEConfig.spec.source.inline` or as a ConfigMap.

### Action

| Step | Description | Priority |
|------|-------------|----------|
| 1a | Remove `tools/cisco-vk-config-collect/` from the branch. | Immediate |
| 1b | Remove the committed binary `cisco-vk-config-collect` from tracking and add it to `.gitignore`. | Immediate |
| 1c | Update the RFC (§5, §9) to reference `nac-collect` as the brownfield onboarding path, noting that its output is directly consumable by IOSXEConfig CRs. | Immediate |

---

## Feedback 2 — Refocus `cisco-vk-config-lint` on drift detection

**Reviewer position:** Do not try to achieve feature parity with
`nac-validate` for static schema validation. Instead, focus
`cisco-vk-config-lint` on detecting configuration drift — both objects
defined in the data model that have drifted on the device, and
objects/config existing on the device that are not defined in the model.

**Analysis:** The current `tools/cisco-vk-config-lint/` is an offline
static validator (YAML parse, kind recognition, family-set check,
per-family semantic rules). This substantially overlaps `nac-validate`.

The reviewer wants two drift dimensions:

1. **Model → device drift:** families/leaves declared in the IOSXEConfig
   whose device state has diverged from the declared intent.
2. **Device → model gaps:** config present on the device that no
   IOSXEConfig CR claims (unmanaged config detection).

Dimension 1 is partially covered by the engine's Fetch → Diff cycle and
`driftPolicy: report` status output. Dimension 2 (orphan detection) is
genuinely new — the engine only operates on `ManagedFamilies`, so device
config outside those families is invisible today.

### Action

| Step | Description | Priority | Status |
|------|-------------|----------|--------|
| 2a | Strip the static schema validation from `cisco-vk-config-lint` (point operators to `nac-validate` for that). | Next iteration | ✅ Shipped. `tools/cisco-vk-config-lint/main.go` rewritten; old `lintIOSXEConfig` / `lintInlineBody` static-validation paths removed. |
| 2b | Repurpose the tool as a live drift reporter that connects to a device (or reads from IOSXEConfig CR status) and reports per-family drift for managed families (the Diff ops that would be applied). | Next iteration | ✅ Shipped. `drift.go:computeReport` runs each writer's `Fetch` + `Diff` against a live device for every family listed in the union of loaded CRs' `managedFamilies`. Non-empty ops are surfaced as `FamilyDrift` with op-count and verb histogram. |
| 2c | Add unmanaged-config detection: fetch the full device config, compare against the union of all IOSXEConfig CRs' `ManagedFamilies`, and report device-present families/objects that no CR claims. | Next iteration | ✅ Shipped. Orphan detection implemented in the same pass: every registered writer's `Fetch` runs; families with non-empty device state but no CR claim land in `Report.Orphans` with their YANG paths. |
| 2d | Support use as a CI tool in `driftPolicy: report` mode — "show me what would change before I switch to revert." | Next iteration | ✅ Shipped. `--exit-on-drift` returns exit code 4 when findings exist; `--output=json` emits a machine-readable report; `--mode={full,drift,orphans}` filters presentation without changing what's computed; `--ignore-families` skips out-of-scope families. |

Cluster-mode loader (read IOSXEConfigs from a running cluster
rather than local YAML paths) is tracked as a Phase-4 follow-up
in `iosxe-config-driver-review.md` §11.

---

## Feedback 3 — Template support: data-model YAML *and* CLI/Jinja style

**Reviewer position:** NAC templates can be expressed in two styles:

1. **Data-model style** — parameterised netascode YAML (structured).
2. **CLI/Jinja style** — IOS-XE CLI snippets rendered via Jinja
   templates (text-based).

The VK implementation should ideally support both. Jinja is the
canonical syntax NAC operators already author CLI templates in; VK
should consume those same templates without a dialect rewrite.

**Analysis:** The current `IOSXETemplate` only supports data-model style:
a YAML body with Go `text/template` `{{ .Param }}` substitution,
producing a netascode fragment that merges into the intent tree
(`internal/drivers/iosxe/configdriver/intent/template.go`).

Supporting CLI/Jinja templates requires three things:

- A type discriminator on `IOSXETemplate` (`spec.type: data-model | cli`)
  so the resolver routes CLI bodies to a text renderer instead of the
  YAML merger.
- A CLI-capable transport path — either NETCONF `edit-config` with a
  CLI payload (Cisco-IA `cli-config-data`) or the equivalent RESTCONF
  `operations` endpoint.
- A Jinja-compatible template engine so NAC-authored CLI templates
  (`{{ var }}`, `{% for … %}`, `{% if … %}`, filters like `default`,
  `join`, `upper`, `lower`) render unchanged in VK. Pure-Go Jinja
  engines exist (`flosch/pongo2`, `noirbizarre/gonja`); neither depends
  on a Terraform runtime or on HCL. Go `text/template` is *not* a
  drop-in replacement — the delimiter and control-flow syntax differ
  enough that NAC templates would need hand-porting.

### Action

| Step | Description | Priority | Status |
|------|-------------|----------|--------|
| 3a | Add `spec.type` field to the `IOSXETemplate` CRD with values `data-model` (default, current behaviour) and `cli`. | CRD change now | ✅ Shipped |
| 3b | CLI template rendering plumbing + NETCONF transport so CLI bodies can be pushed to a device. | Phase 2 | ✅ Shipped (render + transport path). `intent.ExpandCLITemplate` renders CLI text; `ResolvedIntent.CLIBlocks` carries the output as a side-channel (not merged into the data-model tree). The engine emits one `transport.Op{Verb:VerbCLI}` per block after family writes. Both transports push via Cisco-IA `cli-config-data`: RESTCONF POSTs `/operations/cisco-ia:cli-config-data` with a JSON envelope; NETCONF wraps CLI lines in `<cli-config-data xmlns="http://cisco.com/yang/cisco-ia">`. NETCONF adapter is hand-rolled over `golang.org/x/crypto/ssh` with both 1.0 (`]]>]]>`) and 1.1 chunked framing (RFC 6242), hello-based capability detection (base:1.1, candidate, confirmed-commit), and `lock`/`edit-config`/`commit`/`discard-changes`/`unlock` wired to the transport's transactional surface. Under `driftPolicy: report`, CLI blocks surface as `cli:<templateName>` drift entries rather than being applied. **Template-engine gap:** the CLI renderer currently uses Go `text/template`, which is syntactically incompatible with Jinja — NAC CLI templates need hand-porting until 3c lands. |
| 3c | Swap the CLI template renderer from Go `text/template` to a pure-Go Jinja engine (`flosch/pongo2` or `noirbizarre/gonja`) so NAC-authored CLI templates consume unchanged. Data-model templates stay on `text/template` — they are structured YAML, not CLI text, and Jinja's text-oriented control flow buys nothing there. | Next iteration | ⏳ Pending |

---

## Feedback 4 — Share YAML merge logic with `terraform-provider-utils`

**Reviewer position:** The YAML merge logic in
[`netascode/terraform-provider-utils`](https://github.com/netascode/terraform-provider-utils)
(`internal/provider/merge.go`) is the canonical implementation used by
all NAC modules today. CVK should use the same code rather than
reimplementing it. If the merge logic has not been broken out into its
own Go module, that is something to pursue to avoid code duplication.

**Analysis — implementation differences:**

| Aspect | `terraform-provider-utils` | CVK `intent/merge.go` |
|--------|----------------------------|-----------------------|
| Map merge | `MergeMaps` — recursive, supports `*OrderedMap` and `map[string]any` | `mergeMaps` — recursive, `map[string]any` only |
| List merge | All-matching-primitive-fields heuristic (`itemsWouldMerge`) | Named key-field candidates `name > id > sequence > type` + explicit `KeyRules` from `families.yaml` |
| List identity | Any shared primitive key-value match | Single declared key field |
| Duplicate detection | `hasDuplicatesInList` with inverted index | Not handled |
| Key order | `OrderedMap` preserves YAML source key order | Standard `map[string]any` (no order guarantee) |
| Mutation | Mutates `dst` in place | Returns deep-copied output; neither input mutated |

The list-merge semantics are **materially different**. The key-field
approach in CVK is more deterministic (uses family metadata from
`families.yaml`), but the primitive-match heuristic in
`terraform-provider-utils` may handle edge cases CVK misses — and
crucially, it is the heuristic operators already rely on in production
NAC deployments.

**Licensing note:** `terraform-provider-utils` is MPL-2.0; CVK is
Apache-2.0. MPL-2.0 is file-level copyleft, so importing as a Go module
dependency is fine, but the shared module's own license should be
confirmed with legal.

**Practical note:** The core merge logic (`MergeMaps`, `OrderedMap`,
`itemsWouldMerge`, `mergeListItemsIndexed`) has no Terraform framework
dependency and is cleanly separable from the `terraform-plugin-framework`
function wrappers.

### Action

| Step | Description | Priority |
|------|-------------|----------|
| 4a | Add cross-validation tests: run the same family YAML corpus through both CVK's `MergeWithRules` and `terraform-provider-utils`'s `MergeMaps` and assert identical output for all 54 families. | Immediate |
| 4b | Open a discussion/issue with `terraform-provider-utils` maintainers proposing extraction of the core merge + YAML utilities into a standalone Go module (e.g. `github.com/netascode/nac-utils-go`) containing `OrderedMap`, `MergeMaps`, `yamlDecode`/`yamlEncode`, and `resolveYamlTags`. The HCL template evaluator stays out of scope — VK targets Jinja for CLI templates (see 3c) and has no Terraform surface. | Medium term |
| 4c | Once the shared module exists, replace CVK's `intent/merge.go` with an import of the shared module, adapting the `KeyRules` layer as a CVK-local wrapper. | Medium term |
| 4d | Confirm MPL-2.0 / Apache-2.0 compatibility with legal for the shared module approach. | Medium term |

---

## Summary — priority matrix

| # | Action | Priority | Status |
|---|--------|----------|--------|
| 1a–1c | Remove `cisco-vk-config-collect`, update RFC | Immediate | ✅ Shipped (commit `cf032b7`) |
| 3a | Add `spec.type` to `IOSXETemplate` CRD | Immediate | ✅ Shipped (commit `1c82a28`) |
| 4a | Cross-validation tests for merge logic | Immediate | ✅ Shipped (commit `1c82a28`) |
| 2a–2d | Repurpose `cisco-vk-config-lint` as drift reporter | Next iteration | ✅ Shipped |
| 3b | CLI template rendering + NETCONF transport | Phase 2 | ✅ Shipped (this iteration) |
| 3c | Migrate CLI template renderer from Go `text/template` to a pure-Go Jinja engine (pongo2/gonja) for NAC CLI-template parity | Next iteration | ⏳ Pending |
| 4b–4d | Shared merge module with `terraform-provider-utils` | Medium term | ⏳ Pending; cross-validation corpus (4a) is ready to swap expected outputs for shared-library calls once the module extraction lands |
