# LAN ADD acknowledged recovery and atomic completion

## Implemented

New ADD operations emit a private v2 resource identity with separate logical,
immutable recovery and complete pre-dispatch fingerprints. The observed IPv4
baseline must be empty in endpoint and topology records. Previously verified addressed
ports now have a private [owned-result observation](lan-link-owned.md) path;
unowned addressed ports and legacy receipts still require explicit adoption or
requalification. The original
admission and all-member group digest still bind the complete v2 operation.
No new status fields, CRD or transport were introduced.

The recovery binding retains device/serial/software, exact physical port
UUID/name/index/type, discovery session/site/serial identities and the original
pool/global-parent metadata. Expected ADD outputs such as allocated addresses,
port/routing state, refreshed topology record ID, discovery allocated-address
lists and pool usage are excluded from that immutable key. Current complete
observations remain bracketed and stable around final proof.

The original source link is selected by its logical digest and native endpoint
and port identities. Retained task recovery does not impose another unexecuted
link's mutable readiness prerequisites on the acknowledged action. Unknown work
without an acknowledged task stays fenced. Legacy v1 identities remain reserved
and recognizable, but cannot infer a baseline or migrate accepted scope.

## Public acknowledgement recovery

The public reconciler now observes retained Accepted/Unknown/Submitting/Verifying
LAN ADD operations before new or unsupported execution compilation. It reuses
complete acknowledged claims, actor authorization, source/owner/journal guards,
rooted native task-tree completion and repeated native/managed identity reads.
Accepted or acknowledged Unknown can enter Verifying without another native
update. Missing, malformed, changed or revoked evidence leaves the operation and
both reservations retained. Submitting without an acknowledgement reports an
unknown outcome and cannot replay.

This path does not activate public new writes. Native result readback without an
installed independent device verifier reports Waiting with an explicit proof gap.
It cannot manufacture a Verified receipt or release either endpoint. The subsequent
[device-worker proof bridge](lan-link-device-proof.md) now supplies a concrete
verifier for retained Verifying work, with installed qualification still pending.

## Private final proof and receipt

Before independent proof, both native physical interfaces must be up with unique,
usable IPv4 addresses in the same valid subnet within the original LAN pool.
Masks must be contiguous; network/broadcast addresses (except valid /31 hosts),
management aliases, topology disagreement and out-of-pool results are refused.
These checks are not ISIS/device configuration convergence proof.

The existing independent group-proof bracket receives private copies of the
original operation, complete endpoint footprint and exact task completion time.
Source, controller, native interface, pool, managed identity and completion
changes prevent publication. Completed work may consume the last free pool
addresses: completion does not reapply a new-ADD capacity threshold.

The existing atomic group writer retains Verified and the `lan-link:<logical>:<result>`
action receipt together. The result suffix binds the verified native interface
configuration; it excludes usage and topology cache identifiers. Source and owner checks precede proof. Publication
handles a lost response without another native update, and existing predecessor
replacement rules preserve retained earlier reservations. Shared completion
publication can follow; worker/manager settlement still controls release and
journal retirement.

## Validation

The focused race suite passed in 15.394s; see `lan-link-recovery-focused.log`.
Fixture cases cover public accepted/unknown recovery, running/failed/future tasks,
source/pool/port/physical identity changes, revoked or pruned claims, lost native
acknowledgement, missing/denied proof, missing/invalid native results, source/native/
managed/completion races, exhausted-after-completion pools, missing members,
authorization refusal, atomic lost-response persistence and shared completion
publication without premature settlement. Legacy format and addressed-baseline
refusals are included, plus /31 and /30 host/subnet and exclusion boundary checks.

Native HTTPS, Kubernetes authority, actors and proof decisions are fixtures.
They are not installed actor or real-device configuration attestation.

Fresh authenticated live reads passed: maximum sessions 5, active 0, three topology
nodes and two exact reread-qualified existing lab adjacencies. Session history
remains empty. No LAN ADD or switch mutation was submitted. See
`lan-link-recovery-live-prerequisites.log`.

## Remaining scope

An actual installed independent device verifier, public new-write activation,
owned add idempotency/adoption and replacement, explicit delete ownership/recovery,
full source sequencing/composition, installed worker/manager/Lease qualification,
shared/cross-site allocation and concurrent-session semantics and accepted native
LAN execution remain pending. Legacy accepted work requires investigation rather
than inferred migration. The overall pinned-model goal remains incomplete.


## Final regression

The complete affected race suite passed for Catalyst Center, its NetAsCode parser,
shared controller-configuration contracts and Nexus Dashboard. See
`lan-link-recovery-regression.log`. Strict documentation and whitespace checks
also pass. Real accepted LAN execution, an installed independent verifier and
worker/manager settlement remain unqualified; full parity is not established.
