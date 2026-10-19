# Native feature state capture — 2026-10-16

Public FRA/CleanAir Create and owned Update publication now retains a complete
native-state fingerprint. This closes a prerequisite for safe source pruning:
previous desired-effect hashes proved the manifest's declared attributes, but
could not detect later changes to omitted descriptions, interferers, unlocked
attributes or future native fields. Pruning itself is still unimplemented.

## Protocol and compatibility

The existing typed readers now also expose a state-returning form. Public result
verification collects the digest from the **same native detail response** that
passes typed desired-field verification and task/kind/name/UUID checks. It does
not fetch a second object that might already contain an unrelated edit. Existing
reader signatures and native validation remain available to the other planners.
There is no additional detail GET for fingerprint acquisition.

Canonicalization covers the full returned detail object. Object key order and
whitespace are ignored; array order and numeric spelling remain significant.
JSON numbers retain their textual precision instead of passing through float64.
Duplicate keys at any depth, non-object roots and trailing JSON values are
rejected. Only the SHA-256 digest is retained; unknown native values are not
copied into Kubernetes status. The digest does not replace typed source matching.

New `feature-created:v1` and `feature-applied:v1` result resources append a colon
and 64 lowercase hex characters after the existing native UUID. Operation intent
markers and CRDs do not change. Identity validation accepts the original UUID-only
receipt form and the extended form, refusing malformed suffixes. UUID-only legacy
records retain their existing ownership/recovery semantics, but have **no** native
state proof and cannot authorize future fingerprint-based pruning. No legacy
record is retroactively fingerprinted from matching metadata.

Update publication replaces the matching logical applied row, including its
previous fingerprint, rather than appending a new row for every native-state
change. The creator record remains immutable. Capacity and receipt-required
release guards remain in place. Missing a fingerprint does not invalidate legacy
ownership; a malformed fingerprint is invalid evidence. A successful receipt is
still a historical observation, not continuous native-drift protection.

## Verification

- Final fingerprint/receipt/result/restart safety checks pass 10.858s:
  `/tmp/cvk-feature-state-final-focused.log`.
- Feature/shared-journal/discovery/IPAM race regression passes 38.566s:
  [regression log](wireless-feature-state-regression.log).
- Actual CRD/status persistence, field transitions, root/group acknowledged-task
  recovery and release corruption/pruning cases pass 38.255s:
  [API persistence log](wireless-feature-state-envtest.log).

Tests prove equivalent key ordering hashes identically; distinct integers
9007199254740992 and 9007199254740993 remain distinct. Unmanaged-field removal,
array changes, duplicate nested keys and malformed/trailing JSON cannot silently
produce equivalent state. The observed-result fixture performs one detail GET,
includes unknown native attributes and unlocked fields, and refuses source drift
or ambiguous unmanaged data without returning a successful result/fingerprint.
Public recovery tests require native fingerprints on both creator and applied
records and preserve original creator rows through subsequent revisions.

The API servers are private and ephemeral; native services in those tests are
fixtures. They do not use or repair the stopped installed Kubernetes cluster.

## Live qualification

The [native run](wireless-feature-state-native.log) passes seven combined manifest
revisions in 32.21s (race package 34.134s). Every revision compares each stored
creator or latest applied fingerprint with native readback: **14 comparisons**.
Two acknowledged Creates and twelve acknowledged Updates are serialized as
Unknown and recovered through their original tasks without resubmission.

| Definition | Native UUID | Original Create task | Latest Update task |
| --- | --- | --- | --- |
| `CVK_MB_16045600` | `0532568b-0faa-4a27-81ab-c8dad3459c1e` | `01a14311-30b4-7e26-b4b9-b4ceb7db5bbd` | `01a14311-9956-7b30-bce6-b76a9e142f74` |
| `CVK_MB_16045600_FRA` | `3676fb13-6f2a-496f-8ee0-90241fa47694` | `01a14311-2b1e-7cfe-a6c9-142f2a2dbb97` | `01a14311-9273-77fa-b2aa-c22a70dfb037` |

Private evidence is under
`/private/var/folders/w5/1l6hg4ks3wvcdv_bkztxwtyw0000gn/T/cvk-cleanair-manifest-173821386/`.
`creator-records.json` contains two generation-2 original creators and two
generation-8 applied Updates, all with 64-character state fingerprints.
`complete.json` records nativeStatesChecked=14, knownTaskRecoveries=14,
creatorRecords=2, appliedRecords=2 and cleaned=true. Per kind, transport counts
are exactly one POST, six PUTs and one qualification-only DELETE.

Cleanup tasks `01a14311-a129-7ede-aa3a-1f4312f3a2af` (CleanAir) and
`01a14311-a281-755f-9097-4ca71be227a7` (FRA) finish, removing only generated,
unused definitions and restoring both summary baselines. This teardown does not
publish tombstones or qualify manifest-driven deletion. The native lifecycle
uses a Kubernetes client fixture; actual API persistence is separately qualified
above. No WLC/AP deployment or installed-worker execution is attested.

## Remaining pruning work

Feature `prunePolicy: Delete` remains unsupported. Complete wireless-profile and
deployment reference evidence, repeated final native-state/owner/source checks,
Delete task/UUID/absence verification, atomic tombstones, retained uncertainty,
source reintroduction/recreation and installed-worker qualification are still
required. Existing legacy receipts without state fingerprints need an explicit
recovery/adoption policy rather than silent promotion.

Cisco documents the native feature Delete APIs in its
[3.1.x platform changes](https://www.cisco.com/c/en/us/td/docs/cloud-systems-management/network-automation-and-management/Catalyst-Center-Platform-Documentation/3_1_x/user_guide/b-cisco-catalyst-center-platform-ug-3-1-x/m_new_and_changed_ug_3_1_x.html).
The existence of those endpoints is not proof of complete dependency discovery or
safe deletion of a deployed feature. No automatic deletion or whole-resource
parity flag is enabled by this stage.


The subsequent [public pruning stage](wireless-feature-pruning.md) implements
section-only source deletion and tombstones using this state evidence. The
remaining-work section above records the boundary at the state-capture stage.
