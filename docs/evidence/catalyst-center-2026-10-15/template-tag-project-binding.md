# Template tag association project binding

The shared template association executor now retains the native project
incarnation across submission and worker reconstruction. Previously the complete
pre-submit observation included the project UUID, but the persisted operation
retained only the template UUID, tag UUID and name-derived association key.
Recovery could therefore lose the original project binding.

## Implementation

The new `template-tag-association:v2:` operation key binds the logical
project/template/tag selection and native project UUID with SHA-256. Only
non-secret identity metadata enters this digest. The existing operation's
`targetID` and `parentID` continue retaining template and tag UUIDs. The public
compiler's `members/` group prefix is unchanged; combined discovery models retain
their existing additional `templates/` prefix.

This uses the existing immutable resource field and shared journal. It does not
add a transport, credential store or CRD field, or widen the dependency-version
graph reserved for composite physical deployments. Device associations and
network-profile assignments keep their existing operation formats.

Pre-submit checks still reobserve the entire selected source twice through the
one-use association writer. Recovery reconstructs the selected association from
the persisted binding and current project inventory, then repeats native
project/template/tag/membership observation twice. It reads only the acknowledged
tag's membership rather than every unrelated declared membership. A project
replacement or move cannot be accepted by matching names or template UUID alone.

Legacy acknowledged associations have no original project binding to recover.
They are retained rather than silently rewritten or replayed. Canonical legacy
public `members/` journals cannot bypass this restriction through generic
`Verified` release. Public status reports `OperationRecoveryRequired` with the
missing-binding explanation. An unsubmitted legacy `Prepared` operation can still
be cancelled and replanned because it never received a submission permit.

## Fixture evidence

- Serialized shared-journal Accepted recovery with a freshly constructed
  executor verifies the original association and permits normal release.
- Same-name project replacement with the same template UUID is refused.
- Project replacement during readback, template/tag UUID replacement, absent
  membership, malformed keys and legacy keys cannot verify or release.
- A lost native acknowledgement retains `Unknown` and makes no second request.
- Legacy `Verified` release is refused under both public group layouts; public
  reconciliation emits recovery status without a native request.
- Legacy unsubmitted preparation can be cancelled.
- A two-tag source verifies one acknowledged association with exactly two
  membership reads and no reads of the unrelated association.

The initial affected race suite passed in 4.695 seconds. A broader template/tag,
journal, credential, dependency and SWIM race suite passed in 63.470 seconds.
Final focused and native qualification results are retained in the adjacent logs.

The final affected race suite, including the bounded observer and public legacy
recovery status, passed in 4.590 seconds. The strict documentation build and
whitespace checks passed; the ledger still matches the exact 93 pinned resource
types. `template-tag-project-source-fingerprints.json` records the final focused
and native implementation sources.

## Native qualification

The existing isolated project/template/tag harness was extended to serialize the
operation identity and reconstruct the compiler before association verification.
It asserts the versioned project-bound resource for the association phase.

The first native run passed in 37.30 seconds (package 39.196 seconds): project
create/update, template draft create/update, two commits, tag creation, additive
association, fresh-compiler verification and converged no-op. No template was
deployed to a device.

Its owned association, tag, template and project were removed after independent
readback, using these captured UUIDs:

- Project: `7524d7db-f28c-4aa4-ba04-d14cc01aee50`.
- Template: `1307ba47-1ba5-4e29-9332-35d5e75b3755`.
- Tag: `5811495b-c230-4b90-b08e-ed1e62e06f40`.

The final native run also qualified the bounded recovery observer and passed in
32.97 seconds (package 34.856 seconds). Its association/tag/template/project
cleanup was verified using the following captured UUIDs:

- Project: `a3539aeb-4933-44d9-8c01-1bd3868685fc`.
- Template: `c3f2e94b-8f03-447c-aa13-c32a64f06e19`.
- Tag: `dfb9c05e-6488-4218-bef0-4e7d9e6ce680`.
- Association task: `01a14003-9ad0-70fa-93b0-11cee31ff292`.
- Association removal task: `01a14003-b7d2-708a-b3d4-fea0266a4393`.
- Tag removal task: `01a14003-bcd6-7e98-bc3b-551ed5043d62`.
- Template removal task: `01a14003-c2be-766b-9d84-f5a81e39debf`.

Complete task and cleanup evidence is in `template-tag-project-native-final.log`.

## Remaining scope

Native qualification covers the compiled executor with serialized operation
identity; shared Kubernetes persistence/recovery is fixture-tested. Installed
manager/worker and real API-server end-to-end qualification remain pending.
Automatic evidence-based repair of legacy acknowledged associations, ownership
for removal/pruning and protected-tag mutation remain unimplemented. This closes
the in-flight project-incarnation binding gap, not full tag or NetAsCode parity.

Subsequent [manual association qualification](template-tag-association-provenance.md)
retains the same two-observation recovery bound while classifying membership.
Each observation now issues STATIC and MIXED queries, for four scoped membership
requests rather than the two unfiltered requests at this checkpoint.
