# Lost activation and start replies — C9K qualification

Both independent cases passed on 3 November 2026 using the corrected CVK
candidate on C9300-24UX / IOS-XE 17.18.1 fc2, serial FCW2247C1AJ.
Standard remains the default. Only the dedicated CiscoDevice opted into
Verified Copy; Governed remains deferred.

| Lost reply | Native state before loss | Fenced samples | Forwarded install / activate / start | Recovered result |
| --- | --- | --- | --- | --- |
| activate | ACTIVATED | 113 | 1 / 1 / 1 | Ready, HTTP A, empty reason/message |
| start | RUNNING | 112 | 1 / 1 / 1 | Ready, HTTP A, empty reason/message |

For each case, the authenticated lab relay captured the real successful device
reply and independently observed its native target state, then closed the
worker connection before sending response headers or body. The corresponding
pending journal record existed before dispatch. Manager reconciliation replaced
the app worker, and its exact workload binding converged without manual edits.
The same Pod, app and journal survived recovery. The original mutation lease
expired normally; it was neither cleared nor recreated. The recovered worker
observed the completed operation and did not replay it. The trace contains no
scope/replay rejection or relay error; its duplicate guard did not hide retries.

In the lost-start case the app was RUNNING and served HTTP A while the journal
remained startPending and the Pod remained not Ready. Guarded recovery was still
required. Both cases subsequently reached a running journal, Ready Pod and
Available/Progressing Deployment. Their top-level Pod reason/message were empty.
This physically verifies the fix for the stale diagnostic
[recorded in the earlier install run](../verified-iosxe-2026-11-03/findings.md).
Historical evidence has not been rewritten.

Delivery used the existing RESTCONF copy RPC, followed by pinned SSH native
file-size and `verify /sha512` checks, bound to the worker-validated SHA256
package. SSH did not transfer the image. Exact package A, descriptor and producer
signature from the [2 November core run](../verified-iosxe-2026-11-02/README.md)
were reused unchanged. No IOS-XE platform image was upgraded.

The candidate includes the status fix: source fingerprint
`sha256:d07b6ea671fb9508e3eb648014e642eabf845d5fe45d1bd339557cf8b0c098ac`, binary SHA256
`529e98f251b320567712d7077dcf9b54e5d93737e36100252cf809b2b6db2040`. The source fingerprint includes tests and
chart sources. All 56 retained chart files matched current source. Live worker
image IDs were checked against the recorded corrected image configuration.
See [candidate identity](candidate.json), [assessment](qualification.json),
[raw case results](qualification-result.json), [relay trace](relay-events.jsonl),
and [test results](test-results.json). The full Go race run passed 62 packages.

Normal per-case deletion retained both deleted journal tombstones and released
the operation lease. Final cleanup removed only the two owned staged archives,
temporary public CA trustpoint, relay/store processes and isolated cluster.
The original HTTPS selector and IRIS client trustpoint were restored, native
signature policy was preserved, iris remained RUNNING, c9ktest remained DEPLOYED,
and the shared Kubernetes node remained Ready.
See [cleanup evidence](final-cleanup.json).

This qualifies these two lost-reply scenarios on this device/firmware/package
profile. The earlier core upgrade/rollback and lost-install cases used their
separately recorded candidates. A physical Standard control, uninterrupted
service, stateful migration, fleet-wide support and remediation of a particular
CVE are not claimed. The trace measures forwarded CVK requests; no native
installer-internal counter is exposed. The checksum measures the staged archive,
not the running root filesystem.

Evidence is allowlisted and sanitized. It excludes Secret data, credentials,
private keys, kubeconfig and unrelated topology/interface observations. Retained
harness source is under `harness/`; SHA256SUMS covers every exported file.
