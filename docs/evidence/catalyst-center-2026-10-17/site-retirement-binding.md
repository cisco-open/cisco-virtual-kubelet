# Staged site-retirement ownership and dispatch binding

The staged binding follows the [qualified dependency reader](site-retirement-dependencies.md)
and uses the existing projected comparison key, source snapshots, section owner,
creator/latest receipts and common configuration journal. It exposes a read-only
candidate and preflight checker, **not a `configurationWrite` or public Delete**.

The source/gate results on this page describe the earlier staged v1 selector.
The [v2 task/absence observer](site-retirement-recovery.md) now adds recoverable,
cross-bound ancestry proof. Historical v1 evidence is preserved; it is not
promoted to v2 or installed public-deletion qualification.

## Authority and privacy

A candidate requires one private sites Apply owner, explicit prune Delete with
config deletion Retain, an idle controller journal, and a site omitted from the
complete supported source. Global, unowned/legacy creators, wrong key scopes,
foreign owners, stale input and retained image/AP/credential effects are refused.
The native target UUID, parent, type and path hash must match the creator. The
existing private native-baseline check validates the creator or latest applied
state before the dependency reader observes the unused leaf.

The selector contains kind, path hash, source mode, existing domain-scoped key
identity, creator/latest receipt digests and a retirement-domain native MAC.
The separate retirement MAC domain includes the complete selector and full native
target/ancestor footprint. Creator/update MAC domains are unchanged. Paths,
attributes, credentials and key bytes are not published. Candidate formatting is
redacted. The existing projection authorization snapshot is shared with common
journal preparation; no Secret API read or separate credential store is added.

The candidate owns copies of config/controller authority and parsed source bytes.
Its checker requires the exact common `Submitting` operation: resource, action,
target/parent, source UID/version, generations, owner and no earlier task receipt.
It rechecks the complete retained-history digest and the private native baseline
around two fresh dependency/native observations. Source, owner, key, history,
controller, phase or native changes fail closed. It is a read-only preflight;
future submission must still use the existing one-use dispatch wrapper.

## Verification

- [Local binding scenarios](site-retirement-binding-local.log): **62** cases,
  inline and ConfigMap sources, **5.629 s**, race enabled.
- [Temporary real Kubernetes API](site-retirement-binding-api.log): **14** cases,
  **10.120 s**, race enabled. These exercise persisted selector/source binding,
  generations and late owner/journal/native changes. The fixture starts a fresh
  actor identity after its projection setup changes controller generation; the
  production stale-worker fence is unchanged.
- [Private-site/image/dependency regressions](site-retirement-binding-regression.log):
  **39** families / **358** subcases, **34.584 s**, race enabled. This includes
  strict selector/MAC domain checks and the existing private lineage/image cases.

The [source and gate audit](site-retirement-binding-validation.json) keeps this
stage separate from earlier lab evidence. No new lab site/device mutation was
performed here, and no installed public retirement result is claimed. Tests
explicitly confirm the normal sites Delete admission gate remains closed.

## Remaining lifecycle work

Public policy/compiler integration, additional operational/settings ownership
protection, native Delete submission and public integration of the now
[locally/API-qualified task/absence observer](site-retirement-recovery.md),
atomic retirement publication and bounded
recreation remain to be implemented. Config deletion and repeated source
retirement/reintroduction also need installed qualification. The binding alone
cannot retire custody, erase a creator receipt or convert a dependency read into
permission to delete. Full site lifecycle and 93-resource parity remain incomplete.
