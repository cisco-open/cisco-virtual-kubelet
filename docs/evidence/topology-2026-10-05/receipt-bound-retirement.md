# Receipt-bound IOS XE preparation retirement

Implementation: `f4a64d5da7946cb6667e436f410d7ef98f03791c`.
This follows the [0067e363 checkpoint](README.md); it does not supersede that
candidate's CI evidence with an assumed pass for new code.

## Defect and correction

The first preparation-invalidation implementation rejected all native
`install-version-state-in-progress` records. IOS XE 17.18 can leave that value
after a completed image add, including the actual 2 October `.101` capture.
Consequently, normal completed preparation could remain impossible to retire.

The IOS XE adapter now receives the immutable receipt's exact target/running
versions, positive source size, installation start and preparation completion.
For an `InProgress` target it uses a **single** native response to require:

- the response's device/local clock mapping of the **original** receipt window;
- exactly one successful completed install-add in that bounded window;
- the exact target at every location, all its packages added and its source
  package verified with the pinned size;
- idle installer state and one original committed running image per location;
- no later or unresolved native operation, unknown/unsettled unrelated image
  or inconsistent member inventory.

It reuses the existing native add-correlation implementation; the recovery
request's later timestamp cannot expand the correlation window. Missing clock,
history or source-size evidence stays blocked. Clock discontinuity that prevents
correlation is not repaired by guessing. The derived Installed observation is
retirement proof only, not a change to native inventory, an activation grant or
permission to reuse an invalidated receipt. Older history can be unavailable;
this remains a fail-closed limitation rather than a force-clear mechanism.

The manager does not open device sessions. Existing recovery RBAC, exact live
worker binding, immutable authority/receipt, device mutation Lease, bounded
observation deadline, gNOI Verify, single-supervisor restriction and final
status CAS are unchanged. The observer issues no install, activation,
configuration or file-deletion RPC. Ordinary app-hosting and other drivers gain
no permission or changed lifecycle behavior. No public API/CRD/policy changes
are needed for the internal request structure.

## Validation and limits

The adapter tests cover a two-day hold, both version directions, device-clock
offset, absent/malformed clocks, missing/reversed/future receipt intervals,
wrong size or abbreviated target, unverified/not-added packages, busy installer,
uncommitted running image, failed/old/later/duplicate add, unknown operation,
unsettled unrelated image and incomplete member. Worker tests assert the exact
receipt fields reach the observer, the original receipt is preserved, and no
install/activate call occurs. Existing lease/CAS race negatives remain passing.

`internal/drivers/iosxe/softwarelifecycle/testdata/retirement-iosxe-171803.json`
is a minimized projection of the saved 2 October physical inventory. It retains
every location, version, package name/state/size/verification and native
operation summary consumed by this observer. Credentials, package build-user
metadata and transaction detail are excluded. SHA-256:
`9da0daa62ca3ab039cd172a17949cdfa4aa6cdac88b091f5744ef1c6ac6d46a3`.
Its test uses a **controlled clock/receipt envelope** and separately verifies
that missing clock evidence fails. Replaying captured JSON is not a live
retirement run or proof of the original capture's HTTP-Date provenance.

The full Go race suite passed against the implementation content committed as
`f4a64d5d`:

```sh
go test -race -count=1 ./...
```

Private complete output: `/tmp/cvk-20261005-full-race.log`, SHA-256
`2558b662454b4fb3f9273be76a0be0084bcaa315d7e0e61a2eb656d4ced01d74`.
The earlier focused adapter/provider/controller race run also passed:
`/tmp/cvk-20261005-recovery-race.log`, SHA-256
`17b7210127c1459f4a5151d4c49076ef17b01647e4cb469619e739c995b72835`.
That earlier run preceded the additional captured-shape test; the full run
includes it.

The pinned Kubernetes 1.35 real-API suite also passed all **44** top-level
tests, with no skips or failures:

```sh
PATH="/tmp/cvk-tools:$PATH" make test-envtest
```

Provider, controller and topology-rollout packages completed successfully in
216.266, 19.503 and 5.086 seconds respectively. Private output:
`/tmp/cvk-20261005-envtest.log`, SHA-256
`f0704d7da522361b3e927dbeb3e442e4cc1d086a08d9de89537b573c52c751de`.
The offline harness/accounting suite passes 36 tests and the strict MkDocs
build passes. Required remote CI must still run on the pushed head; these
local passes do not stand in for that result or physical acceptance.

**Still open:** new-candidate deployment/migration qualification; actual
cancelled preparation retirement; exact inactive-image removal/replacement;
old-approval rejection; fresh planning/receipt; worker restart; both image
directions with independent service/path probes. The current physical runtime
is still `6f3686e9`. This change closes the identified observation-code gap,
not R3/E04/E05/F08 or the entire roadmap acceptance matrix.
