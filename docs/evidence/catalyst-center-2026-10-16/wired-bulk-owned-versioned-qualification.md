# Versioned credential public reconciliation with a real API and Catalyst Center

The [combined qualification log](wired-bulk-owned-versioned-api-native.log) passes
in 89.47 seconds. It exercises all six pinned NetAsCode credential types through
the existing public CVK reconciler, actual private Kubernetes API persistence,
authorized per-key projection layout, native Catalyst Center requests, and the
existing owned bulk prerequisite observer in one process.

The types are CLI, HTTPS read/write, SNMPv2 read/write and SNMPv3. The test creates
six generated, unassigned credentials and rotates every declared credential leaf
from version 1 to version 2 on the same native UUIDs. The eight projected leaves
include both CLI password/enable and SNMPv3 authentication/privacy fields. Source
contains metadata and explicit counters; generated values are placed only in
private mode-600 test projection files under mode-700 directories. The path is
computed by the common IntentSecretRelativePath helper, and the producer uses
the existing resolver/journal rather than a separate credential writer.

## Verified behavior

- The actual CRDs assign controller/config UIDs and generations. Source aliases
  conform to their real admission rules. Applied UUID/version descriptors are
  persisted by the public reconciler after native task verification.
- Each type receives exactly one native create and one native rotation. The
  transport permits only the exact generated payload, expected native UUID and
  one create/update pair per type. Site assignment and device API writes are
  denied. The credentials remain unassigned throughout this run.
- A declared version increase with old projection files submits zero rotations.
  Updating every leaf of the first selected credential while leaving later
  definitions stale also submits zero rotations. Whole-selection preflight is
  therefore exercised against real native requests, not only local fixtures.
- Supplying all version-2 projections converges through the existing asynchronous
  journal, maintains native UUIDs and replaces the six applied-version receipts.
  Three restarted reconciliation no-ops per phase cause no mutation replay.
- The bulk consumer uses the same real API owner, current declarations, source
  authorization and persisted applied receipts. Its projection root is explicitly
  nonexistent. Dependency identity remains stable for unchanged input; the old
  version-1 observer is refused after owner rotation, while the version-2 observer
  succeeds. This does not create or rotate credentials from inventory.
- Both controller and config status are scanned for every generated value from
  both phases; none are present. No Kubernetes Secret API objects are created.
  The connection client's private authentication is supplied by the existing
  lab launcher and is separate from these synthetic intent credentials.
- Exact, independently observed generated UUIDs are deleted only after settled
  reconciliation and unchanged public native metadata. Task completion and the
  complete typed non-secret credential baseline are checked after cleanup.
  All six generated credentials were verified removed.

Only ephemeral memory and private projection files contain the generated values.
The trace compares request payloads without logging or hashing credential material.
If an uncertain operation occurs in a future run, the test retains generated
public identities and the existing journal in a private diagnostic file with
all known generated values redacted; it does not repeat an uncertain mutation.
No such retained fixture or diagnostic was needed in this passing run.

## Scope and remaining gates

The API server is private Kubernetes v1.35.0. Reconciliation is driven by the
qualification process, and projected files are supplied by that process using
the common layout. This does not qualify installed worker startup, Secret-source
projection by the manager, RBAC/actor separation, the installed cluster version,
actual device authentication or physical bulk provisioning. The source inventory
row supplies dependency context only; no device is looked up, claimed or changed.

The test adds no production protocol, secret store, transport or credential
executor. ND, direct-device and SWIM structures are unchanged. Public bulk
activation and complete rendered device convergence remain pending. Credential
prune/handoff and broader model execution gaps are not closed by this test.
All resource and whole-section parity flags remain partial.

This extends the [combined metadata-only qualification](wired-bulk-owned-credential-qualification.md)
and [owned credential dependency implementation](wired-bulk-owned-credentials.md).
