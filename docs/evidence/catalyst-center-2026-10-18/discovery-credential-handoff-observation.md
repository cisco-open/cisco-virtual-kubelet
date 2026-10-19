# Credential ownership handoff observation

This stage qualifies read-only prerequisites for a later credential ownership
handoff. Public transfer is not enabled. The original single-owner lineage
validator and ordinary handoff fence remain in place.

The existing credential manager completion check now reuses the pure canonical
retirement-record verifier. It verifies the complete creator/retirement and
credential pair, exact evidence digest, owner, controller/config identities,
generations and known closure format. Unsubmitted cancellation witnesses cannot
be reinterpreted as successful credential delivery. No format, CRD, finalizer,
transport or status store is added.

A private observer then requires an independently enrolled pure-credential
successor with a complete current source and authorized mounted projection.
It checks old-config disappearance before and after two complete native reads.
Retired UUIDs must remain absent, declared new names must be unowned, and native
inventory, source, projections, resolved global-credential IDs and retained status
must stay unchanged. The result contains only scoped metadata/digests; it cannot
transfer ownership, prepare an operation or submit a native request.

Successor versions must advance every delivered high-water mark for a reused job
and logical credential field, including removed leaves. Manifest row renumbering
does not reset that floor. Queued Delete versions never count as delivery.

Twenty-three scenarios run under controlled and real Kubernetes API persistence:
inline/ConfigMap/same-name and unrelated-native success, stable global references,
counter reuse, projection lag, old-config presence, old UUID or unowned-name return,
native read failure, source/projection/journal/config races, global-ID drift,
missing/changed closure evidence, missing credential pair, pending operation,
paused/changed controller and missing enrollment. Five floor tests additionally
cover row renumbering, removed fields and queued-but-undelivered counters. Native
race tests assert they reach their intended observation boundary, rather than
passing because an earlier unrelated guard rejected the fixture.

Final source-bound gates passed. The [validation manifest](discovery-credential-handoff-observation.json)
records source/binary hashes, the exact selected test command, package results and logs.
Live lab checks here cover existing read-only/cancellation behavior, not native
credential handoff. Full pinned-model parity remains incomplete.

Remaining handoff work is to seal multi-owner credential history in the existing
retirement envelope, validate it through the ownership CAS and subsequent normal
execution, retain version floors across successive owners, and qualify public,
installed and native flows. General empty/unowned cleanup and mixed Delete-policy
sources also remain outside the supported credential lifecycle.


| Verification | Result |
| --- | --- |
| Handoff prerequisite, shared completion, public deletion, unsubmitted/no-dispatch and ordinary handoff race/API regression | Passed; 346 passing test events, 116.931s |
| Existing adapter, ND, software-upgrade and maintenance race checks | Passed |
| SWIM, reload, XFSU and transfer fallback checks | Passed |
| Full application build | Passed |
| Guarded live/API baseline and existing cancellation/refusal cases | Passed; three devices, two images, one native discovery job preserved; zero mutation attempts |
| Strict documentation build | Passed |

The frozen package snapshot contains 1,614 Go files. Verification covers the
named gates and selected tests; it is not a whole-repository or full-model
completion claim. No actual credential ownership transfer was performed.
