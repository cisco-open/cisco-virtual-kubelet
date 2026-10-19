# Optional null discovery fields

Pinned `cc_discovery.tf` 0.5.0 passes optional job attributes through
`try(job.field, defaults.field, null)`. A present null does not select a fallback.
CVK previously rejected nulls for all mapped discovery attributes, so a resolved
manifest containing optional nulls could fail before planning.

The existing parser now treats null as unselected for seven supported optional
fields: `cdp_level`, `lldp_level`, `retry`, `time_out`, `netconf_port`,
`preferred_mgmt_ip_method` and `ip_filter_list`. They produce no native property,
no zero/empty-string value and no invented default. The offline defaults exporter
uses the same parser: optional null overrides are allowed, while a null present
in the job suppresses a non-null fallback. Runtime default merging is unchanged.
Non-null values still use the existing type/range/compatibility checks; explicit
zero and empty-list declarations are distinct from null.

Required resolved name/type/address/protocol/global-credential references still
cannot be null. Scope controls, unknown fields and unimplemented credential fields
also remain rejected, even when null. No unknown declaration is silently dropped.
The `examples/configs/catalyst-center/discovery-null-apply.yaml` example uses
ordinary Retain policy and explicit global credential references.

## Qualification

The initial focused gate reproduced the parser/default failures before the fix.
Projection tests compare each nullable field and the complete nullable set with
omission. Ordinary and mixed inventory/template manifest cycles create once,
recover/restart, converge, switch null-to-omitted and back, and preserve the exact
creator receipt without additional writes. Required/scope/unknown/credential null
cases fail through the public reconciler before any native I/O or journal change.
Defaults tests cover null overrides and explicit-null suppression of a non-null
fallback. The complete discovery regression group passes, including existing
ownership, cleanup, history and handoff tests; migration/export and pinned-model
packages pass separately.

Real Kubernetes API qualification additionally reads the stored source to prove
its null keys survive CRD persistence before running both public manifest cycles.
This is separate from native qualification, which uses a real appliance with
fixture Kubernetes persistence and explicit actor reconstruction.

Native job **238** created from a source containing all seven nulls. The guarded
transport required exactly the original five selected payload properties; no null
property was forwarded. Null-to-omitted and omitted-to-null source revisions both
converged without another mutation or changed receipt. Public deletion then made
one exact delete, recovered its original task, retained absence/retirement evidence
and released the manager finalizer. The complete discovery baseline and target
inventory identity were preserved. The existing cleanup Unknown fault was injected
in fixture Kubernetes state; this is not an installed null-field rollout.

## Remaining boundary

This closes these seven optional-field predicates, not complete discovery schema
or default parity. Required unresolved values, inline credential execution,
unresolved whole-module inputs and broader composition remain open. Null is
unselected intent, not an instruction to reset or replace the native job.
The earlier installed handoff image/source boundary predates this parser change;
its qualification is not relabeled as installed optional-null qualification.
The full handoff-source race audit passed 13,593 test/subtest events with no
failures, but predates this change ([terminal boundary](discovery-handoff-full-suite.json)).
The separate optional-null full race audit passed **13,627** test/subtest events
in **1688.358 seconds**, with no failures
([terminal boundary](discovery-null-full-suite.json)). This audit predates the
private discovery credential additions; hosted CI and an installed optional-null
rollout remain unqualified.

Private native checkpoint: `/private/tmp/cvk-discovery-owned-2367546028`.
See the [validation record](discovery-optional-null.json),
[initial failing gate](discovery-null-before.log),
[discovery regression](discovery-null-regression.log),
[real API persistence](discovery-null-api.log),
[native qualification](discovery-null-native.log), and
[export compatibility](discovery-null-export-regression.log).
