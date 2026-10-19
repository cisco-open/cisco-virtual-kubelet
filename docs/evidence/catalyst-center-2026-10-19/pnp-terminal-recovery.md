# PnP terminal recovery without current intent

The public configuration reconciler now retires a verified PnP removal journal
before reading the current configuration object, source, contract or controller
execution settings. It uses the existing shared journal finish boundary and the
original immutable creator-bound absence receipt. It performs no native API call,
does not create ownership and does not mark edited intent InSync.

This closes historical completion after a source disappears, intent becomes
invalid, the configuration is deleted, or the controller is paused or edited,
provided the original cleanup is already Verified and manager custody has been
settled and retired. A journal event enqueues its original configuration name even
when that configuration no longer exists. Replacement controller UIDs and other
configuration names cannot select this recovery path.

Manager custody must still retire first. Missing, malformed or foreign ownership
and absence receipts hold the operation. Unknown or other nonterminal operations
are not inferred complete from native absence. The shared finish method requires the exact original Verified operation on its
own authoritative read and status compare-and-swap; the early routing check cannot
accidentally use legacy Prepared cancellation or finish a different target if the
operation changes concurrently. Existing physical group cancellation and completion use
the same retained-evidence entry point with their existing admission checks.

## Validation scope

The controlled public lifecycle tests run preparation, one-shot submission,
independent absence verification and manager settlement before removing or editing
intent. They cover missing source, invalid intent, deleted config, pause, controller
generation/endpoint edits, worker disappearance, wrong request, damaged receipts,
foreign ownership, Unknown state and a concurrent phase change. Terminal recovery
is checked with a transport that rejects every native request, including reads
and authentication. Creator/tombstone history must remain unchanged.

The Kubernetes 1.35 API-server tests exercise actual CRDs, native CEL admission,
actor-separated journal transitions and settlement with a controlled appliance.
API-server caller/readiness facts are staged; these tests do not run the deployed
manager and connector processes.

The native Catalyst Center harness uses a fresh pair of generated unclaimed PnP
records. It allows only their exact generated registration payloads and Deletes
bound to their durably verified creator UUIDs. It checks manager-custody retention,
then missing-source and invalid-source terminal recovery with zero transport calls.
It retains original creators and absence tombstones and independently verifies the
PnP and managed-device inventory baselines. Kubernetes storage and runtime authority
in this native harness are simulated; this is not installed runtime qualification.

[Machine-readable evidence](pnp-terminal-recovery.json) records terminal results,
source fingerprints and the private recovery location for this fresh cohort.
Never replay a completed native qualification or reuse its deleted record IDs.

## Remaining work

This change completes only receipt-backed historical journal finishing. It does
not implement cancellation of Prepared metadata custody, uncertain native outcome
resolution, in-flight controller-generation/domain changes, deletion-policy PnP
cleanup or full claim/provisioning convergence. Full pinned model execution and
hardware qualification remain incomplete. Docker's local API still times out, so
installed qualification in the separate lab cluster remains pending.
