# Managed identities for bulk fabric devices

The [native stage](fabric-devices-bulk.md) now passes its complete incoming,
existing-peer and zone-parent footprint through CVK's existing repeated managed
observer. No parallel resolver, transport, lease or claim protocol was added.

The observer hashes complete native dependency snapshots, resolves immutable
CiscoDevice/Node identities, reobserves native dependencies and rereads managed
bindings. The planner repeats the same full native and managed evidence around
the external footprint guard, then attaches the complete canonical group targets
to the native write identity. Failure returns no partial binding set.

Incoming devices alone are insufficient: an unmanaged or replaced existing peer
blocks the whole action. Cross-namespace aliases, wrong device address/driver,
Node UID changes and physical identity changes fail the existing rules. The
native fixture was extracted for reuse, preserving its original tests while
avoiding a second fabric-device API simulation.

## Verification

Eleven managed cases cover valid and zone footprints, missing incoming/peer
declarations, peer aliases, address/driver mismatches, replaced peer Node identity
and native/managed races during the guard. Valid fixtures retain two incoming
targets plus the existing peer. Refused or raced closures never send a native
mutation; neither successful nor failed closures can replay.

Managed/native/singular cases passed in 4.098s. Affected managed/native and shared
group identity/recovery race checks passed: Catalyst Center 10.261s and shared
configuration 10.867s. See [managed cases](fabric-devices-managed-tests.log) and
[affected regression](fabric-devices-managed-regression.log).

The repeated authenticated native fabric inventory check still found zero sites;
accepted execution was explicitly skipped (0.33s; package 1.032s). See
[fabric availability](fabric-devices-managed-live-prerequisites.log) and
[sanitized access](fabric-devices-managed-live-access.log). This proves the current
setup limitation, not installed managed actors or live role mutation.

## Remaining scope

This supplies preparation evidence only. Complete source grouping/chunking,
source/owner binding, shared group journal preparation, real all-member claims
and one-use dispatch permit, durable acknowledged recovery/receipt, independent
device convergence, completion/settlement, installed actors/API-server and live
accepted mutation remain pending. The helper neither creates a reservation nor
grants authority, and no resource/full-section completion flag changed.
