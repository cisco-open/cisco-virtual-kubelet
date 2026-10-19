# Catalyst Center live preflight — 8 October 2026

Status: **preflight exercised; distribution, activation and Kubernetes SWIM
deployment remain incomplete**. The user authorized all three switches for
upgrade/downgrade testing, including reloads. This record must not be cited as
a successful software upgrade or a qualified production integration.

## Verified lab state

- Catalyst Center reports `3.2.3-75346.100`.
- Ubuntu16 (`192.0.2.43`) owns the three physical C9300-24P `CiscoDevice`
  resources in `cvk-live`. The deployed manager and six C9K workers use
  `cvk-tas-extentions:079f6b4f`; this Catalyst Center branch was not deployed
  over that installation. Ubuntu5 hosts a separate virtual-router demo.
- `.100`, `.101` and `.103` are reachable and report 17.18.3 in controller
  inventory. This is not a fresh independent gNOI/CLI version attestation.
- Both imported images match the devices' controller-bound product identifier
  `286315874`. Both still report image integrity `UNKNOWN`; matching uploaded
  bytes and a product identifier do not establish vendor integrity.
- All three devices belong to `Global/CVK/MUC07`, site
  `14943839-d66c-450d-b65a-5552eb1af040`, with role `ACCESS`.

## Actions and observations

Initial readiness jobs returned completed tasks with `isError: false`, but
reported that no golden image was assigned and returned zero validation
records. Treating parent-task success as readiness would therefore be incorrect.

17.18.04 (`058d4e65-d4d7-41a4-9f6e-ee5ac63c9276`) was then tagged golden only
for the above site, product identifier and ACCESS role. The previous tag was
false; task `01a11ad0-1f8d-7e9b-ba46-1edc2156b998` completed successfully and
the tag was read back as true. The lab-scoped tag remains in place. Neither the
global image policy nor the 26.02.01 golden tag was changed.

New readiness jobs completed on all three devices. [readiness.json](readiness.json)
contains sanitized, task-bound evidence, with no credentials, tokens or raw
configuration. The selected image was **17.18.04**; these checks do not qualify
26.02.01.

| Switch | Image applicability / startup / config register | Flash | Other findings |
| --- | --- | --- | --- |
| `.100` / `cat9k-live` | Success | 6,044 MB available; 2,620 MB requested | NETCONF transfer warning; xFSU ineligible because of STP root/forwarding-link condition |
| `.101` / `cat9k-lab-101` | Success | 3,132 MB available; 2,620 MB requested | NETCONF transfer warning; xFSU check successful |
| `.103` / `cat9k-lab-103` | Success | **Warning: 2,388 MB available; 2,620 MB requested** | NETCONF transfer warning; xFSU check successful |

The transfer check reports HTTPS/SCP reachable at `192.0.2.254` but a NETCONF
transfer failure, suggesting a default-VRF connectivity issue. That diagnostic
is not an independently proven root cause and does not prove HTTPS/SCP image
distribution works. No switch files were deleted and no automatic flash cleanup
was invoked. No image distribution or activation was submitted.

The 3.2.3 validation response places status inside a `resultDetails` array and
may omit top-level `status`. The decoder now handles that shape and the published
single-object form, rejects conflicting outcomes, and discards free-form device
output. In-progress responses also contained entries with timestamps but no
status; those must not count as successes.

## Branch changes and tests

- Added readiness submission, bounded validation-result reads, product-identity
  reads, and conservative evidence qualification to the CC client.
- Reused the adapter's existing REST transport and inventory paginator.
- Added tests for the empty-successful-task defect, nested status, conflicting
  status, stale/mismatched evidence, repeated pages, request filtering, and
  POST non-replay.
- Added opt-in live qualification from persisted receipts; it never sends a
  readiness or device-mutation POST itself.
- Repository-wide `go test ./...` passed. Race-enabled adapter, ND, lifecycle
  and direct software-upgrade tests passed. After the final wire-schema changes,
  the Catalyst Center race tests passed again.
- Live health and inventory passed with three devices and two images. Live
  readiness qualification **failed as intended on the returned warnings**;
  this is not a passing lab upgrade test.

## Remaining deployment work

1. Implement and qualify the modern SWIM execution contract. The current
   internal executor uses older endpoints. Modern activation also distributes
   images, and progress is reported by `networkDeviceImageUpdates` child
   workflows; replacing URLs without changing state/claim semantics is unsafe.
2. Complete durable Kubernetes operation storage and the authenticated bridge
   to existing device mutation claims, topology reservations, drain controls
   and independent device evidence. The executor is still unregistered and
   the worker still advertises SWIM as unsupported. A successful out-of-band
   API call would not validate this missing integration.
3. Diagnose the transfer warning and review a file-level cleanup plan for
   `.103`, preserving committed/running images, retained preparation receipts
   and app-hosting files. Do not invoke blanket automatic flash cleanup.
4. Use `.101` as the initial normal-reload canary after coordination and
   transfer checks pass; `.100` currently hosts the two drain-test workloads.
   Explicitly choose normal reload rather than assuming xFSU eligibility.
5. Distribute and activate 17.18.04, verify the exact physical identity and
   running version independently, settle workload/health gates, then exercise
   26.02.01 and the return downgrade. Extend to the other two switches only
   after the canary and restart/ambiguous-task recovery are qualified.

The authorization for all three switches is already present. No additional
user approval is being requested by this record.
