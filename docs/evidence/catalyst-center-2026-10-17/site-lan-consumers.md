# LAN automation site-reference fences

Site retirement now reuses the complete paginated LAN session reader to check
both session-level discovery sites and per-device discovery site overrides.
The [Cisco LAN Automation Status schema](https://developer.cisco.com/docs/catalyst-center/lan-automation-status/)
includes `discoveredDeviceSiteNameHierarchy` and `discoveryDevices` records with
`deviceSiteId` / `deviceSiteNameHierarchy`. Checking the session default alone
would miss an override outside that branch.

## Shared metadata and authority

The existing native reader now retains bounded per-device scope records, physical
identity, optional PnP identity and discovery/allocation flags. It validates
nonempty addresses, canonicalizes known serials and rejects malformed/duplicate
identities. A pending empty address remains observable and never grants physical
authority. Existing LAN link/readiness/session primitives still use the same
reader and transport; no additional session inventory implementation was added.
Unknown session/device fields are remembered as unsupported for retirement
without silently changing normal reporting into destructive authority.

Retirement requires every known scope to resolve consistently against current
site UUID/path inventory. Session default and per-device scopes at, above or below
the target block deletion. Conflicting UUID/path pairs, dangling sites, missing
legacy `discoveryDevices`, unsupported fields/actions and read failures also
block. STOP, descriptive status and false discovery/allocation flags are never
proof that a session reference has been cleaned up. Complete unrelated branches
can pass. No LAN start, stop, reset, device or pool mutation is performed.

The check runs in the existing fresh native preflight before the shared one-use
writer. Late references use its certified no-dispatch retirement path and retain
section ownership/history. Existing retained operation/credential/effect fences
remain independent. The guard does not delete historical LAN session metadata or
invent cleanup receipts.

## Discovery assessment

The reviewed discovery executor currently has no direct site identifier and
rejects per-site deployment selection. That model gap remains on the discovery
ledger; this change does not treat global discovery reporting as per-site parity.
PnP and assigned-device checks remain responsible for known onboarding consumers.
Additional operational reference families still require complete assessment.

## Qualification boundary

Fixtures cover default/explicit scopes, safe unrelated branches, target/ancestor/
descendant references, UUID/path disagreement, dangling references, pending
addresses, missing legacy scope data, unsupported fields/actions and no STOP or
flag-based completion shortcut. Guarded execution fixtures inject late session,
per-device and incomplete/error scope before dispatch. Existing native LAN
readiness, allocator, session and link regressions cover the shared reader.

The [site race gate](site-lan-consumers-regression.log) passes
**67 families / 1005 subcases** in **86.498 s**.
The [temporary Kubernetes API gate](site-lan-consumers-api.log) passes
**8 families / 114 scenarios** in **77.75 s**.
The [shared LAN primitive gate](site-lan-consumers-lan-regression.log) passes
**66 families / 565 subcases** in **222.307 s**.

The [first native attempt](site-lan-consumers-native-interrupted.log) stopped on
a stale read-only child-recreation observation. Its original acknowledged task
was retained. A subsequent read-only diagnostic reconstructed the observer and
matched key/history/parent proof; transient native read consistency is suspected,
not proven at a particular field. No production check was bypassed. The
[exact retained settlement](site-lan-consumers-retained-settlement.log) passed in
**35.78 s** (**38.209 s** Go duration), settling that same task with **zero Create
requests**, then issuing only its two verified cleanup Deletes. The original
baseline was restored before another cycle began.

The qualifier now waits boundedly on stale/busy observation and readback under
the original task and unchanged source; it never reconstructs a submission
writer. The [fresh final native cycle](site-lan-consumers-native-final.log) passed
in **83.15 s** (**85.592 s** Go duration): four Create POSTs/four Deletes, eight
original tasks, four archived/four current action rows, retained ownership,
terminal idle journal and restored typed inventory. Both private checkpoints
remain available. All LAN inventory requests were reads; no LAN workflow was
started, stopped or reset.

The [endpoint check](site-lan-consumers-endpoint.json) found HTTP 200 and an empty
first page. The full paginated guard exercised this empty-session lab state;
native populated-session/override behavior is not qualified. The
[validation audit](site-lan-consumers-validation.json) preserves separate initial,
retained-recovery and final harness hashes. Passed production/fixture sources
remained unchanged during the harness fix. Kubernetes persistence/restart is
simulated, not installed/public retirement routing.

## Remaining gaps

Legacy sessions without complete per-device scope require an independently
qualified scope/provenance path; they remain held. Native populated-session scope
and per-device overrides, full operational reference coverage, policy cleanup,
public routing, configuration deletion, building/floor and installed complete
cycles remain pending. Full pinned 93-resource parity remains incomplete.


Subsequent [public lifecycle qualification](site-public-lifecycle.md) enables the
qualified private-site manifest route. The historical gates and native boundaries
recorded above are unchanged; installed execution and populated native LAN scope
qualification remain pending.
