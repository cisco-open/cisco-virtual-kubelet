# Floor-image synchronous upload and bounded Report qualification

## Live-discovered receipt failure

The existing multipart primitive assumed `response.taskId`. Actual lab upload
returned HTTP 201 and a top-level floor DTO, with `groupInstanceUuid` binding the
requested floor and nonempty `imageInfo`. CVK therefore retained Unknown even
though the upload succeeded. It did not replay the uncertain request. See the
[original failure](floor-image-original-receipt-failure.log) and
[DTO diagnosis](floor-image-group-receipt-diagnosis.log).

The native DTO's `hierarchyName` is not the Site Design API's public
`nameHierarchy`; it must not be guessed into a source-model path comparison.
The decoder now validates HTTP 201, the exact requested native group UUID and
nonempty image metadata. Logical path/floor UUID binding is independently
rechecked through the existing site reader immediately before upload and before
publishing completion. No response URL is followed and no image bytes, contact
metadata or authentication material are stored in status.

The [pinned provider](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.6.1/internal/provider/resource_catalystcenter_floor_image.go)
uses the same `image` multipart part and upload endpoint. Cisco's
[upload API](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/uploads-floor-image/)
documents HTTP 201. New public execution uses the qualified synchronous DTO
contract; alternate new-upload response forms are refused rather than inferred.

## Existing foundations and durable response kind

The synchronous write uses the existing validated-response submission function,
write-ahead journal, ownership/source CAS and atomic action receipt. No common
API/CRD fields, controller provider interfaces, transport, ND or upgrade pathways
were changed. The source still binds model, image index and BinaryData snapshot
to one ConfigMap UID/resourceVersion, with no filesystem or URL reads.

New request identities use `floor-image-sync:<floor-hash>:<content-hash>`.
Retained legacy `floor-image` task operations keep their original task checks.
Both response kinds share the stable floor ownership key. A pruned legacy task
cannot become a synchronous receipt simply because its task ID is empty, and
a synchronous record cannot borrow a foreign task. Existing valid task receipts
also prevent byte-identical uploads being replayed by the new executor.

The synchronous response moves Submitting to Verifying using the existing CAS.
Final publication requires the exact current source, owner, nonce, response kind,
submission time and independently re-observed floor UUID. Missing/future/zero
submission time or unrelated create/group/cancellation/dependency evidence is
refused. Lost or malformed response remains fenced and is never retried.

## Report semantics

The [floor read contract](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/gets-a-floor/)
does not expose original image bytes or a content digest. Asset-aware Report now
validates the complete declaration and observes geometry/AP positions while
explicitly leaving image contents unverified. Matching geometry plus an image
produces Waiting / `ObservationIncomplete`, not Ready/InSync. Actual observable
geometry drift still reports Drifted. Null image intent does not create a false
observation gap. Invalid or missing assets are rejected before native reads, and
a ConfigMap race prevents publication of an old observation.

Apply completion is explicitly described as receipt-based upload completion,
not current remote byte equality. Out-of-band image replacement remains
undetectable by this supported read API.

## Qualification and cleanup

The final real-appliance public manifest test passed in 25.24s (package 26.133s).
It created a fresh isolated area/building/floor, verified native identities and a
floor-width update, uploaded a valid 640x480 PNG, retained a synchronous completion
receipt, restarted the simulated adapter and verified no replay. A source
BinaryData revision uploaded different bytes and replaced the same floor receipt
without duplicates. Report then preserved geometry observation while leaving
image contents unverified. The hierarchy was deleted with task and native
absence verification. See [live execution](floor-image-manifest-live.log).

These use real appliance requests with fake Kubernetes persistence and simulated
worker restarts. They do not qualify installed actors or real API-server
admission. The final run includes the pre-upload floor identity recheck.

Five earlier isolated diagnostic hierarchies were separately preflighted before
cleanup: original successful creation tasks, exact expected three-site scope,
current identity pins, no assigned network devices or planned APs and no foreign
children. Only those test identities were deleted, in reverse order, with each
deletion task and absence checked. The first cleanup request was explicitly
rejected with HTTP 415 for a missing JSON content-type; after correcting that
header, all preconditions were repeated. No uncertain upload was replayed.
All five diagnostic scopes were removed. A final independent inventory check
confirmed zero remaining sites across all seven exact test scopes, including
both successful runs. See [final cleanup check](floor-image-final-cleanup.log) and
[verified diagnostic cleanup](floor-image-diagnostic-cleanup.log).

The final affected race checks passed in 5.469s, including the composed
floor → image → planned-AP lifecycle and image revision without AP replay. The
initial broad regression caught that this older composition fixture still emitted
a generic image task receipt; it was corrected to match the actual HTTP 201 DTO.
No runtime acceptance or ordering check was relaxed to make that fixture pass.
Affected race tests cover native synchronous receipt validation, rejection of
task-envelope/identity/status errors, atomic legacy/synchronous publication,
pruned-task response-kind refusal, late floor replacement, malformed completion
journals and all asset-aware Report cases. See
[final affected checks](floor-image-final-tests.log).

The final full race suites passed: Catalyst Center 276.124s and shared
configuration 31.038s. The separate Nexus Dashboard adapter race suite passed in
4.622s. See [full regression](floor-image-full-regression.log) and
[Nexus Dashboard regression](floor-image-nd-regression.log).
Strict documentation build and `git diff --check` passed.

Installed rollout/admission, live JPEG/PDF uploads, broader AP/image/geometry
composition, removal/prune and remote-image drift detection remain unqualified.
No full resource or section completion flag changes.
