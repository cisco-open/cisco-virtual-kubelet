# Site recreation history protocol

The protocol follows the existing cohort-archive pattern: original creator,
optional latest-update and verified retirement rows remain intact, with their
Resource keys retagged under `site-history:v1:<cohort-digest>:`. Operations,
acknowledgements and verification times are preserved. A
`site-private-recreate:v1:` selector binds the original private identity scope,
predecessor retirement operation ID and complete cohort digest.

## Structural authority and history

The candidate must use a later source/generation, the same config owner and
comparison-key scope, nondecreasing controller generation and the same parent.
The archive builder changes only a copy. It refuses unsupported phases, a changed
operation, incomplete authority, oversized archive keys or exhausted row/byte
bounds. That copy is intentionally invalid until a verified successor is added;
it is not permission to persist a partial archive.

Archive readers reconstruct each full lifecycle using the same core validators
as current rows. Public creator, applied and retirement readers additionally
validate the complete history. Every archived cohort must be reachable from a
current successor through exact predecessor digests. Missing creator/update/
retirement rows, orphan archives, unknown versions, changed timestamps or
producers, malformed roots, stale inputs and key/owner/parent mismatches fail
closed. Tasks cannot be reused across descendant lifecycles, and historical
native UUIDs cannot be reused by a successor or another archived cohort.

Internal Current readers separate structural validation from whole-chain checks
and avoid recursive archive validation. Archived operations stay out of current
producer maps. Existing private create/update MAC domains and receipt formats
remain unchanged. No native attributes, paths or credential values are added to
status. The existing action-row and key-size bounds remain in force; no old
cohort is silently discarded to make space.

## Qualification

The [final local race regression](site-recreation-protocol-regression.log) passes
**59** families / **813** subcases in **74.939 s**, including existing private
site, image, singular dispatch/task and retirement cases. New protocol fixtures
cover two consecutive recreation cycles with and without latest-state evidence,
all three history-aware readers, partial staging refusal, older task/UUID reuse
with recomputed public checksums, and an inline-to-ConfigMap successor frame.

The [real temporary Kubernetes API run](site-recreation-protocol-api.log) passes
**81** scenarios across history and retirement families in **51.256 s**. Eight
new history cases verify complete two-cycle serialization through the common
receipt persistence helper and rejection of dropped/edited history. These are
structural area fixtures with synthetic native proof values; they do not qualify
native creation or real ownership adoption. The
[source/gate audit](site-recreation-protocol-validation.json) records the exact
qualified boundary.

## Remaining integration

No new lab mutation occurred here. A production recreation writer is not yet
enabled. Native absence/ancestry planning, guarded submission, original-task
recovery integration, capacity admission and atomic archival in the production
creator publisher remain required. Public policy/compiler routing, full consumer
lifecycle checks, parent/child retirement coordination, config deletion and
installed complete-cycle qualification are also pending. Existing source
reintroduction remains fenced until that integration exists. Full site lifecycle
and 93-resource parity remain incomplete.


## Subsequent publication integration

The next change connects archive capacity and atomic predecessor/successor
publication to the production creator writer. Its separate
[qualification boundary](site-creation-publication.md) supersedes only those
remaining integration items above; native planning/dispatch and installed
complete-cycle qualification remain pending. The historical protocol gate and
source hashes above are unchanged.
