# Public credential discovery deletion

This follow-up enables manager enrollment and public worker orchestration for
pure inline-credential discovery under `deletionPolicy: Delete`. It reuses CVK's
existing discovery finalizer, configuration journal, one-use submission guard,
task observer, atomic receipt writers and completion/retirement conditions.
No new transport, finalizer, controller or credential store is introduced.

Terminal verification passed for the frozen source snapshot recorded in the
[validation manifest](discovery-credential-public-deletion.json). Controlled and
real Kubernetes API-server runs cover 18 lifecycle scenarios and three cohort
scenarios per persistence mode. These use controlled native responses, not live Catalyst
Center credential cleanup.

Coverage includes normal inline/ConfigMap deletion, acknowledged task recovery,
original pre-deletion Create and replacement-retirement drain, source/projection
restoration, exact paired audit retention and manager-only finalizer release.
Two-job cleanup verifies whole-cohort admission; conflicting native state causes
zero Deletes, and mixed global-only declarations cause zero Creates or Deletes.

Cold first-Create Prepared or empty/unowned histories remain held. Submitting or
taskless uncertainty, failed/foreign tasks, missing scope and changed specs remain
held without mutation replay. Credential ownership handoff, proven no-dispatch
recovery, installed credential actors and native credential mutations remain
unqualified. The full pinned 93-resource model is not complete.

The restored lab tunnel supports Basic-auth token generation followed by
`X-Auth-Token` requests. Read-only qualification observes three devices and two
SWIM images. Two enforced read-only discovery refusal tests also passed: each
reconstructs the public reconciler three times, refuses unowned-job adoption or
partial mixed creation and preserves one native discovery job and all three
device identities. These checks do not qualify mutation or device authentication.


The first expanded regression exposed stale expectations in two projection-boundary
test families: Delete-policy intent without manager enrollment previously reported
`Failed`; it now reports `Waiting` with `CleanupEnrollmentPending`. The assertions
now require that exact reason, no discovery finalizer, zero native calls and an
unchanged journal. This correction changes test expectations only.


| Verification | Result |
| --- | --- |
| Expanded discovery/configuration race regression, including real Kubernetes API tests | Passed; 2079 passing test events, 503.059s |
| Existing controller adapter, ND, software-upgrade and maintenance race tests | Passed |
| SWIM, reload, XFSU and transfer fallback race tests | Passed |
| Full `cisco-vk` application build | Passed |
| Live health/inventory and enforced read-only discovery refusal tests | Passed; three devices, two images, one discovery job preserved |
| Strict documentation build | Passed |

Only two test files changed after the first regression: the corrected enrollment
expectations described above. Production sources used by the build and companion
checks are unchanged. The final expanded regression passed with those assertions.
Logs and source/binary hashes are linked in the validation manifest.
