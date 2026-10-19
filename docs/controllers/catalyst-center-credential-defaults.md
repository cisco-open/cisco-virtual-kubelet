# Pinned credential defaults

`controller-export-credentials` explicitly expands missing non-secret credential
fields from the bundled NetAsCode **0.5.0** defaults. It reuses the same pinned
bundle and strict decoder as network-profile defaults, then validates the exact
output with the existing credential execution parser. Runtime reconciliation,
Secret resolution, task recovery and ownership are unchanged.

From the repository root:

```sh
go run ./tools/cvk-netascode-migrate controller-export-credentials \
  --model-version 0.5.0 \
  --output /private/tmp/catc-credentials-resolved.json \
  examples/configs/catalyst-center/credential-defaults.yaml
```

The output path must be new. Resolved model data is written mode 0600; stdout
contains only provenance and counts. Use the resulting model as the existing
global network-settings owner's ConfigMap/inline source, retaining the normal
Secret references and version/value envelopes. Their destination paths do not
change. The command neither reads credentials nor executes an API operation.

| Missing active field | Pinned value |
| --- | --- |
| HTTPS read/write `port` | 443 |
| SNMPv3 `auth_type` in AUTHNOPRIV/AUTHPRIV | SHA |
| SNMPv3 `privacy_type` in AUTHPRIV | AES128 |

Explicit fields are preserved. Explicit null is not replaced by a default and
must satisfy the existing execution parser. Inactive implicit SNMP defaults are
omitted from execution intent; explicit contradictory inactive fields remain an
error. NOAUTHNOPRIV gains no authentication/privacy fields. Modes, usernames,
passwords and write-only counters are never invented. Inline secret values,
unknown fields, duplicate keys, aliases and ambiguous documents are refused
without returning payload or value-bearing errors.

Without selection flags, this command accepts one already-selected global
`network_settings.device_credentials` section. With selection flags it also accepts
minimal site credential references, as described below. Other sections and merging
are not implemented here. It is not a whole-model NetAsCode resolver. Provenance
includes the pinned revision/default hash, resolution/validation policy hash and
input/output hashes; `leafSchemaValidated` and `configurationParity` remain false.
Direct runtime input still requires resolved fields.

## Scoped override files

Add `--defaults examples/configs/catalyst-center/credential-default-overrides.yaml`
to supply a separate `defaults.catalyst_center.network_settings.device_credentials`
overlay. Only HTTPS read/write ports and SNMPv3 auth/privacy types are accepted.
Precedence is pinned fallback, then overlay, then explicit entry value. Partial
overlays keep other pinned fields. The same decoder handles profile and credential
defaults with separate allowed scopes; neither silently consumes the other's data.

All override fields are validated through the existing credential parser before
selection, including unused or explicitly shadowed values. Null, non-finite,
invalid, unknown and secret-bearing defaults are refused rather than ignored.
Usernames, modes, credential values and version counters remain explicit. The
CLI rejects missing/empty override files and stdin reuse for `--defaults`.
Provenance adds `overridesSHA256`; resolved bytes still go only to a new private
file. The shared decoder refactor changes policy fingerprints, not existing
profile-default semantics or the pinned defaults bundle.

See [override qualification](../evidence/catalyst-center-2026-10-18/credential-default-overrides.md).

See [qualification evidence](../evidence/catalyst-center-2026-10-18/credential-defaults.md).


## Global and per-site credential selection

Use explicit selection flags for a source containing normalized definitions and
minimal `sites.areas/buildings/floors` credential references:

```sh
go run ./tools/cvk-netascode-migrate controller-export-credentials \
  --model-version 0.5.0 \
  --managed-sites '["Global/Lab"]' --manage-specific-sites-only \
  --output /private/tmp/catc-site-credentials-resolved.json \
  examples/configs/catalyst-center/credential-site-selection.yaml
```

| Selection | Credential definitions | Site assignments |
| --- | --- | --- |
| Explicit `--managed-sites '[]'`, global flag false | All declarations | All declared references, including Global |
| `--manage-global-settings`, no managed sites | All declarations | Global only |
| Nonempty managed sites, global flag false | None | Selected paths |
| Global flag plus managed sites | All declarations | Global and selected paths |

The shared pinned selector uses literal prefix matching unless exact-only mode
is requested: `Global/Lab` also matches `Global/Lab2`. Each explicitly managed path
must be declared. Global credential references use the existing area declaration
`name: Global` with no parent. Site geometry and other network/profile declarations
are outside this exporter scope and are refused, not discarded.

All normalized definitions/defaults and reference syntax are validated before
filtering. Names are resolved against native credentials only during execution;
missing selected names fail instead of silently using a Global slot. Definitions
can be omitted for lookup-only sources. Definition-bearing inputs still require
the existing explicit modes/counters even when per-site output omits them. Null
assignment removal remains unsupported.

Per-site output contains no credential definitions or counters. Use no definition
Secret references in that config. This selector does not create sites, read
Secrets, adopt credentials or change ownership. The existing global
`network_settings` section owner must apply the generated model; parallel per-site
owners and independent state authority are not enabled. Retain semantics preserve
omitted prior state. Provenance separates declared/selected definitions, selected
assignments/sites and option hashes; default counts describe preselection expansion.

See [selection qualification](../evidence/catalyst-center-2026-10-18/credential-selection.md).


## Acknowledged assignment recovery

Public network-settings plans now recover supported site-credential Update tasks
retained as Unknown. The common journal still enforces the original owner,
controller generation and source revision. The assignment observer requires an
unchanged declared assignment, original site/credential UUIDs, a complete
successful task tree and stable native readback around that observation.
Persisted operations repeat the same task/dependency/effect proof at final
verification. Other composed resource observers remain in place.

Taskless outcomes, removed/null assignments, changed source, replaced UUIDs,
incomplete/failed trees and inconsistent readback remain fenced. Recovery does
not submit another assignment or grant a new mutation permit. It does not add
inheritance removal, credential deletion or ownership transfer.

See [recovery qualification](../evidence/catalyst-center-2026-10-18/site-credential-recovery.md).

## Installed assignment recovery qualification

All six named credential assignments now pass acknowledged Unknown recovery in
the isolated lab's installed manager/worker, including one worker Pod replacement
per family and a later authoritative no-op observation. Source restoration
precedes supervised fixture cleanup. This qualification does not enable credential
pruning or parent-inheritance removal and does not attest device authentication.
See [installed evidence](../evidence/catalyst-center-2026-10-18/installed-site-credential-recovery.md).
