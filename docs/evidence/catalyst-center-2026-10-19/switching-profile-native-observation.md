# Complete private switching profile observations

Pinned NetAsCode **0.5.0**, revision
`a435555fe828f03cf3311d52fe1ab6572a247916`, branch
`pr/johalley/catc-support`. Full resource and model parity remain incomplete.

## Execution change

A populated detail response now supplies both the existing typed profile and a
complete private native observation from the same GET. Unknown top-level and
attribute fields are preserved. Typed UUID/name/namespace/population checks remain
in place. The public reader delegates to that observation rather than issuing a
second request whose state could differ.

The existing submission guard and strict created-result stability check compare
complete selected native observations. The metadata recovery observer also keeps
this private state on both sides of original task completion. An unknown native
edit no longer disappears merely because the fields understood by the model did
not change. Pre-submit refusal still uses the same one-use proof and journal CAS;
acknowledged uncertainty retains the existing task/recovery contract.

Canonicalization sorts unique-key profile attribute groups, qualified CLI/day-zero
template membership sets and the already supported unique-key inherited attribute
envelopes. Unknown values and opaque array order remain exact. JSON numbers retain
precision, and inherited bytes used for preserving updates are left untouched.
Missing/malformed populated state, duplicate groups and duplicate/invalid template
members refuse comparison. Private observations redact normal/debug formatting
and expose no fields through JSON serialization.

A protected comparison helper reuses the existing approved projection/key identity,
with its own native-state HMAC domain and explicit binding, name and UUID context.
Source/projection checks run before and after comparison. Missing/short/invalid
keys, missing checks and changed projection material refuse rather than falling
back to an unkeyed digest. Raw native values and ordinary hashes of possible
secrets never enter status, errors, logs or public dependency identities.

## Qualification

Fixtures cover unknown native edits, semantic object/attribute/template ordering,
opaque arrays, unknown membership-item fields, integer precision, malformed detail
state, privacy, key/domain separation and projection refusal/change. Existing
switching guards also require a no-dispatch proof for an unknown native edit.
A separate metadata-observer case proves unknown fields survive into repeated
recovery observations.

The 62 existing switching custody and recovery cases pass with real temporary
Kubernetes API persistence. Native public reconciliation uses a generated 32-byte
key through the existing approved `/network_profiles` projection, not the appliance
credential. Complete observations include ten native fields and keyed comparisons
remain stable after Create, empty-area assignment and both membership revisions.
Creator receipts remain immutable; automatic preflight recovery, three original
Unknown-task recoveries and restarted no-ops pass. Generated profile/assignment/
area, template and project cleanup completes. No real device or template deployment
is targeted. Native Kubernetes persistence is simulated.

The focused race gate passes 563 test events; the final private-observation gate
passes 33. The broad regression passes 19,903 events across sixteen packages,
including ND, shared configuration, maintenance, direct-device and SWIM paths.
The final metadata-observer assertion was added as a test-only strengthening and
is covered separately; production sources were unchanged. Production worker and
strict documentation builds pass.

Final gate results, fingerprints, original task identifiers and log hashes are in
[the machine-readable evidence](switching-profile-native-observation.json).

## Remaining scope

Protected comparison is not durable latest-effect publication. Versioned applied
operations, original-task/source/owner binding, capacity/bookkeeping and atomic
latest-row replacement, mandatory receipt-gated release and projection/key history
are still needed. Old creator-only rows must not be retroactively fingerprinted
from arbitrary current native state. Adoption/prune/source/config/controller
cleanup, handoff/retirement, installed actors and broader schema/default/catalogue/
physical coverage remain open. No resource or whole-section capability flag was
promoted by this reader/comparison qualification.
