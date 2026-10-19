# Successor credential cleanup and sealed retirement

This stage connects a privately transferred successor's exact initial revision
to the existing public Delete-policy worker and manager lifecycle. It adds no
new provider, CRD, transport, finalizer, journal or NetAsCode field. Automatic
public ownership transfer remains disabled.

Cleanup authority requires the original custody record and pre-deletion source
scope, complete sealed archives, current owner/spec/controller identity and an
exact source-committed deletion operation. Kubernetes deletion may advance the
generation while leaving the original intent unchanged. Current source and
projected bytes remain mandatory for submission; metadata-only recovery is
restricted to acknowledged original work. Structural history validation by itself
cannot authorize a native call.

The final native preflight reads a fresh post-permit controller snapshot, checks
the entire owned cohort and archived UUID absence, then rechecks that exact
revision through the shared dispatch guard. Uncertain taskless or lost-permit
outcomes retain the original fence. Verified deletion commits a retirement row
without changing any delivered credential version or historical row. Repeated
complete absence and unchanged authority precede a new full-cohort seal and
worker completion proof. The manager verifies the exact closure and releases
only its existing discovery finalizer, preserving unrelated finalizers.

The lifecycle suite uses controlled native responses under controlled persistence
and a real isolated Kubernetes API. It covers inline/ConfigMap success, source
loss after acknowledged deletion, original Unknown-task recovery, lost permit,
Accepted/retirement/closure/completion acknowledgements, taskless responses,
returned archived/current UUIDs, missing custody/archive/scope/projections and
changed source/spec/seal. A seal change during post-permit native preflight must
prevent DELETE. Accepted and Unknown pre-deletion Creates must finish through
their original tasks before deletion. The later-handoff scenario spans three
owners and repeated Create/deletion/retirement/manager release, retaining all
history and advancing delivered versions to three.

The [validation manifest](discovery-credential-custody-cleanup.json) records the
frozen source/binary hashes and terminal gate logs. Live baseline and existing
pre-submission cancellation/refusal checks perform no native credential mutation;
they do not qualify native successor discovery/deletion or installed actors.

Still pending: automatic public transfer, cancellation before first successor
delivery, safe successor intent revisions and source replacement/pruning, empty
or unowned/mixed-source cleanup, native credential delivery and installed actors.
Full NetAsCode 0.5.0 resource parity remains incomplete.


| Verification | Result |
| --- | --- |
| Affected discovery/credential/configuration race and isolated-API regression | Passed; 2,459 passing test events, 722.759s |
| New successor cleanup lifecycle scenarios | Passed; 24 per persistence mode, 48 total |
| Existing foundation, ND, direct upgrades and maintenance race checks | Passed |
| SWIM/reload/XFSU/transfer fallback regression | Passed |
| Full application build | Passed |
| Existing guarded live baseline and pre-submission cancellation/refusal | Passed; three devices, two images and one discovery job preserved; zero native mutation attempts |

The frozen package snapshot contains 1,626 Go files. These gates are scoped
qualification, not a whole-repository or complete-model claim. Two initial link
attempts exhausted host disk space; the affected gates passed after removal of
stale derived Go cache objects and sequential reruns. Source, runtime binaries,
backups and evidence were preserved.
