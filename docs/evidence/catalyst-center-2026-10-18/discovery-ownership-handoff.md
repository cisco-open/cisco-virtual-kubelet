# Discovery ownership handoff with retained history

New pure-discovery Delete-policy configs can hand the inventory section to a
successor after their own Kubernetes UID has disappeared. The deleting worker
archives `DiscoveryOwnershipRetirement` in the existing controller conditions
only after the ordinary repeated native completion observation and fresh
source/spec/evidence checks. The record binds the original completion proof,
owner evidence and a fixed-size digest of all qualified deletion tombstones.
Owner reservations and every action receipt remain until a successor claims them.

The successor must be independently manager-enrolled, carry a fully validated
pure-discovery source and observe an idle journal. It checks the original UID is
gone, verifies canonical retirement evidence and all historical receipts, and
reads native inventory twice to prove the retired UUIDs are absent. Unchanged
source/controller snapshots and the existing status CAS fence the owner transfer.
Handoff sends no native request and prepares no operation; ordinary planning,
preparation, submission guards and task verification run on later reconciles.

The successor can reuse both config and job names. A new incarnation retains its
own task/UUID receipt alongside predecessor deletion receipts. Historical native
IDs never become its ownership. Planning and verification reject a reappearing
retired UUID; receipt publication preserves historical rows and existing capacity
bounds. Every later completion carries the full deletion-history digest forward,
so repeated replacements do not relabel or erase audit evidence. Foreign imported
work keeps its previous observation/ownership behavior and is not retired or
adopted by this mechanism.

## Validation boundary

The controlled gate exercises public entry, same-name replacement, original UID
still present (including an unrelated keeper after cleanup), malformed/missing
proof, altered/pruned receipts, busy journals, native/source/controller/evidence
races, final CAS conflict, API read errors and unsupported manifests. Post-handoff
corruption and old UUID reappearance refuse new intent. A three-successor cycle
creates, verifies, no-ops and deletes the same logical job while preserving every
earlier receipt. Manager protection/enrollment occurs before worker attestations,
matching the installed lifecycle order.

The same handoff cases pass with a real Kubernetes API server, along with the
existing public deletion and completion families. Manager cache indexes are
emulated in this isolated harness. The full discovery regression group passes;
an initially overbroad history check was corrected to preserve existing imported
and foreign-job behavior. Controller registry, ND, software-upgrade and direct
gNOI package gates pass, and the production binary builds.

Native qualification uses real appliance tasks and exact-ID writes with an
allow-list for one generated single-address job targeting `198.51.100.101`.
Kubernetes persistence and actor restart are test fixtures. Its predecessor cycle
retains the existing injected Unknown check; the successor uses a naturally
expired acknowledged task with no status fault injection. The native guard allows
at most two creates and two exact deletes and refuses unrelated mutations.
Terminal task/UUID/count evidence and final test output are recorded in the
[validation record](discovery-ownership-handoff.json) and
[native output](discovery-ownership-handoff-native.log).

## Remaining qualification and compatibility

The preceding retained-source/site-bulk full race suite passed 13,561 test/subtest
events in 1676.277 seconds. That is explicitly a **pre-handoff** source boundary.
The complete handoff-source race audit subsequently passed 13,593 test/subtest
events in 1688.248 seconds with no failures. Its
[terminal record](discovery-handoff-full-suite.json) pins production source
`5d36bf0dfde744b2b1e5894f6f736213134f628629a50abf4be784086b357ce2`;
it predates the subsequent optional-null parser changes. Hosted CI remains unqualified.

The earlier installed source-loss test did not establish installed handoff.
Its already-deleted config has no retirement attestation and remains fenced;
this implementation does not manufacture one or erase its retained owner.
A subsequent [installed two-incarnation qualification](discovery-installed-handoff.md)
now passes in a fresh namespace, with natural task timeout, source loss and worker
restart in both cycles. That installed run is separate from this native harness.

Automatic handoff is limited to newly attested pure-discovery Delete-policy
inventory ownership. Legacy records without that proof, controller-generation
changes, mixed/grouped/template custody, Retain-policy removal and complete pinned
model/schema/default parity remain open. Receipt retention remains subject to the
existing count/size limits; history garbage collection is not implemented.
