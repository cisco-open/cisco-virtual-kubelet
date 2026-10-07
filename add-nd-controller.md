# add-nd-controller.md - Nexus Dashboard (ND) controller adapter for CVK

Progress tracker. Mark items `[x]` as they land. Status legend: `[ ]` todo, `[~]` in progress, `[x]` done.

## Context
CVK has a neutral controller-adapter framework (`internal/controlleradapter/`: `Register`, `Descriptor`, `Factory`, `ValidateConfigContract`; manager `internal/controller/networkcontroller_controller.go`; worker `cmd/cisco-vk/controller_worker.go`). No concrete adapter exists: `cmd/cisco-vk/controller_adapters_register.go` is empty. Goal: add ND as the first adapter using the new ND `/api/v1` APIs (not NDFC legacy `/appcenter/...`), incrementally: (1) "lights on" = connect/auth/health, (2) inventory read, (3) inventory -> standalone CVK nodes.

## Decisions
- Each ND switch becomes a `CiscoDevice` CR (driver `NXOS`), reconciled by the existing `CiscoDeviceReconciler`.
- ND API target: ND 4.x `/api/v1` manage APIs.
- `NetworkController.spec.type` = `nexus-dashboard`. NaC data model root key = `nd`. Config/intent reconciliation NOT implemented now.

## Guardrails (docs/controller-extension-guide.md)
Private package; factory validates local options only (no dial, no goroutines); single import in `controller_adapters_register.go`; no product branches in neutral registry/manager/driver factory; credentials only as file paths from `Options`; TLS verify on by default; extra RBAC = static reviewed ClusterRole + manager allowlist; removing package + import leaves everything else unchanged.

## Phase 0 - Groundwork
- [~] Verify ND API facts: login/token/cookie/expiry/min version VERIFIED (docs); switch inventory endpoint + schema, pagination, rate limits NOT verified (only in in-product swagger `https://<nd>/help-center/swagger/`; do in Phase 2 against a live ND)
- [ ] (Phase 2) Save sanitized JSON fixtures in `internal/controlleradapter/nexusdashboard/testdata/`
- [x] Define descriptor (`registry.go:42`): Type `nexus-dashboard`, DisplayName "Nexus Dashboard", Capabilities `["health"]` (later + `inventory`)
- [x] Decide `NetAsCode` block: format `netascode-*` (match upstream NaC ND stripe, root `nd`); check `validateRegistration`/`ValidateConfigContract` accept empty `Sections`, else declare section `nd` and leave unreconciled
- [x] Decide `WorkerClusterRole`: `DefaultWorkerClusterRole` for phases 1-2; dedicated ND role in phase 3

## Phase 1 - "Switching on the lights" (connect, auth, health)  [PR 1]
Package `internal/controlleradapter/nexusdashboard/`
- [x] `register.go`: `init()` registers descriptor + factory
- [x] `adapter.go`: Factory validates Options only; `SetupWithManager` adds health Runnable
- [x] `client.go`: private ND client on `internal/configengine/transport.RESTClient` (+ `RetryIdempotent` for GET); TLS from `Options.CAPath`; login, token cache, one re-login on 401 for GET only (pattern: `internal/drivers/nxos/dme_client.go`)
- [x] Credentials from `Options.CredentialPath` files (`username`, `password`, optional `domain`); drop session on `MaterialRotation`
- [x] Health loop at `connection.healthCheckInterval`: sets `Authenticated`, `APICompatible`, `Ready`, capability status (confirm worker vs manager status ownership in `networkcontroller_controller.go` `updateAvailableStatus`; never patch spec/metadata/finalizers)
- [x] Add blank import to `cmd/cisco-vk/controller_adapters_register.go`
- [x] Tests (httptest fake ND): descriptor validation, factory doesn't dial, login ok/401/expired, TLS (bad CA fails, custom CA ok), redaction, credential rotation, rate limit, cancellation, 429/5xx backoff
- [x] `import_boundary_test.go` still passes; build works with and without the import
- [x] Docs: `docs/controllers/nexus-dashboard.md` (example `NetworkController` + credential Secret layout), link from extension guide
- Exit: `NetworkController` -> fake/lab ND gives `Authenticated/APICompatible/Ready=True`; bad password -> `Authenticated=False` with redacted message

## Phase 2 - Inventory read-only  [PR 2]
- [ ] `inventory.go`: paginated switch list -> private `ndSwitch` -> `InventoryItem{Serial, Hostname, MgmtAddress, Model, Platform, Fabric, Role, SoftwareVersion, Reachable}`
- [ ] Periodic resync (default ~5m, jittered) as manager Runnable; add `inventory` capability
- [ ] Status: counts/summary only (no secrets); keep last-known-good on partial failure
- [ ] Fabric/role filter (use whatever `NetworkControllerConfig` spec allows, see `api/config/v1alpha1/networkcontrollerconfig_types.go`)
- [ ] Tests: pagination, empty, partial failure, non-NX-OS skipped with reason

## Phase 3 - Inventory -> standalone CVK nodes  [PR 3]
- [ ] Resolve open question 1 (switch credentials) before starting
- [ ] Static ND worker ClusterRole (`ciscodevices` CRUD; no Secrets, no controller finalizers) + registry allow-list + manager `bind` policy + chart `controller-worker-rbac.yaml` + `chart_rbac_test.go`
- [ ] `deviceSync.go`: create/update `CiscoDevice` (driver `NXOS`) per reachable NX-OS switch: deterministic name from serial, `address`, `physicalIdentity` (write-once), labels (controller, fabric, role, model), `nodeName` from hostname w/ collision handling, `credentialSecretRef`, `tls`
- [ ] Ownership via label/ownerRef + server-side apply field manager; never touch unowned `CiscoDevice`s
- [ ] Removal policy: annotate missing (`cisco.vk/nd-missing-since`), default no delete; opt-in `prune` after grace; mass-delete guard (empty/errored/shrink > N%)
- [ ] Envtest (`make test-envtest`): create, idempotent resync, ownership isolation, prune guards, name collisions
- [ ] Kind E2E: `kubectl get ciscodevices` and `kubectl get nodes` show ND switches

## Phase 4 - Later (out of scope now)
- [ ] NaC intent reconciliation against ND (root `nd`, report-before-apply)
- [ ] ND state into node conditions; non-NX-OS platforms via ND; `kubectl ciscovk` ND inventory view; credential sourcing improvements

## Open questions
1. Switch credentials: ND doesn't expose device passwords. Recommend one shared Secret + per-fabric override. `CiscoDevice.credentialSecretRef` is same-namespace, so Secrets must live in the CiscoDevice namespace.
2. Assumed switches' mgmt IPs are reachable from the cluster directly.
3. Does the ND lab need `insecureSkipVerify`; where is the lab for opt-in live tests (pattern: `.github/workflows/lab-ci-*.yaml`)?

## Critical files
- New: `internal/controlleradapter/nexusdashboard/*`, `docs/controllers/nexus-dashboard.md`
- Edit: `cmd/cisco-vk/controller_adapters_register.go`; phase 3: `internal/controlleradapter/registry.go`, `internal/controller/networkcontroller_controller.go`, `charts/cisco-virtual-kubelet/templates/controller-worker-rbac.yaml`
- Reuse: `internal/configengine/transport/{rest_client,retry,redact}.go`, `internal/tlsutil/client.go`, `internal/controlleradapter/contract.go`, `internal/drivers/nxos/dme_client.go`

## Verification (every phase)
`make test` (race), `make test-envtest`, `make lint`; build with/without import; chart render + RBAC tests for new role; kind E2E against fake or lab ND.

## Progress log
- 2026-10-07: Plan drafted; decisions above confirmed.
- 2026-10-07: Plan copied to repo root; starting Phase 0.
- 2026-10-07: Phase 1 code landed (client, health loop, register, tests, docs, blank import). Decisions: descriptor declares section `nd` and model version `nd-4.2` (registry rejects empty sections/versions); Ready=Authenticated&&APICompatible, probe = GET /api/v1/manage/fabrics; golang.org/x/time promoted to direct dep. Not yet done: lint, kind/lab exit test, `-race` run. Local `go build ./cmd/...` fails at link (macOS SDK/clang tbd mismatch, unrelated); verified via `go vet` + `CGO_ENABLED=0 go test`.
