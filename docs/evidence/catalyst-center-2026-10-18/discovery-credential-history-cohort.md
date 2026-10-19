# Retired credential cohorts and history seals

New verified credential closures emit `CredentialHistoryDigest` inside the
existing ownership-retirement condition. Its versioned digest seals canonical
copies of creator, retirement and credential-application rows. It excludes mutable
reservation metadata, native payloads and secret bytes. No CRD, finalizer,
transport, journal or NetAsCode input field is added. Ordinary retirement wire
format is unchanged when the optional field is absent.

The manager's shared verifier checks the new seal. Legacy single-owner records
remain readable, while an unknown or altered seal version is rejected. The
private archive-seal verifier requires the exact known-version digest: removing
a whole closed owner is detectable even if remaining rows are structurally valid.
That digest check alone grants no ownership or native submission authority.

A derived cohort uses the existing strict single-owner parser on local copies.
It retains original UID, task, UUID, version and source bindings, verifies complete
closed archived groups, rejects ambiguous global operation IDs (including other
families), native UUID reuse, orphaned pairs and mixed group-cancellation history, and checks
active-owner delivered counters against all archived logical-field floors.
Removed fields and row renumbering cannot reset a floor. Queued retirement
versions do not count as delivery. No original receipt is rewritten or relabeled.

Eighteen scenarios run under controlled and real Kubernetes API persistence.
The deliberate identical map-key duplicate is schema-refused where applicable;
a distinct-resource cross-family ID collision and different-receipt native UUID
reuse persist and are refused by the adapter. Successful cohort cases also check
version transitions and the reappearance of either archived UUID. Separate tests
prove the seal changes on complete-owner loss and remains stable across derived
reservation views. These are audit fixtures, not native execution evidence.

The same cohort validator is used by qualified single-owner manager closure
verification. Public handoff, foreign-history execution and multi-owner closure
publication remain disabled. The next step is to bind the archive seal to
controller/config/source identity at the ownership CAS, then enforce it and the
retained floors/absence checks through ordinary execution, cancellation, recovery
and subsequent retirement. Missing or legacy seals must not enable that path.

Final source-bound gates passed. The [validation manifest](discovery-credential-history-cohort.json)
records source/binary hashes, the exact selected test command, results and logs. Live
checks here exercise existing guarded read-only/cancellation behavior, not native
credential history transfer. Full pinned-model parity remains incomplete.


| Verification | Result |
| --- | --- |
| Cohort/seal, handoff-observer, completion, public deletion, cancellation/no-dispatch and ordinary handoff race/API regression | Passed; 414 passing test events, 122.482s |
| Existing adapter, ND, software-upgrade and maintenance race checks | Passed |
| SWIM, reload, XFSU and transfer fallback checks | Passed |
| Full application build | Passed |
| Guarded live/API baseline, existing cancellation and refusal tests | Passed; three devices, two images and one native discovery job preserved; zero mutation attempts |
| Strict documentation build | Passed |

The frozen package snapshot contains 1,617 Go files. Verification covers the
named gates; it is not a whole-repository or full-model completion claim. The
multi-owner cohorts are audit fixtures with real API persistence, not successful
native ownership transfers or credential deliveries.
