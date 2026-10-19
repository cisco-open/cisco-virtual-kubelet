# Credential-bearing discovery: retirement audit preparation

Branch: `pr/johalley/catc-support`. NetAsCode module pin: **0.5.0**.
This stage prepares ownership and recovery evidence for replacement. It does not
claim that credential-bearing replacement execution is enabled or lab-qualified.

## Implemented boundary

- Successor input identities retain the semantic public/version binding and the
  exact predecessor retirement receipt digest. Earlier semantic inputs cannot
  overwrite a previous incarnation's credential application.
- The existing shared action/credential audit retains every creator pair and
  retirement. Validation checks one rooted chain per name, exact native UUIDs,
  unique relevant operation IDs, ownership, timing and generation order, and
  complete matching credential evidence. Missing roots, retirement links,
  application receipts, forks and reused native UUIDs are refused.
- Each historical transition reuses the common credential source/version rules
  against ancestors only. Removed fields retain their high-water counters;
  reintroduction must advance them. Hash-consistent history cannot excuse an
  earlier rollback or same-counter source change.
- Pre-delete admission reserves two action receipts and one credential application
  using the existing count and serialized-byte budgets. The candidate budget is
  calculated on a detached copy. No synthetic receipt is persisted or installed.
- A retirement binds queued versions without delivering them. Generic credential
  publication, generic verification and generic slot release refuse this family.
  New creation completion also checks full retained history after persistence.

## Qualification scope

Controlled tests construct audit records only. They cover linked replacement,
removed-field reintroduction, source changes, historical rollback, missing or
modified proof, operation and UUID reuse, forks, misleading delivery receipts,
retired UUID reappearance, changed queued input, malformed contextual identity,
and action/credential count and action-byte capacity. Existing discovery and
shared credential/journal paths are regression-tested separately from native
execution. Machine-readable results and source hashes accompany this record.

## Remaining execution work

The public executor still refuses changed credential intent. It must select the
latest creator rather than treating historical incarnations as a conflict, then
connect exact-owned-UUID/public-fingerprint deletion to the shared one-use
submission permit. Dedicated successful-task and repeated UUID-absence checks
must publish retirement atomically before the successor may be dispatched.
Known-task recovery, uncertain outcomes, source changes, lost acknowledgements,
retained-source cleanup, pruning and deletion require end-to-end qualification.
Actual device CLI/SNMP credentials and installed/native credential delivery are
still unqualified. Restored API access and read-only health/SWIM inventory checks
do not prove these mutations. Full pinned-model parity remains incomplete.

## Recorded results

- Final-source discovery/shared-journal race and Kubernetes API regression: PASS,
  108.779 seconds, 1,460 passing test/subtest events.
- Shared controller contract, Nexus Dashboard, upgrade coordination and
  maintenance checks: PASS; upgrade package result was cached.
- Complete CVK build and strict documentation build: PASS.
- Current-source native read-only health/device/SWIM image inventory: PASS,
  three devices and two images. No native device mutation was attempted.

See [machine-readable qualification](discovery-credential-retirement-audit.json),
[regression log](discovery-credential-retirement-regression.log),
[foundation log](discovery-credential-retirement-foundation.log), and
[native read-only log](discovery-credential-retirement-native-read.log).

## Follow-up

The [replacement execution stage](discovery-credential-replacement.md) connects
latest-creator selection, guarded Delete, absence retirement and contextual
successor dispatch. The results above remain scoped to this earlier audit stage;
they do not qualify the later executor or native credential delivery.
