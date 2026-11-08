# Standard staging cleanup, upgrade and rollback — C9K, 2026-11-06

This candidate qualifies automatic cleanup of newly owned Standard staging tars
on a C9300-24UX (FCW2247C1AJ), IOS-XE 17.18.1 fc2, through the normal managed
shared-worker app-hosting flow. Standard is the omitted/default mode; no artifact
bindings, trust metadata, installation journal or artifact-specific RBAC were
configured. Verified remains optional and Governed deferred.

## Observed results

| Case | Release | Pod UID | Deployment revision |
| --- | --- | --- | --- |
| preloaded-before | STD-GC-A-20261106-a85d9b305139 | f6bceeb3-c299-4ef2-b4b1-458e2fc39173 | 1 |
| initial-a | STD-GC-A-20261106-a85d9b305139 | c5263495-106e-4a95-b8e3-ca83815a6a8b | 2 |
| upgrade-b | STD-GC-B-20261106-a85d9b305139 | 0a02d83d-8af5-4b7a-9a31-22844cb36cec | 3 |
| rollback-a | STD-GC-A-20261106-a85d9b305139 | cbd4447b-726d-4fc4-ba94-3df35ffaeacc | 4 |
| failed-upgrade-rollback-a | STD-GC-A-20261106-a85d9b305139 | 428e7765-56b4-4b6c-91da-23691cfc809e | 6 |
| retry-upgrade-b | STD-GC-B-20261106-a85d9b305139 | 9bdd1fc4-9c64-4a8d-887e-fe24f7aa83e8 | 7 |
| final-rollback-a | STD-GC-A-20261106-a85d9b305139 | e76fde1b-9ce6-4aba-9534-43582e126577 | 8 |
| preloaded-after | STD-GC-A-20261106-a85d9b305139 | f89f450b-a451-42cd-a981-7311a5c0783c | 9 |

All healthy revisions served the exact independently compiled A or B release,
with native RUNNING and Pod/Deployment readiness. The same Deployment UID was
retained and every transition created a new Pod/native app ID. Remote installs
used a complete successful RESTCONF copy before installing the local archive.
Each successful default staging tar was confirmed absent automatically while the
app continued running. No test command deleted those worker-owned paths. A separate read-only observer
recorded complete-size staging files becoming absent after installation, including
a growing file during a successful copy; its raw transitions are preserved in
`owned-file-observations.jsonl`.

The caller-preloaded A archive deliberately used the same `cvk-stage-<uuid>.tar`
filename pattern as worker staging. Local `Never` deployment passed before and
after the remote upgrade/rollback sequence. Exact size and native SHA-512 stayed
unchanged at every boundary, including the failed download. Filename resemblance
did not authorize cleanup. The qualification caller removed only its own canary
after all preservation proofs; the pre-existing tar inventory remained unchanged.

The failed upgrade Pod was `1ee05639-4038-46d1-8ef6-cc748677da83`. Under a valid HTTPS chain, the
origin declared B's complete Content-Length, sent 4,096 bytes and closed the
connection. The C9K returned a definitive copy failure. There was no app
configuration, installation, fixture runtime or Ready result, despite A and B
having run previously and no device image-cache pruning. The failed destination
was confirmed absent automatically, and the preloaded A checksum was unchanged.
This firmware removed/refrained from leaving the truncated file itself; deletion
of an actual partial residue is covered by unit tests, not claimed as a hardware
observation.

History rollback was requested at **2026-11-06T10:45:01.217588+00:00**, while the original
unchanged mutation Lease remained held. No Lease or journal was reset. Recovery
waited for natural expiry at **2026-11-06T11:10:44.005660+00:00**, copied A anew, and was healthy
at **2026-11-06T11:12:28.206268+00:00** (about **1647.0 seconds** after the request).
Restoring complete B delivery allowed another upgrade and history rollback.
The final preloaded deployment and normal deletion passed.

The first harness stopped after healthy recovery because its Pod-UID-only log
filter omitted copy-completion lines from detached missing-container recovery.
The original healthy snapshot, logs, script and failure were preserved before
resumption. Exact staging-path correlation established the copy/install ordering;
a read-only recheck used the same recovered Pod without replay or a guard reset.
The reported recovery time uses the first healthy snapshot, not the later harness
recheck. A later oper-data address lag was handled using CVK’s supported ARP
fallback: authenticated native ARP had to map the Pod IP to this exact app’s
native MAC before the HTTP probe. Oper-only snapshots and both observer sources
are preserved; native state was never overridden. `harness-stop-evidence.tar.gz` and its index preserve this attempt.

This is a single-slot Recreate qualification. It does not establish zero-downtime
updates, RollingUpdate capacity or automatic vulnerability-based image selection.
Rollback still needs the prior versioned upstream package, or a caller-retained
local image; a deleted temporary tar is not a persistent rollback cache.

## Ownership and safety contract

- A new remote installation gets a CSPRNG UUID path directly under `flash:`.
  CVK reads filtered filesystem metadata, proves that exact destination absent,
  and creates an in-memory ownership receipt before submitting the existing
  synchronous copy RPC. Existing files are never adopted or swept by prefix.
- CVK deletes only a receipted regular file after the copied package is consumed
  (native RUNNING), or after confirmed teardown/absence. A definitive synchronous
  4xx copy failure also permits cleanup of any partial owned destination.
  Successful staging that never reached install can be removed immediately.
- Cleanup uses the existing RESTCONF RPC family, `Cisco-IOS-XE-rpc:delete`, with
  one exact owned filename. Its qualified completion response and a subsequent
  exact-key absence observation are required. No new transfer transport is used.
- Device-local/preloaded sources, explicit `cisco.io/apphost-package-dest`
  destinations, legacy files and files from another worker instance are retained.
  Explicit local adoption relinquishes a matching live receipt, including aliases
  matched conservatively by basename.
- Uncertain copy/delete completion is quarantined. An unconsumed installation
  retains its tar until running or confirmed teardown. Cleanup errors retain the
  receipt/file and respect normal mutation fences; no forced recovery is used.
- Receipts are scoped to the live worker, not a new durable Standard journal.
  A crash can leave an archive for operator inspection. A replacement worker
  cannot reconstruct deletion authority from a filename. If qualified filesystem
  ownership observations are unavailable, copy-first deployment uses an unowned
  unique destination and retains it with a warning. This preserves the ordinary
  Standard flow without requiring extra caller configuration.

The complete exclusive device mutation guard covers allocation, copy, app
lifecycle and deletion. The ownership contract assumes normal single-owner CVK
coordination; out-of-band writers must not replace CVK's active staging files.
These receipts are deletion authority, not cryptographic image identity. Select
optional Verified for the signed artifact identity and durable install journal.

## Evidence and checks

[qualification.json](qualification.json) is the independent assessment;
[candidate.json](candidate.json) binds the dirty source tree, rebuilt binary,
container image and independently inspected image layer. [test-results.json](test-results.json)
records the full race-enabled suite. [automated-checks.json](automated-checks.json)
records final source, documentation, cleanup and evidence verification.
[SHA256SUMS](SHA256SUMS) covers every exported artifact. Repeated sanitized
observations are indexed in [observation-snapshots-index.json](observation-snapshots-index.json)
and retained in `observation-snapshots.tar.gz`; boundary snapshots, native CLI
proofs, worker traces, server events and the executed harness remain accessible.

The existing reports, including the earlier retained-tar finding, were preserved
byte-for-byte. The initial broad filesystem probe's incomplete response is also
preserved. Narrow filtered metadata and exact-key queries were then qualified;
copy/delete probes were confined to fresh test-owned canaries. The lab restored
the original management TLS selector/client identity, unrelated iris RUNNING,
c9ktest DEPLOYED and CAF Stable, stopped only its isolated cluster/origin, removed
its fixture tags and two ephemeral origin keys, and verified the shared host node.
No device clock, global image-cache or CRL-cache changes were made.

The [IOS-XE RPC YANG](https://github.com/YangModels/yang/blob/main/vendor/cisco/xe/1731/Cisco-IOS-XE-rpc.yang)
defines the existing copy/delete RPCs. The [platform software operational YANG](https://github.com/YangModels/yang/blob/main/vendor/cisco/xe/17181/Cisco-IOS-XE-platform-software-oper.yang)
defines the filesystem keys and regular-file type used by the qualified exact
observations. Hardware results are specific to this device and release.
