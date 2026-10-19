# SECTIONDIVIDER native contract investigation

SECTIONDIVIDER remains a required, unresolved part of the pinned NetAsCode model.
The earlier [datatype qualification](template-parameter-types.md) found that the
lab omitted a divider without a content reference and retyped a referenced one
to STRING. This follow-up investigates documented ordering/grouping fields; it
does not replace divider support with a smaller success criterion.

## Evidence and scope

A [read-only inspection](template-divider-existing-readonly.log) found no divider
records in 26 existing native drafts. This is a bounded search for an example,
not a complete proof that no API or appliance version supports dividers.
The [upstream model](https://netascode.cisco.com/docs/data_models/catalyst_center/templates/template/)
lists the datatype; a schema enum alone does not prove its native representation. Pinned enum/mapping
provenance remains in the [source inspection](template-variable-metadata-upstream.json).

Three explicit candidates use the documented native fields:

1. `customParamsOrder=true` and per-record `order`/`customOrder`, with a divider
   before a normal parameter and no divider content reference.
2. The same ordering fields with a content reference.
3. Ordering plus `group` and `key` fields, with no divider content reference.

These fields are **research inputs only**, not hidden additions to production
NetAsCode compilation. The [research helper source](template-divider-contract-sources.json)
records the tested source hashes. The native definition must retain the divider
datatype and every requested managed field before it can qualify. A scalar
substitute, missing record, altered order or stripped metadata does not match.

The initial [native run](template-divider-contract-native.log) completed two
candidate tasks; neither preserved the divider. Planning the grouping candidate
then stopped with a stale-input error. Its fixture was retained rather than
blindly retried. [Supervised cleanup](template-divider-contract-retained-cleanup.log)
checked original NCTP Create-task UUID proofs, the last recorded completed update,
exact captured identities/content/parameter names, two-version history and empty
PnP/profile consumers before completed Delete tasks and final absence.

## Replanning and final probe

The final [native run](template-divider-contract-native-final.log) uses CVK's
existing one-use write and guard-bound proof of no dispatch. Only a refusal that
the original guard certifies can lead to a fresh plan, with at most three plans
per candidate. An uncertain write or missing proof retains the fixture and is
never retried. A candidate that exhausts proven refusals is explicitly **not
tested**, rather than classified as a native rejection.

Every dispatched request checks the captured project/template UUID, full task
tree, independent native readback and unchanged committed version. Known completed
draft outcomes permit another explicitly different candidate and exact generated
cleanup. The native research test remains a failed qualification until divider
execution and committed lifecycle work; successful cleanup is not divider support.
It is opt-in with `CATC_TEMPLATE_DIVIDER_CONTRACT=isolated-research`.

In that run, the grouping candidate exhausted three **proven** no-dispatch
refusals. A third [group-first run](template-divider-contract-native-group-first.log)
therefore tested grouping before introducing the unstable two-record custom-order
view. All three candidates actually dispatched and their full task trees
completed. None preserved the divider or complete managed intent. The run failed
qualification in 106.76 seconds and verified exact template/project cleanup.
No guard was removed or snapshot comparison relaxed to obtain those requests.

A separate [ordering inspection](template-custom-order-readonly.log) found no
existing custom-ordered templates among the 26 drafts. No sufficient native
contract evidence justified canonicalizing true custom-order views by name or
inventing precedence between ordering properties. That production behavior remains
unchanged. A confirmed native representation/reference implementation is still
needed before adding a production divider mapping; these bounded failed probes
are not proof that every possible representation is unsupported.

With the normal live endpoint/credential environment configured, reproduce the
research using:

```sh
CATC_LIVE_REQUESTS_PER_SECOND=2 \
CATC_PROJECT_EXECUTION=isolated-project \
CATC_TEMPLATE_COMMIT=isolated-template \
CATC_TEMPLATE_DIVIDER_CONTRACT=isolated-research \
go test -race -tags live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveCatalystCenterTemplateProject$' -count=1 -timeout=15m -v
```

The shared live metadata helper now reuses one task-tree wait function; its
completion/error/context behavior is unchanged. [25 ordering/fingerprint/dispatch
subcases](template-divider-contract-regression.log) pass. Custom-order observations
remain order-sensitive, and server-side changes cannot be normalized away merely
to make a research request submit. [Strict docs](template-divider-contract-docs.log)
and the [93-resource scope audit](template-divider-contract-ledger.log) accompany
the evidence.

No device deployment, scheduling, software upgrade, ownership adoption, receipt
migration, public prune or installed actor is part of this research. Production
transport, ND and direct-device/SWIM foundations are unchanged. All other resource
gaps and the full pinned-model goal remain open; resolving this field is not a
claim that template lifecycle or controller parity is complete.
