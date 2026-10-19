# AP configuration: native cohort dispatch staging

## Upstream and architecture

Pinned NetAsCode 0.5.0 `cc_access_points.tf`, resource
`catalystcenter_access_point_configuration.ap_config`, groups common settings
into a single request. Only Ethernet MAC and optional desired hostname belong
to each AP. The new private planner preserves that shape and reuses existing
AP parsing, inventory/site/RF/assignment readers and the shared authenticated,
rate-limited transport. No ND, gNOI, SWIM, shared journal or actor protocol was
modified. No new public capability is advertised.

[The Cisco configure API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/configure-access-points-v1/)
documents POST `/dna/intent/api/v1/wireless/accesspoint-configuration`, a required
AP list and radio list, and HTTP 200 with a task ID. The planner emits an empty
radio list for hostname-only/top-level-only requests and requires that exact
acknowledgement status. A task acknowledgement never proves settings convergence.

## Implemented

- Freeze the complete common settings object, including nested radio fields.
- Reject empty, heterogeneous, duplicate or unresolved cohorts.
- Resolve all members by stable serial and native UUID/Ethernet MAC; require
  their declared floor, RF profile and current assignment.
- Bind the immutable body and complete ordered dependency set into one digest.
- Repeat the whole native snapshot during preparation, before authorization
  and after the explicit all-member guard.
- Pass copied identity descriptors to the guard; caller mutation cannot change
  the native request or retained dependency evidence.
- Consume each plan once on success, denial or uncertainty. Lost replies, bad
  task IDs and authentication errors never replay the request within that plan.

The callback must independently validate source/journal ownership, all-member
physical reservations and provisioning completion. This native stage does not
supply those proofs and cannot publish completion or release a lease. Replanning
is not durable acknowledgement recovery and must remain behind the future journal
bridge; the current public inventory compiler still gates AP execution.

## Verification

`TestAccessPointConfigurationCohortDispatch` checks two-member, reordered,
hostname-only and deeply frozen requests; missing AP/floor/assignment; duplicate
or heterogeneous members; changes during initial collection or authorization;
UUID/MAC/floor/RF/assignment replacement; denied/missing guards; copied guard
inputs; missing/lost/401/202 responses; and repeated submit refusal. The submitted
body is compared with all mapped configure gates and nested radio settings.

Initial tests exposed that the generic mutation transport accepts successful
HTTP 202 responses. This stage now uses the existing required-status transport
variant to enforce the documented HTTP 200 acknowledgement, without changing
other providers' transport behavior.

The accompanying `access-point-configuration-affected.log` records AP,
extranet and SWIM race tests. The final isolated descriptor-copy revision is covered by
`access-point-configuration-descriptor-final.log`: returned planning descriptors
and callback descriptors are separate from the frozen dispatch evidence. These
are affected-test runs, not a new full-suite qualification. The native read-only inventory check returned HTTP 200, three
devices and zero APs; see `access-point-configuration-native.json`. No device or
controller configuration was changed in the lab.

## Remaining

Independent configuration/radio readback comparison, provisioning-completion
proof, durable Prepared/Submitting/acknowledged recovery, installed all-member
reservation handoff, public manifest execution and native AP mutation remain
unqualified. AP configuration and overall NetAsCode parity remain incomplete.
