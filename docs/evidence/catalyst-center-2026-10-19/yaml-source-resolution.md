# Explicit YAML source resolution

Pinned NetAsCode **0.5.0**, revision
`a435555fe828f03cf3311d52fe1ab6572a247916`, utils **2.0.0** revision
`a383caa57652edc7952f3bd596f9a75719480643`.
Full configuration and YAML-loader parity remain **incomplete**.

## Implemented source path

The offline `controller-merge --resolve-yaml` path resolves supported anchors,
independent alias copies, merge-key precedence, scalar tags and explicit source
bindings using CVK's existing YAML library. The default import contract and
runtime parsing remain unchanged. No second project/runtime parser, transport,
controller, owner or journal is introduced.

Every process-environment binding requires an explicit `--env NAME`. The pure
source library receives only caller-provided values; it cannot read the process
environment. Tagged and quoted `!env` strings follow the pinned provider behavior;
bound values stay strings and are not interpreted recursively. Keys are not
environment-resolved. Missing/empty/unlisted values fail before output. Values
and names are omitted from provenance, which reports counts/digests only.

`inputSnapshotSHA256` binds ordered raw/effective inputs, source mode and policy.
Model/default components share that binding; a changed environment value changes
it even when input-file bytes are identical. `orderedInputsSHA256` still identifies
raw file inputs alone. Output stays private mode0600. Section defaults and strict
execution checks run afterwards through existing paths.

Alias copies do not share mutable merge state. Cycles, undefined references,
ambiguous canonical keys and over-limit expanded inputs refuse. Expanded mapping
keys and scalar tag text are counted before allocation/encoding, with depth,
node and byte budgets. Model values cannot open files or URLs.

## Qualification

- An independent private runner of the immutable utils YAML/tag/merge helpers
  generates 38 conformance fixtures. It uses the provider's pinned YAML dependency
  only outside the repository. The CVK model/exporter race suites pass those
  fixtures plus binding authorization, snapshot changes, alias independence,
  ambiguity and expansion-limit cases. No new project dependency is added.
- Public reconciliation and real temporary Kubernetes API persistence tests pass
  the normalized hierarchy/image/AP source through defaults, bound selection,
  private custody and original-task recovery. Runtime raw-alias refusal remains
  in place; environment interpretation occurs only in explicit offline import.
- The final isolated native design cycle passes eight original-task recoveries,
  four setup mutations and six design writes including two synchronous image
  uploads. Scope exclusion/re-selection preserves the journal with no extra
  writes. Exact AP cleanup precedes hierarchy cleanup; typed native inventory
  baseline restored, no active operation or generated fixture left. Native
  Kubernetes persistence is simulated, not installed qualification.
- Six ND/shared configuration/managed protocol/maintenance/software lifecycle/
  direct-device/SWIM packages pass regression tests. Model/tool suites cover the
  changed offline code; the runtime parser and execution implementation were not
  changed by this source-mode addition.
- The file-based example emits matching environment-bound model/default snapshots
  and feeds the existing site exporter. Migration and production builds pass.

See [machine-readable evidence](yaml-source-resolution.json) for exact source
fingerprints, fixture digest, test counts, native result and gate log hashes, and
[source usage](../../controllers/catalyst-center-model-merge.md) for the CLI.

## Limits and review corrections

Reference cases identified non-specific-tag, custom-key and explicit flow-timestamp
behavior; those were matched before qualification. Final review added early
expanded key/tag byte accounting and reran the affected gates. Both native
qualification cycles were cleaned; completed private checkpoints must not be
replayed.

This is not complete provider/parser equivalence or schema validation. Compound
and extension tags outside the qualified fixtures, per-resource scalar/default
coercion, installed source composition, native schema/catalogue coverage and the
remaining creator/adoption/destructive and physical/bulk flows remain pending.
No report or successful preprocessing is counted as resource parity.
