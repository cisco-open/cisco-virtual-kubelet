# Template software-version fallback and explicit clearing

CVK previously rejected `catalyst_center.templates.software_version`, although the
pinned NetAsCode 0.5.0 module uses it as the regular/composite definition fallback.
The existing template parser now consumes it for onboarding regular, Day-N regular
and composite definitions. Report, Apply, draft matching, commit verification and
owned state capture continue to use the same compiled payload and existing
transport/journal contracts. Other defaults still resolve upstream.

## Pinned precedence

The module uses `try` rather than `coalesce` at the
[regular mapping](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_templates.tf#L388)
and [composite mapping](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_templates.tf#L426).
The [source inspection](template-software-version-upstream.json) records the
module commit, file hash, expression and exact lines.

| Definition property | Templates property | Compiled behavior |
| --- | --- | --- |
| Omitted | Nonempty string | Inherit the templates constraint |
| Nonempty string | Any valid fallback | Use the definition constraint |
| Explicit `null` | Nonempty string | Suppress inheritance; do not manage this optional native field |
| Explicit `""` | Nonempty string | Explicitly clear the constraint |
| Omitted | Explicit `""` | Explicitly clear the constraint |
| Omitted | Null or omitted | Leave the optional native constraint unmanaged |

Null is not a request to clear. Existing update logic preserves omitted optional
native attributes during another metadata/content update. The live case forces a
content update with a null override and verifies that the old native constraint
survives, rather than accidentally inheriting the templates value.

For explicit empty constraints, native omitted, null and empty-string readbacks
are equivalent for managed-field convergence. Nonempty constraints remain exact;
malformed numeric/array readbacks do not match. This equivalence applies only to
`softwareVersion`. Private ownership baselines still use complete raw native
snapshots and do not adopt this normalization or discard unknown fields.

Non-string/non-null fallbacks and overrides are rejected before native writes,
even when other definitions provide their own override. No new source format,
CRD, provider interface, transport, key projection or task/lease store is added.

## Qualification

- [22 focused race cases](template-software-version-tests.log) pass in 3.163
  seconds. They cover root/definition precedence, onboarding/regular/composite
  inheritance, null suppression, explicit clearing, malformed types, Report/Apply
  planning and real public reconciler no-op/refusal routing. HTTP and ordinary
  Kubernetes state are simulated; fresh reconciler instances preserve source
  binding and never manufacture mutation/ownership receipts for unchanged objects.
- [Three private Kubernetes API cases](template-software-version-api.log) pass in
  8.671 seconds using the real CRDs on an isolated Kubernetes 1.35.0 API server.
  They exercise inherited and cleared InSync observations and malformed-source
  refusal before native observation. Product HTTP is simulated; the installed
  cluster and worker actors are not used. A fake-client fixture initially lacked
  its ConfigMap UID and was correctly fenced; that fixture now supplies identity,
  while the real API uses server-generated UIDs.
- [Native eight-transition cycle](template-software-version-native.log) passes in
  225.55 seconds. Real project create/update and regular template create/update/
  commits establish the generated fixture. Through fresh existing compilers, the
  fixture inherits a constraint, overrides it, preserves the override during a
  null/content update, returns to inheritance and explicitly clears it. Empty,
  null and omitted fallbacks then converge without extra tasks or versions.
  Each modifying transition completes its actual task tree, verifies the same
  captured UUID and parent, checks draft and committed readback, advances the
  native committed version and reaches a fresh-compiler no-op. Exact template and
  project cleanup is verified.
- [Broad template/configuration/PnP/profile regressions](template-software-version-regression.log)
  pass in 174.691 seconds; [shared ND/controller/software-upgrade foundations](template-software-version-foundations.log)
  pass. The published example at
  `examples/configs/catalyst-center/template-software-version-execution.yaml`
  and existing examples [compile](template-software-version-examples.log).
- [Strict documentation build](template-software-version-docs.log), formatting,
  whitespace and the [93-resource scope audit](template-software-version-ledger.log)
  accompany this change. Resource rows do not prove complete parity.

## Execution scope and remaining work

`software_version` here is template applicability metadata, not a SWIM target or a
request to change a switch's running image. No device deployment or upgrade was
submitted. The live transition qualification covers regular definitions;
onboarding/composite fallback mapping is fixture-qualified. Native public-worker
execution is not implied by separately passing public fixtures and native compiler
qualification.

Version-pattern matching, software variants, broader schema/default combinations,
combined definition/deployment execution and installed qualification remain
pending. Public template pruning/deletion/recreation remains disabled; complete
operational dependency coverage and the destructive lifecycle still need their
separate ownership/source/reference/task/absence safeguards. This closes one
pinned field-resolution gap, not the full Catalyst Center parity goal.


Follow-up: [variable metadata qualification](template-variable-metadata.md)
introduces conditional parameter-record ordering and exact native decoding for
all template views. All native values/fields remain significant; software-version
empty/null matching is still excluded from keyed snapshot equivalence.
