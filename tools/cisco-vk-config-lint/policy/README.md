# Policy rules for IOSXEConfig CRs

This directory ships a starter pack of Rego rules suitable for
[`conftest`](https://www.conftest.dev/) or any Open Policy Agent
deployment. Operators run them against rendered IOSXEConfig
manifests in CI:

```sh
conftest test --policy tools/cisco-vk-config-lint/policy \
  manifests/edge-01/*.yaml
```

The rules are advisory — none of them gate the runtime driver. They
exist so review-time policy lives in the same repo as the data model
the policy reasons about, and so future OPA-side gating
(`ValidatingAdmissionPolicy`, gatekeeper) can reuse the same files
without translation.

## Files

- `iosxeconfig.rego` — generic CR-shape rules
  (no `driftPolicy: revert` without a `pruneOnRelinquish` companion;
  required `managedFamilies`; deny inline secrets; …).
- `iosxe_security.rego` — Cisco-IOS-XE-specific guard rails
  (no `username … privilege 15` without `secret`; no
  `enable password` in cleartext; require AAA when SNMP write
  community is set; …).
- `families.rego` — gating rules referencing family names declared
  in `internal/drivers/iosxe/configdriver/schema/families.yaml`.

## Conventions

Each rule emits a `deny[msg]` (hard fail) or `warn[msg]`
(advisory) so `conftest test` can be wired with
`--no-fail-on-warn` in early adoption phases. Rules namespace
their messages by file so an operator with `conftest test
--output=json` sees provenance:

```text
[ERROR] iosxeconfig: spec.driftPolicy: 'revert' set without pruneOnRelinquish guidance
```

## Adding rules

The CR's full JSON shape is the input — the Rego rules walk
`input.spec.*` and `input.metadata.*` directly. When adding a new
field to the CRD, add a positive test (`spec_with_X.json`) under
`fixtures/` and a `test_X` rule in the matching `.rego` file so a
regression breaks the build.
