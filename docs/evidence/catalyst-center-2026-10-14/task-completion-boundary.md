# Native completion boundary for bulk convergence proof

The existing task-tree reader now retains the latest terminal timestamp across
the entire validated rooted tree. Its boolean completion API delegates to the
same implementation, preserving existing callers and clock-skew semantics.
There is no second parser or new transport. Free-form progress, native failure
text and task data remain excluded from the typed evidence.

Running, failed, duplicate, foreign, disconnected or cyclic trees provide no
usable completion boundary. A successful root alone cannot override a later
descendant's completion. The existing root, parent, task identity, submission
window and bounded tree checks still apply.

Bulk native verification uses this completion-bearing observation. Final bulk
receipt construction passes that native boundary to the independent proof
callback, then repeats native/member evidence and requires the same completion
timestamp after proof. A changed boundary therefore cannot leave a stale proof
accepted. The existing durable receipt timestamp uses second-resolution
metav1.Time: final construction waits until its representable verification time
is at or after the native millisecond completion time. Future-dated and
not-yet-representable completion evidence does not invoke the proof callback.

This supplies a trustworthy timing input, not rendered-device attestation or
source/site revision proof. The real proof provider remains unavailable, and
public bulk dispatch/completion remains gated. No SWIM, direct upgrade or ND
protocol was replaced or widened.

## Fixtures

The existing rooted-tree matrix remains covered. New tests retain the maximum
end time from an out-of-order nested tree, suppress timestamps for running or
invalid descendants, and prove that a report fresh only against the root's end
does not pass the later descendant boundary. Existing clock skew remains
accepted by the boolean interface; stricter final proof refuses future time.

Bulk receipt fixtures assert exact native completion propagation and cover
future completion, changed completion during proof and subsecond completion
that the current receipt timestamp cannot yet represent. The complete private
POST/PUT lifecycle fixtures still require independent simulated device proof.
Native test task timestamps are now stable across observations, reflecting
terminal-task behavior rather than generating a new end time on every GET.

Affected race checks passed: Catalyst Center 11.244s and shared configuration
7.045s before the final second-resolution gate. See
[affected regression](task-completion-boundary-regression.log).
The complete final race suites passed with the second-resolution gate included:
Catalyst Center 241.766s and shared configuration 30.971s. See
[full regression](task-completion-boundary-full-regression.log). Strict
documentation build and `git diff --check` passed.

## Live read-only qualification

The reader inspected a real previously acknowledged lab site-update task from
the [public site-update evidence](site-bulk-public-update-live.log), using its
recorded submission time and identity. No replacement task was submitted to
manufacture success. Recorded submission was 2026-10-14 14:58:10 UTC, and the
validated latest completion was 2026-10-14 14:58:10.497 UTC.

Live test passed in 0.32s (package 1.143s). See
[live timestamp evidence](task-completion-boundary-live.log) and
[sanitized access](task-completion-boundary-live-access.log). This qualifies
native task timestamp reading for that task, not an accepted live bulk cycle,
installed Kubernetes actors, or source-bound rendered-device convergence.

## Remaining scope

Independent device proof tied to the expected source/site revision, public
all-member preparation/dispatch/receipt/settlement, complete model composition
and accepted live bulk recovery remain pending. Cached compliance remains
insufficient: current reports are old, NETWORK_PROFILE is absent, and inspected
NETWORK_SETTINGS source arrays are empty.
