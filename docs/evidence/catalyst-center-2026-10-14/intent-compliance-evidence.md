# Catalyst Center intent compliance evidence

The branch now has a private, read-only compliance reader using the existing
Catalyst Center client and authentication transport. It retains only device
identity, compliance type, status, state and check timestamp for NETWORK_SETTINGS
and NETWORK_PROFILE. Violation descriptions, configured/intended values and
unrelated compliance domains are not retained in the typed evidence.

The [official compliance-details API](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/compliance-details-of-device/)
provides these fields. The reader uses one GET with `diffList=false`. On the lab
appliance, combined category/type filters returned an empty response despite
reports being present in the unfiltered response. The reader therefore projects
the two networking types locally, without a retry, refresh or remediation call.

## Freshness and identity

The supplemental predicate requires COMPLIANT/SUCCESS and a millisecond check
timestamp at or after an independently established task-completion boundary,
and no later than the observation time. Missing reports, absent timestamps,
warnings, not-applicable results and stale reports cannot pass. Conflicting
device identity, duplicate networking types, unsupported status/state and
invalid timestamp units are rejected. A null root UUID with an explicit empty
list remains absence, never positive evidence.

This does not bind a report to the expected source/site revision or establish
rendered-device convergence. It is not connected to the final bulk receipt
proof. Existing Verifying operations continue to retain their fences and report
GroupConvergenceVerificationUnavailable when actual convergence proof is absent.

## Live qualification

Authentication using the local credential file succeeded. The read-only live
test inspected all three real inventory identities and exercised refusal of
old/absent reports against the inspection start time. That time is only a test
boundary; no native task completion was fabricated.

| Switch address | NETWORK_SETTINGS check (UTC) | NETWORK_PROFILE |
| --- | --- | --- |
| 198.51.100.100 | 2026-10-08 09:53:51; COMPLIANT/SUCCESS | Absent |
| 198.51.100.101 | 2026-10-09 22:23:48; COMPLIANT/SUCCESS | Absent |
| 198.51.100.103 | 2026-10-08 09:53:51; COMPLIANT/SUCCESS | Absent |

Live test passed in 0.79s (package 1.444s). See
[sanitized live output](intent-compliance-live.log) and
[sanitized access](intent-compliance-access.log). The same inspection found no
fabric sites or multicast configurations in the lab. No device/controller
configuration writes, refresh, remediation, claim changes or release occurred.

Fixtures cover stale/future/missing timestamps, incomplete selections, device
replacement, unsupported domains, duplicate selection, missing/future completion
boundaries and an exact completion timestamp. The earlier affected Catalyst
Center and shared configuration race checks passed in 7.867s and 6.941s.
The final complete race suites also passed: Catalyst Center 238.322s and shared
configuration 31.287s. See [full regression](intent-compliance-regression.log).
Strict documentation build passed; `git diff --check` passed. The initial
sandboxed full-suite attempt could not bind localhost HTTPS test listeners;
the successful run used permission for those listeners.

Full model parity and public bulk completion remain pending. Freshness alone
cannot replace source-bound device readback, actual all-member actor execution,
receipt publication or settlement qualification.


## Source-correlation follow-up

A separate read-only inspection of each NETWORK_SETTINGS report found an empty
sourceInfoList on all three switches. Both the ordinary `diffList=false` response
and a detailed `diffList=true` response returned zero sources. The inspection
emitted only source-array types/counts and would retain only metadata field
shapes/site-match booleans if present; no configured/intended or violation values
were logged. See [ordinary metadata](intent-compliance-source-metadata.log) and
[detailed metadata](intent-compliance-source-metadata-detailed.log). The
production typed reader still requests `diffList=false`.

These particular reports cannot bind an expected source/site revision. This does
not assert that future reports or other appliance versions cannot provide such
evidence; it rules out using the currently observed cached reports as final
bulk configuration proof. No compliance refresh or remediation was requested.
