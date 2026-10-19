# Public credential handoff and first-preparation cancellation

The existing configuration reconciler now routes enrolled, pure credential
Delete-policy discovery sources through the shared typed ownership observer and
atomic custody publisher. The old config must be gone, its complete closure and
history seal valid, and the current source scope attested. Repeated native and
source/projection observations precede a metadata-only ownership CAS. Creation
and cleanup retain the existing journal and one-use native dispatcher on later
reconciles. This adds no provider, transport, CRD, finalizer or persistent store.

A config deleted at its first Prepared Create can retain the exact unsubmitted
witness and complete typed-history seal atomically with slot release. Live
cancellation and source-free handoff readback share structural witness/cohort
validation; structural validation alone grants no execution authority. Archived
UUID absence and every requested name are checked in repeated native reads.
The original projected snapshot is preserved across revalidation. A changed
snapshot first refuses cancellation; a later stable observation can replan
because no credential was delivered. Taskless/uncertain submitted work cannot
use this path.

After atomic proof publication, completion can recover from lost acknowledgement
and source/projection loss without loading or synthesizing those inputs. It
repeats complete native absence, original Prepared-name absence, exact config
metadata and unchanged controller/proof observations, then publishes the same
completion. Original source/projection loss before atomic publication remains
fenced. A reappearing native name or archived UUID prevents finalization.

The manager independently checks the canonical config-bound closure and releases
only its existing finalizer. After the config disappears, source-free sealed
readback verifies the retained operation, controller/config/spec/source/scope,
complete history and selected evidence. A later custody link preserves this
witness, and rejects its operation ID if reused by the incoming owner. No creator
or delivery row is invented. A cancelled queued version is reusable if it exceeds
the actually delivered floor; an empty first-owner cancellation allows the first
real delivery to use version one. Legacy unsealed empty-history cancellations
remain manager-readable but cannot authorize transfer. Verified later retirement
uses the existing complete-cohort seal rollover.

Tests cover 24 successor-cancellation modes and 15 public transfer modes under
controlled persistence and a real isolated Kubernetes API, plus 19 existing
first-owner cancellation modes. Public positive cases proceed through creation
and/or deletion using reconstructed reconcilers. Fault cases cover lost/rejected
custody acknowledgement, missing seal/pair, old config presence, reused counters,
occupied journal, stale controller, unenrolled config, source/projection loss and
ConfigMap re-attestation before transfer. Cancellation cases cover archive loss
or reappearance, source/scope/projection/operation changes, same-version snapshot
replanning and lost/rejected retirement/completion writes. Root and archived
no-work handoffs preserve actual-delivery counters and complete audit.

The [validation manifest](discovery-credential-public-handoff.json) records the
frozen package/binary hashes, exact regression command and terminal gate logs.
Controlled native responses plus real Kubernetes persistence do not qualify
installed actors or native credential mutations. Existing live baseline and
pre-submission refusal/cancellation checks remain read-only.

Remaining: safe successor intent revisions and source replacement/pruning after
transfer, mixed/unowned or unwitnessed empty cleanup, native credential delivery
and installed actor qualification, and the wider NetAsCode 0.5.0 model backlog.
Full pinned-model parity remains incomplete.


| Verification | Result |
| --- | --- |
| Affected discovery/credential/configuration race and isolated-API regression | Passed; 2,547 passing test events, 823.750s |
| Targeted public transfer, successor and first-owner cancellation suite | Passed; 122 passing test events |
| Existing foundation, ND, direct upgrades and maintenance race checks | Passed |
| SWIM/reload/XFSU/transfer fallback regression | Passed |
| Full application build | Passed |
| Existing guarded live baseline and pre-submission cancellation/refusal | Passed; three devices, two images and one discovery job preserved; zero native mutation attempts |

The frozen package snapshot contains 1,633 Go files. These are scoped gates,
not a whole-repository or complete-model qualification claim.
