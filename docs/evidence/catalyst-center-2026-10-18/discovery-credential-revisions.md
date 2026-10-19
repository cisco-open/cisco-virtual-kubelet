# Credential successor revisions and pruning

Successors that acquired public custody can now revise bounded pure-credential
Delete-policy discovery intent. Strict initial custody publication/readback is
preserved. The immutable provenance root validates the original controller/config,
predecessor closure, complete archived seal, current operation/history bounds and
cancelled-witness identities. Current execution separately validates the canonical
source scope, actual source/projection and revision generation/source identities.
No old spec/source body, native permission, persistent store, provider, transport,
CRD or finalizer is synthesized or added.

The initial generation must retain its exact spec. ConfigMap content can change
only with a changed UID/RV; same-generation ConfigMap-only updates are supported.
Later config revisions use the current attested spec. The original custody link,
predecessor closure and every retained receipt remain unchanged. Initial resolved
inputs remain pinned for the initial revision; a new current scope cannot bypass
native identity/public-state matching or delivered-version rules.

Rotation, metadata changes, addition, source pruning/Retain and prune/recreate
reuse the existing executor, one-use permit, source-bound task observation and
atomic receipt publication. Current and archived logical-field floors survive
row reordering and removed leaves. Reintroducing a removed leaf at its old version
is refused. Retirement applies no credential versions; a source-lost acknowledged
Delete can finish its original task without creating a successor or claiming
current-source convergence. Unknown/taskless outcomes never authorize replay.

Twenty lifecycle modes run under controlled persistence and a real isolated
Kubernetes API. They cover inline/ConfigMap rotation, metadata updates, job
addition/prune/Retain, prune/recreate, leaf removal/reintroduction and stale leaf
versions, lagging projections, archived UUID return, seal/scope loss, lost Accepted/
retirement/permit acknowledgement, ambiguous deletion, Unknown-task recovery and
source loss. Successful updates also delete the revised config, verify complete
retirement, preserve archive/history and release only the manager's finalizer.

The [validation manifest](discovery-credential-revisions.json) records the frozen
source/binary hashes and terminal gate logs. Existing live baseline and guarded
pre-submission checks are read-only. Controlled native responses plus real
Kubernetes persistence do not qualify native credential mutations or installed
actors.

Still pending: final projected-leaf removal, empty/mixed source-family migration,
installed actors, native credential delivery and the wider pinned NetAsCode 0.5.0
backlog. Full model parity remains incomplete.


| Verification | Result |
| --- | --- |
| Affected discovery/credential/configuration race and isolated-API regression | Passed; 2,589 passing test events, 954.724s |
| New bounded successor revision lifecycle suite | Passed; 20 modes per persistence type, 40 total cases |
| Existing foundation, ND, direct upgrades and maintenance race checks | Passed |
| SWIM/reload/XFSU/transfer fallback regression | Passed |
| Full application build | Passed |
| Existing guarded live baseline and pre-submission cancellation/refusal | Passed; three devices, two images and one discovery job preserved; zero native mutation attempts |

The frozen package snapshot contains 1,636 Go files. These gates qualify the
named scope; they do not establish whole-repository or complete-model parity.
Actual device CLI/SNMP credential location has been requested for native
credential-discovery qualification; controller API access alone is not evidence
of device authentication.
