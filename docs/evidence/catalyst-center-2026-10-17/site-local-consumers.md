# Local settings and credential fences for site retirement

Staged site retirement now checks local-only NTP, DNS, DHCP, timezone, banner,
telemetry and AAA settings plus all six device-credential assignment slots.
Populated groups, explicit credential assignments/disables, missing or unknown
groups, malformed payloads, partial HTTP results and read failures refuse
retirement. No settings or credentials are reset or cleared by this check.

## Shared readers and retained authority

The existing network-setting and site-credential readers now expose their shared
local-only response envelope internally. Existing typed reporting/execution still
uses those same request and decoding paths; retirement applies stricter complete
absence validation to the response. There is no additional authentication/client,
Secret API access or separate transport stack.

Retained CVK setting/credential producer receipts remain independent blockers,
including explicit policies that currently produce an empty response. Unowned
populated local settings also block. A valid inherited `null` or empty setting
group contains no populated local policy. The appliance does not distinguish an
initial empty group from a reset through this API; this check does not establish
ownership of empty/default/reset state. Credential `{}` explicitly disables
inheritance and is conservatively blocked; only inherited `null` is absent.

Only fixed group names and `inherited`/`empty` classifications join the private
retirement native footprint. Actual setting values and credential IDs never
enter this snapshot or status. The keyed retirement MAC therefore changes even
when local state moves between empty and inherited: an in-flight candidate must
replan. Unknown native fields are not silently discarded to prove absence.

The existing source/owner/history guard, one-use no-dispatch proof and status CAS
remain unchanged. A stale Prepared proof is refused and can only take the normal
unsubmitted cancellation path; it is not silently rewritten. Already acknowledged
tasks keep their existing original-task/absence recovery contract after deletion.

## Qualification scope

Focused fixtures cover eight response families, local-only query scoping,
populated/empty/inherited and malformed/unknown/missing groups, HTTP partial/error
results, no leaked setting values and zero mutation requests. Site integration
cases cover late policy/credential arrival, empty/inherited shape changes and
Prepared drift with certified refusal and preserved custody. Existing network,
AAA and credential regressions cover the factored readers' typed callers.

The [site race regression](site-local-consumers-regression.log) passes
**66 families / 977 subcases** in **85.375 s**.
The [Kubernetes API gate](site-local-consumers-api.log) passes
**8 families / 110 scenarios** in **77.034 s**.
The [shared-reader regression](site-local-consumers-reader-regression.log) passes
**62 families / 619 subcases** in **57.209 s**.

The [native generated-area handoff cycle](site-local-consumers-native.log) passes
in **80.20 s** (**82.448 s** Go duration), including consumer reads before each
retirement. Four Create POSTs/four Deletes and eight distinct original tasks
complete parent/child creation, retirement, recreation under the replacement
parent and final retirement. Four archived/four current rows, retained custody,
a terminal idle journal and restoration of the typed site inventory baseline
are verified. No local settings or credentials were cleared by this flow.

A subsequent [read-only availability check](site-local-consumers-endpoint-types.json)
confirms HTTP 200/object responses for all eight endpoint families on one existing
area, building and floor each. It records no setting values and does not qualify
building/floor mutation or policy cleanup. The
[validation audit](site-local-consumers-validation.json) records exact source
hashes, task IDs and private checkpoint provenance. Native lifecycle persistence
and worker restarts remain simulated; public/installed Delete routing is not
qualified.

## Remaining gaps

These checks add consumer fences; they do not provide policy cleanup authority.
Other operational site references still need complete resource-level assessment,
public compiler/policy routing remains closed, and configuration deletion is not
qualified. Empty/default/reset provenance remains limited by the local API.
Building/floor and installed complete-cycle qualification remain pending. Full
site lifecycle and all 93 pinned resources remain incomplete.


## Subsequent operational references

A separate [LAN automation scope fence](site-lan-consumers.md) extends the
operational reference assessment. It does not complete all reference families or
public/installed routing. Historical source hashes and gates above are unchanged.
