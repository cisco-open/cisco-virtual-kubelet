# Bulk-site recovery binding qualification

The native bulk primitive now shares canonical member ordering with a
journal-compatible recovery descriptor. A compact source-bound bitset retains
the exact sites absent at preparation. The descriptor digest binds ordered
logical identities and all pre-existing declared-site/ancestor UUIDs. It carries
neither attribute payloads nor credentials. A 1,000-member selection fits the
existing 512-character operation resource field without schema changes.

Recovery reconstructs the original selection from the unchanged source and
retained descriptor, not from the sites still missing after a partial result.
Readback refuses replaced existing members or ancestors and cannot
verify until all declared attributes converge. Parsing rejects zero selection,
noncanonical counts/base64/digests, out-of-range bits and oversized resources.
Source revision and owner authorization remain responsibilities of the existing
configuration journal; this helper does not grant submission authority.

Fixture tests cover each partial result, reconstructed selection, existing
member/root replacement, malformed descriptors, maximum-size selection and
changed logical source. The existing singular-site manifest restart regression
also passes. The native planner reuses this canonical ordering.

On 2026-10-14, a fresh isolated native bulk cycle created
`Global/CVK-Q-20261014-142816-000000000`, its building and floor. The test verified
the retained member/anchor descriptor, the existing shared task-tree observer,
complete hierarchy readback, a normal floor-width update and no-op convergence.
Only verified fixture identities were deleted; cleanup passed. Live test 10.72s,
package 11.566s. See [live output](site-bulk-binding-live.log).

The earlier completed native task was independently inspected without printing
free-form task data/progress. Its returned task tree contained one completed
root with the expected root binding. This qualifies the observed native task
shape, not additional unseen child work or deployed Kubernetes actors.

Manifest submission, durable descriptor persistence, owner/source checks at
dispatch, failed-task retention, unknown submission handling and full public
restart recovery are still pending. The native helper and its descriptor are
not yet a public bulk capability, and no full resource parity is claimed.
