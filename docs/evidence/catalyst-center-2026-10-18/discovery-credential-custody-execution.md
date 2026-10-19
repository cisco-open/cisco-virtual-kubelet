# Credential successor execution after private custody

The existing configuration reconciler can now execute an exact initial successor
revision after a separately verified private custody CAS. Automatic public
ownership transfer is still disabled. This adds no new provider, transport, CRD,
finalizer, journal or model field.

Execution authority is independent of structural receipt budgeting. The record
must bind the current controller and successor identity/generation/spec/source,
original source-scope attestation, resolved-input digest and complete sealed
predecessor history. Archived delivered versions remain floors for successor
credentials. Native preflight and result observation check that retired UUIDs
remain absent; the shared one-use dispatch guard rechecks custody and the exact
controller revision used for preflight. Original audit rows are never relabelled.

Normal Create, acknowledged original-task recovery, paired receipt publication
and slot release reuse existing machinery. Present-source execution requires
current source/projection checks. Acknowledged completion after ConfigMap loss
uses original attested metadata without inventing a replacement source or
claiming InSync. Taskless uncertainty, a lost submission-permit acknowledgement,
missing scope and changed custody/archive remain fenced.

Fifteen scenarios run under each of controlled persistence and a real isolated
Kubernetes API. They cover inline/ConfigMap creation, timeout and restart recovery,
lost Accepted/Submitting/Verified acknowledgements, taskless responses, source
loss, archived UUID return before/after submission, missing custody/whole archive,
changed seal/input digest and missing original scope. Successful delivery preserves
the original receipts and advances the logical credential version floor. Repeated
reconciles do not replay requests; unverified outcomes fabricate no delivery.

The [validation manifest](discovery-credential-custody-execution.json) records the
frozen package source and binary hashes, terminal test results and gate logs.
Live checks use the actual restored tunnel for guarded read-only and pre-submission
refusal/cancellation behavior. They do not submit native credential mutations or
qualify native successor execution. Token generation and the three-device
inventory succeeded; the previously retained PnP test UUID returned HTTP 404.

Still pending: automatic public handoff, safe successor intent revisions,
replacement/prune/config deletion, subsequent multi-owner retirement, empty or
unowned and mixed-source cleanup, native credential delivery and installed actors.
Full NetAsCode 0.5.0 resource parity remains incomplete.


| Verification | Result |
| --- | --- |
| Affected discovery/credential/configuration race and isolated-API regression | Passed; 2,409 passing test events, 635.191s |
| New successor execution scenarios | Passed; 15 scenarios per persistence mode, 30 total |
| Existing foundation, ND, direct upgrades and maintenance race checks | Passed |
| SWIM/reload/XFSU/transfer fallback regression | Passed |
| Full application build | Passed |
| Guarded live baseline and pre-submission cancellation/refusal | Passed; three devices, two images, zero native mutation attempts |
| Strict documentation build | Passed |

The source-bound package snapshot contains 1,623 Go files. These gates are scoped
qualification, not a whole-repository or complete-model claim.
