# Site hierarchy execution and numeric precision

On 2026-10-14, the native site executor was qualified against Catalyst Center
through the local tunnel using the user-supplied credential file. Only a newly
created isolated hierarchy was mutated:
`Global/CVK-Q-20261014-132357-000000000`.

The fixture created an area, building and floor; verified each native task and
resource readback; updated floor width; verified the full model reached a no-op;
then deleted the verified fixture identities in child-before-parent order.
Cleanup was verified. The live test passed in 11.32 seconds (package 12.110s).
The retained [test output](site-precision-live.log) contains no credentials or
tokens.

The implementation now submits selected numeric site attributes at the same
precision used for comparison and by pinned NetAsCode 0.5.0 bulk-site resources:
five decimal places for coordinates and three for floor geometry, using
`floor(value * precision + 0.5) / precision`. Source intent remains unchanged.
Unselected building attributes continue to be preserved. Floor dimensions that
would round to zero are refused during whole-intent preflight, before any parent
creation.

Local race tests passed for site parsing, comparison, planning, task submission
and journal reconciliation (3.826s). New regression cases cover negative
coordinate half ties, normalized payload/readback convergence, unchanged source
intent and zero-rounded descendant refusal.

This qualification exercises the production native site executor, not installed
Kubernetes actors. It does not qualify bulk API requests, ownership-aware
pruning, maps, AP placement or full NetAsCode site parity.

The read-only network reporting suite initially failed because its old
expectations treated every inherited group as drift and counted only unset DNS
as drift. Runtime reporting already resolves effective ancestor values and
reports all unset selected groups. The live tests now construct round-trip
intent from effective values while counting explicit/inherited/unset native
observations separately; synthetic local changes still verify drift detection.
No production inheritance logic was changed.

The rerun passed all six checks: AAA metadata, authentication templates, the
three lab device identity bindings, inventory assignment reporting, NTP/DHCP/DNS
reporting, and timezone/banner reporting. See the retained
[read-only qualification output](network-report-live.log).

The broader race regression passed for Catalyst Center (528.657s), the pinned
model helpers, Nexus Dashboard, shared controller configuration contracts,
controller reconciliation and CLI. See [regression output](site-foundation-regression.log).
Strict documentation build and `git diff --check` also passed. No foundational
provider logic, ND execution or upgrade routing was changed by this increment.
