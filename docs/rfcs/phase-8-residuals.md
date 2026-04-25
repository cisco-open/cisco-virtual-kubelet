# Phase 8 residuals

**Branch:** `pr/johalley/ciscoconfig_xe`
**Status:** open. Tracks the Phase-8 deliverables that have shipped on this branch *as code* but require external infrastructure (publishing, signing, registry metadata, content) before operators can consume them at the level intended by the original Phase-8 scope.
**Audience:** anyone planning the Phase-8.5 push that closes these.
**Companion docs:**
[`iosxe-config-driver-review.md`](iosxe-config-driver-review.md) (where Phase 8 is defined),
[`architectural-review.md`](architectural-review.md) (the "ship the per-pod topology today" verdict).

---

## 1. Why a separate RFC

`iosxe-config-driver-review.md` §11 closes Phase 8 with: "All Phase 0–8 milestones shipped on this branch; the only residuals are external (Terraform Registry release infrastructure, netascode example corpus) and tracked at the foot of §11." That foot-note is fine for a design review but too thin to action against. This document is the actionable form: each residual is named, the work is scoped, and the acceptance criteria are explicit.

These items are deliberately scoped *out* of this branch because they involve work that lives outside the Git repository: signing keys held in a corporate KMS, Terraform Registry metadata that requires a verified publisher account, MkDocs content that requires curation against a real device fleet. A PR can ship code; only release engineering can ship signing infrastructure.

---

## 2. Residual #1 — Terraform Registry release infrastructure

### 2.1 Where the Phase-8 code stands today

In-tree, complete:

- `tools/terraform-provider-iosxeconfig/` — the provider itself, real CRUD against `IOSXEConfig` CRs through the cluster API. Schema, plan, apply, import, drift detection — all functional. Acceptance test fixtures exist under the Hashicorp `terraform-plugin-testing` framework.
- `tools/terraform-provider-iosxeconfig/internal/provider/resource_iosxeconfig_test.go` — provider unit tests (green on this branch).
- Documentation strings on every resource and data source.

What this means: an operator who builds the provider locally (`go install`) and points Terraform at the local binary via a `dev_overrides` block in `~/.terraformrc` gets a working provider end-to-end against a real cluster.

### 2.2 What is missing

What `terraform init` operators expect — pulling the provider from the public Terraform Registry — does not work. Specifically, the following infrastructure does not exist in the project's namespace:

| Item | Owner | State |
|---|---|---|
| Terraform Registry "verified publisher" account for `cisco-open` | Cisco / Hashicorp | not provisioned |
| GPG signing key registered with the publisher account | release-engineering | not generated for this repo |
| GitHub Actions release workflow that builds, signs, publishes the provider on tag push | this repo | not implemented |
| Provider registry manifest (`registry.json`) | this repo | not authored |
| Documentation on the registry side (README rendered as the provider's landing page) | this repo | not landed |

### 2.3 Acceptance criteria for closing

This residual closes when, on a clean machine with no `dev_overrides`, the following works:

```hcl
terraform {
  required_providers {
    iosxeconfig = {
      source  = "cisco-open/iosxeconfig"
      version = "~> 0.1"
    }
  }
}
```

```bash
$ terraform init
Initializing provider plugins...
- Finding cisco-open/iosxeconfig versions matching "~> 0.1"...
- Installing cisco-open/iosxeconfig v0.1.0...
- Installed cisco-open/iosxeconfig v0.1.0 (signed by cisco-open)
```

…with the signed-by line referring to the GPG key the publisher account vouches for.

### 2.4 Concrete work breakdown

In rough sequence:

1. **Cisco / Hashicorp** — provision the publisher account; this is paperwork outside the repo's reach.
2. **Release-engineering** — generate a GPG key in the corporate KMS; export the public key and register it with Hashicorp.
3. **In this repo** — author `.github/workflows/terraform-release.yml` that runs on `tools/terraform-provider-iosxeconfig/` tags (`tfprovider/v*`):
   - Build for `linux_amd64`, `linux_arm64`, `darwin_amd64`, `darwin_arm64`, `windows_amd64`.
   - Sign each `_SHA256SUMS` with the registered GPG key (KMS-backed via `gh-action-sign-archive` or equivalent).
   - Upload binaries + signatures to the GitHub release.
   - Hashicorp's webhook polls the release and indexes it.
4. **In this repo** — author `tools/terraform-provider-iosxeconfig/docs/` content in the layout Hashicorp expects (auto-rendered from the provider's `Description` strings, but a hand-written index page is required).
5. **Smoke** — install the published provider in a fresh shell on a machine without the source checkout; run an `apply` against a kind cluster.

### 2.5 Effort and risk

Effort is dominated by step 1 (publisher account paperwork) and step 2 (KMS provisioning), neither of which is engineering work. The technical residue (steps 3–5) is on the order of two engineer-days once the publisher account exists.

Risks:

- **Account naming.** "cisco-open" is the recommended namespace; resolve any conflict with an existing Cisco-owned publisher account before announcing the provider source.
- **Versioning.** First-published version cannot be `0.0.x`; Terraform Registry rejects pre-release tags. Use `0.1.0`.
- **Schema drift between provider and CRD.** The provider currently pins to `config.cisco.vk/v1alpha1`. The CRD-v1 promotion (see `crd-v1-promotion-plan.md`) will require a dual-version provider release; coordinate the cuts.

---

## 3. Residual #2 — netascode example corpus for the portal-compat dialect

### 3.1 Where the Phase-8 code stands today

In-tree, complete:

- `tools/cisco-vk-config-docs/` with a `--dialect=portal` flag.
- The portal dialect emits an MkDocs directory tree under `data_models/iosxe/<family>/index.md` that mirrors the netascode portal URL shape, with front matter, OpenConfig path surfacing, and family-cross-link resolution.
- Generated tree validates against MkDocs strict mode; URL fragments resolve to the right family pages.

What this means: someone running `cisco-vk-config-docs --dialect=portal --output-dir=site/` gets a navigable docs tree that matches the netascode portal layout *structurally*.

### 3.2 What is missing

The structure exists; the *content* of each per-family page does not. Each family page has the right front-matter, the right key list, and the right cross-links, but the "Example" section that operators actually reach for is empty. The netascode portal's value is overwhelmingly in those examples — they're how operators learn how to express a non-trivial config in the netascode YAML shape.

The gap, concretely, is ~54 example fragments — one per family. Each fragment is a 5-30 line YAML excerpt showing a realistic configuration of the family in the netascode shape, with comments explaining the non-obvious fields.

### 3.3 Acceptance criteria for closing

This residual closes when:

1. Every family page generated by `cisco-vk-config-docs --dialect=portal` includes a non-empty Example block.
2. Each example is actionable on a real device (verified by feeding it through `cisco-vk-config-lint --offline` and asserting the rendered ops are sensible).
3. A representative subset (~10 families) has been actually applied to a Cat9K via a `driftPolicy: revert` reconcile and reverted, capturing operator feedback on whether the example was right.

### 3.4 Concrete work breakdown

1. **Curate** — start from the netascode portal's existing examples for the families where a 1:1 translation is possible. Translate the YAML keys to CVK's family names (`access_list_extended` vs netascode's `ip.access-lists.extended`), preserving the structural shape that operators recognise.
2. **Author** — for the families where netascode has no equivalent example (e.g., the IOS-XE-specific families that aren't in the netascode portal), write fresh examples grounded in the YANG schema. The `families.yaml` index has the leaf list; the example should exercise a meaningful subset.
3. **Wire** — add the example fragments under `tools/cisco-vk-config-docs/examples/<family>.yaml`. Modify the portal-dialect renderer to inline the file's contents into the generated page's Example block.
4. **Verify** — for each example, run `cisco-vk-config-lint --offline` against a CR built from it; assert the rendered ops are sensible.
5. **Live-test** — pick ~10 families, apply via `driftPolicy: revert` against the lab Cat9K, observe the device, revert. Capture any "this example was wrong because…" findings and patch the corpus.

### 3.5 Effort and risk

The effort is content authorship, ~1 hour per family for the straightforward ones, ~3 hours per family for ones requiring fresh authorship. Total ~80 engineer-hours of focused writing. Reviewable in chunks of 8-10 families per PR.

Risks:

- **Example correctness.** Writing config examples that look right but don't actually compile under the device's parser is the bug operators dread. Step 4 (lint --offline) catches the rendering layer; step 5 (live revert/apply) catches the device-acceptance layer. Both are required.
- **Style consistency with the upstream netascode portal.** The point is to feel *familiar* to anyone who has read the netascode portal, not to invent a new style. PR review should compare each example side-by-side with the corresponding netascode page.
- **Example maintenance over time.** As IOS-XE YANG releases evolve, some example shapes become stale. Add a `generated-against-yang-release: 17.18.1` front-matter field to each example so a mass-update PR is mechanically generatable when a new release lands.

---

## 4. What is NOT a Phase-8 residual

For clarity, since the phrase "Phase 8" gets stretched in conversation:

- **CRD v1 promotion** is its own RFC ([`crd-v1-promotion-plan.md`](crd-v1-promotion-plan.md)). It's a v1 cut concern, not a Phase-8 deliverable.
- **Log unification** is its own RFC ([`log-unification-plan.md`](log-unification-plan.md)).
- **`internal/drivers/iosxe/configdriver/...` → `internal/configdriver/` relocation** is the Phase-10 mechanical move tracked in [`driver-extension-guide.md`](driver-extension-guide.md) §7. Not Phase 8.
- **Reconciler-level OTel spans** were a watch-item shipped on this branch (`internal/provider/config_reconciler_controller.go`).
- **Per-namespace VK ServiceAccount, ClusterRoleBinding, IOSXEConfigApplyLog RBAC** were live-test-surfaced bug fixes, not Phase-8 work.

---

## 5. Status sweep — Phase-8 in-tree deliverables (✅ shipped on this branch)

For completeness, the items that *did* ship on this branch:

| Phase-8 deliverable | Status | Anchor |
|---|---|---|
| Terraform provider for `iosxeconfig_config` (real CRUD on `IOSXEConfig` CRs) | ✅ shipped | `tools/terraform-provider-iosxeconfig/` |
| ArgoCD health-check Lua hooks for `IOSXEConfig` and `IOSXEConfigBundle` | ✅ shipped | `docs/argocd-health/` |
| OPA / conftest rule packs for `IOSXEConfig` admission-time policy | ✅ shipped | `policy/conftest/` (under `tools/cisco-vk-config-lint/policy/`) |
| `cisco-vk-config-docs` netascode portal-compat dialect (structure) | ✅ shipped | `tools/cisco-vk-config-docs/` (`--dialect=portal`) |
| `cisco-vk-config-docs` netascode portal-compat dialect (content corpus) | ⏳ **residual #2** | tracked above |
| Terraform Registry distribution | ⏳ **residual #1** | tracked above |

---

## 6. Suggested triage

If only one of the two residuals can be funded near-term, prioritise **#2 (example corpus)** over **#1 (Registry release)**. The example corpus is what operators actually reach for during evaluation; without it, the portal-compat dialect feels like an empty shell. The Registry release is a convenience that motivated operators can substitute for via a `dev_overrides` block — annoying but not a blocker.

If both can be funded, run them in parallel: they share no dependencies and no review surface.
