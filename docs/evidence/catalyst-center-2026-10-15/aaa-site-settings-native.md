# Site AAA registration prerequisites and native execution

## Reproduced missing prerequisite

The site AAA API returned HTTP 400/NCND00001 with **NCND01221** for both declared
primary addresses: no corresponding authentication-policy server was registered.
The public reconciler retained Unknown without replay. This was a dependency
gap, not a successful qualification. [Redacted native rejection](aaa-missing-registration-native.log).

CVK now uses its existing authentication-policy-server metadata reader to validate
all plain-AAA primary and selected secondary addresses before the site-settings
planner releases a write. Registrations must be explicitly non-ISE and support
the requested protocol, either exactly or through RADIUS_TACACS. No server is
registered implicitly and no registration credential is read or adopted.

Submission repeats the complete dependency observation, including native UUIDs
and protocol capability. Changed registrations refuse dispatch and every attempted
closure is consumed on success, refusal or uncertainty. Verification also refuses
missing/incompatible current dependencies. Combined network/telemetry/credential
models preflight the AAA dependency before exposing an earlier component's write.

The existing controller-specific parser, common credential projection, section
owner, operation/task journal and credential application receipts remain in use.
No shared CRD, actor, transport or other infrastructure provider was changed.

## Native setup and qualified lifecycle

[Passing native run](aaa-site-manifest-native.log): test **71.81 seconds**, package
**73.972 seconds**.

The test used two isolated non-ISE dual RADIUS/TACACS registrations and a new empty
area. The existing isolated registration helper supplied explicit ports, retries
and timeout and verified native UUID/metadata readback. This prerequisite setup
is separate from public site-settings reconciliation; it does not prove combined
cross-section registration/apply recovery in one config.

The public site-settings reconciler then completed four source generations:

1. Initial network TACACS and client RADIUS settings with generated version-1
   secret projections.
2. Version-2 secret rotation. Deliberately stale version-1 projections refused
   execution without a native write or journal operation before projection update.
3. Adding secondary servers for both groups with unchanged credential version 2.
4. Removing both secondary servers with unchanged credential version 2.

Each phase produced exactly one site PUT/task, reached current-generation InSync,
retained one exact site-UUID/config-owner/two-version credential receipt, cleared
the active journal and passed repeated simulated worker restart/no-op checks.
The test checked the exact generated secret values in memory before transport;
secret values were never printed or stored in Kubernetes status/evidence. Global
AAA metadata remained unchanged, and the area remained childless and device-free.
External authentication was disabled before setup and after completion; no
external-authentication write endpoint was called.

The test removed its exact area UUID, then both captured registration UUIDs,
verified task completion/readback and restored the original empty registration
baseline. No operational device configuration was changed. No real RADIUS or
TACACS service was provided, so this does not qualify actual authentication.
Kubernetes persistence and worker restart are simulated; native requests are real.

## Regression and examples

- [Affected race suite](aaa-site-prerequisites-affected.log): **6.263 seconds**,
  covering AAA, policy server, network credential and SWIM/Swim cases.
- [Dependency/verification race cases](aaa-site-dependency-verification.log):
  **2.226 seconds**. Missing primary/secondary records, incompatible or unknown
  server type/protocol, reader failure, replaced UUID before dispatch and no
  replay are covered. Missing registration cannot certify an accepted operation.
- Existing combined-network preflight tests now include a missing registration
  that blocks an earlier NTP write.
- `examples/configs/catalyst-center/aaa-prerequisites-apply.yaml` supplies matching
  registrations through the existing system_settings owner.
- `examples/configs/catalyst-center/aaa-apply.yaml` applies site settings afterward
  through the network_settings owner. Both examples parse, and their primary
  addresses/protocols/versions align. The examples were not applied to the lab.

## Diagnostic cleanup

An initial harness path/UUID mistake stopped before any AAA write; its exact empty
area was removed after identity, empty-scope and unset-setting checks. Three
subsequent requests were explicitly rejected for absent registrations. Their exact
captured area UUIDs were later checked for no children, no assigned devices, no
local AAA overrides and no local device credentials, then deleted with terminal
task and absence verification. [Cleanup evidence](aaa-diagnostic-cleanup.log).
No rejected AAA request was replayed and no real Kubernetes journal was cleared.
This work leaves no AAA fixture registrations or areas. The four earlier planned
AP diagnostic hierarchies are outside this cleanup and remain recorded separately.

## Remaining scope

ISE PSN/PAN registration relationships and integration readiness are not inferred
from plain-AAA address rules. Durable registration UUID lineage across operation
restart is not persisted by this submission guard. Combined cross-section
execution, installed worker/actor qualification, real RADIUS/TACACS traffic,
inheritance/reset/delete and broader site/server combinations remain incomplete.
Full pinned NetAsCode parity is not complete.

[Cisco site AAA API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/set-aaa-settings-for-a-site/)
documents both replacement groups and separate null/inheritance versus empty/unset
semantics; the current apply path continues to require both explicit groups.
