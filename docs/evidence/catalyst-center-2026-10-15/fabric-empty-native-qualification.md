# Empty fabric native qualification and correlated failure reporting

The previous [public authentication lifecycle](fabric-authentication-public.md)
was fixture-qualified with no native fabric available. This investigation tried
to create an isolated empty fabric without onboarding, assigning or changing any
switch. It identified a native application-lookup failure and verified cleanup;
it did **not** qualify an accepted authentication-profile update.

## Guarded scope

Every mutation was individually armed with an exact method, endpoint and body.
The trace checked unchanged global authentication profiles and device inventory,
zero fabric members, zero assigned devices, zero fabric zones, no fixture
children and no foreign fabric or hierarchy changes. A failed or uncertain
attempt consumed its armed request and stopped the run. Raw response diagnostics
and baselines stay in private mode-700 directories with mode-600 files; only
sanitized task/scope metadata is included here.

The fixture was `Global/CVK-Q-Auth-20261015-163915-000000000`, area UUID
`e115620c-5e8d-43c8-ad75-c830d46ae1e7`. Area creation task
`01a1406e-aa13-7947-aad6-152abc106ec9` completed. Its first Closed Authentication
fabric request returned HTTP 400 without a task receipt. Its original response
body was not captured, so the exact rejection reason remains unknown. The test
stopped before any authentication PUT or further profile fixture was created.

Read-only task-window and audit-log inspection found no nonterminal operation
in the bounded original request window. Native readback showed zero fabrics,
zones and devices assigned to the area. The audit event recorded receipt of the
request but did not provide its rejection reason. The original request was not
replayed.

## Distinct intent and prerequisite correction

A separate No Authentication intent on the exact retained empty area was
accepted as task `01a14076-2b2e-70dd-8551-9ed29c8b161c`. Its complete two-node
tree was terminal, with root error **NCSP11008**. The diagnostic reports a
required package/application handler could not be found for type
`ConnectivityDomain` with a null qualifier.

The documented [installed-package summary API](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/cisco-dna-center-packages-summary/)
returned 49 packages, including `sda` **2.740.65729**. This contradicts a simple
claim that SDA is uninstalled. Generic application-health events included healthy
entries, but that partial event view does not prove this operation's handler is
available. The precise cause of the lookup failure remains unproven.

Local and inherited Wired Endpoint Data Collection was initially disabled. Cisco
documents IP Device Tracking/Wired Endpoint Data Collection as a prerequisite
for [fabric site creation](https://www.cisco.com/c/en/us/td/docs/cloud-systems-management/network-automation-and-management/catalyst-center-assurance/2-3-7/b_cisco_catalyst_assurance_2_3_7_ug/m_monitor-and-troubleshoot-sd-access-fabric-networks.html).
Using existing telemetry logic, task `01a1407d-0ea3-71a7-86af-43c0c1a04d07`
enabled it only on the new, unassigned area. The initial harness JSON round-trip
changed typed values and produced a comparison discrepancy. A type-preserving
copy then verified local wired collection and a no-op without another telemetry
PUT. Other settings retained their effective inherited values; AAA metadata for
both groups was empty.

With that prerequisite verified, a corrected No Authentication intent was
accepted as task `01a14080-7670-78c6-b60e-108f736c3a0b`. Its two-node tree again
ended with **NCSP11008**. Thus enabling the documented wired prerequisite did
not resolve the lookup failure. No fabric was created and no authentication
profile update ran. This is evidence about this empty fixture context, not proof
that every SDA operation on the appliance is unavailable.

## Exact cleanup and observation recovery

After independently checking every node of the final failed task was terminal,
the cleanup guard again proved zero fabrics/zones/assigned devices and no child
sites. DELETE of the exact area UUID was accepted as
`01a14082-0f78-7058-8679-81d78a5911da`. The first tree read was incomplete.
DELETE was not repeated. A later read-only observer verified the same successful
task and its complete tree twice, absence of the exact area and descendants,
zero fabrics/zones and equality to the original three-profile global baseline.
Three device inventory entries remained stable across those observations.

The cleanup observer passed in **1.65 seconds** (package **3.733 seconds**).
All resources created for this investigation are removed. No device role,
assignment, provisioning, upgrade or global controller policy was changed.
The final harness reuses the verified wired-prerequisite helper before each new
empty-fabric attempt, preserving effective telemetry scalar/list types and
accepting already-enabled inherited collection without another PUT. It also
permits a five-second grace for reobserving an incomplete tree of the same
acknowledged task. Genuine task failures stop immediately;
foreign roots never satisfy completion. It never retries the mutation.

## CVK implementation improvement

Public group recovery previously hid a native task failure behind generic
`GroupVerificationPending`. It now publishes `ControllerTaskFailed` and Failed
status when the existing reader produces a correlated typed task failure.
NCSP11008 adds fixed application-availability guidance; it does not assert that
a package is absent. The journal, task and every member reservation stay held;
no completion receipt, retry or release is inferred.

Task-tree validation now checks the entire rooted graph and timestamps before
classifying a failed row. A failed orphan, missing root or invalid end time
cannot masquerade as this operation's task failure. Only bounded native machine
codes reach status; free-form failure/progress/data remains excluded. Public
fixtures check repeated reconciliation, secret-marker exclusion, retained
Accepted journal, held reservations and one native request.

The focused status/tree suite passed in **5.353 seconds**. The affected fabric,
authentication, configuration/admission, SWIM and example race suite passed in
**215.216 seconds**. Native read-only classification of the recorded NCSP11008 task passed in
**0.43 seconds** (package **2.544 seconds**). Task-publication grace fixtures
passed initially in **3.349 seconds** and again in **3.348 seconds** after
sharing the wired-prerequisite helper. Nexus Dashboard tests passed using valid
cached results. Strict documentation and exact 93-resource-type ledger checks passed.
Adjacent logs retain the sanitized evidence.

## Required follow-up

- Determine why the installed SDA/application stack cannot resolve
  `ConnectivityDomain` for this empty fixture. Controller application/registration
  diagnostics or a supported fabric fixture are needed; reinstalling SDA or
  restarting services is not justified by package inventory alone.
- Re-establish the verified wired-collection prerequisite on any new isolated site
  and provide compatible AAA context before authentication-enabled qualification. The original
  Closed Authentication HTTP 400 reason must not be guessed.
- Qualify accepted scoped profile changes/restoration, installed actors and
  on-device BPDU/authentication behavior once an appropriate native fabric exists.
- Continue combined prerequisite transitions, broader field/default combinations
  and ownership-aware reset/removal implementation.

No controller package was installed, removed or restarted. Full pinned
NetAsCode parity and native fabric authentication qualification remain incomplete.
