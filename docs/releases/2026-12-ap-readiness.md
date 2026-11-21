# AP app-hosting assessment for 1 December 2026

Assessment date: **21 November 2026**. Target: AP scope for the **1 December
2026 integration commit**. This is a feature assessment, not a declaration that
a December release has been published or that its full release gates passed.

## Baseline and decision

The assessed remote branch is `wlc` at
`a8fbcfcd7e9ebd9e5b5783e8db6be2b57751642b`, five commits ahead of `main` at
`e68dd3d2454485e535fc30468847895044a850dd`. The feature adds a native Catalyst
AP IOx CAF driver and an optional direct-AP metrics exporter. There is no WLC
adapter in this change.

**Recommended maturity: Alpha, with bounded platform qualification.** The
initial branch needed fixes before inclusion: its port and capacity defaults
were bypassed by normal integration, its synthetic inventory could not resolve
Kubernetes identities reliably, lifecycle retries could remove successfully
installed applications, and metrics failures could appear successful. The
changes below address these software defects. Final hardware and protected CI
evidence must name the reviewed candidate commit.

## Assessment and remediation

| Area | Finding and resulting behavior |
| --- | --- |
| Connection defaults | Shared config selected port 80/443 before the AP constructor could default to 8443. AP config now retains CA settings, enables HTTPS, and selects 8443 unless explicitly overridden. |
| Capacity and scheduling | Shared CRD/node defaults advertised 16 Pods and fictitious initial resource quota. AP nodes now advertise at most two single-container Pods, honor a lower one-Pod limit, omit unknown CPU/memory/storage quota, and identify as `platform: cisco-ap`. |
| Pod contract | Untranslated `envFrom`, Pod security/resources, DNS and other runtime settings were silently ignored. Unsupported settings are rejected before device requests; the guide supplies the required API-admission overrides. |
| Lifecycle recovery | Retries now resume deployed/activated/stopped states, observe intermediate states before the next mutation, reject unknown states, serialize lifecycle changes, and bound status waits. Invalid activation input is rejected before installation. |
| Inventory and recovery | Provider support resolves AP inventory by exact Pod UID, preserves unknown UIDs for orphan cleanup, retains bounded name-to-UID identities for force-delete discovery, and routes stopped/failed application recovery through the maintenance mutation guard. Forward recovery rechecks current UID/deletion state while serialized with deletion, preventing stale notifications from resurrecting apps. |
| Status | Polling preserves observed start/transition timestamps and image evidence. Failed applications request recovery without falsely claiming successful completion. Status reads remain read-only. |
| HTTP client | Concurrent authentication is cancellable, token renewal is bounded, redirects are rejected, response sizes are bounded, malformed inventory is an error, and raw device response bodies are excluded from errors. |
| Package input | Full archive validation rejects truncated/unsafe/duplicate entries and bounds expanded input. Packages must contain a nonempty regular `package.yaml`. Signature/digest verification and on-device expanded-footprint validation remain outside this contract. |
| Metrics | Failed/empty CLI parses no longer report success or retain stale device samples. Band labels follow reported frequency, output escaping/precision is corrected, SSH output is bounded, and server/configuration behavior has regression coverage. |
| Live harness | Cleanup targets only the exact IDs created by that test. Replacing a metrics app requires an explicit exact app ID, avoiding name/port-based deletion of unrelated apps. |
| Toolchain | The baseline scan found reachable [GO-2026-6859](https://pkg.go.dev/vuln/GO-2026-6859) in Go 1.26.10. Current module, CI and Docker pins now use verified Go 1.26.11; installer security floors are 1.26.11 / 1.27.4. Historical November notes are preserved. |
| Build and documentation | CI now tests, vets, cross-builds and vulnerability-scans the exporter's nested module. Its build helper includes redistribution notices. A platform guide and candidate manifests describe supported behavior and limitations. |

## Local validation

Validation environment: Darwin/ARM64, Go **1.26.11**. All AP hardware flags
were disabled. Mock HTTP/TLS/SSH endpoints ran on loopback; no physical AP or
WLC was modified.

| Check | Result |
| --- | --- |
| Full root `go test -race ./...` | Passed with AP hardware flags disabled |
| Full root `go vet ./...` | Passed |
| Exporter `go test -race -p 2 ./...` and `go vet -p 2 ./...` | Passed |
| Exporter `sh build.sh` | Passed; static Linux ARM64 ELF built with Go 1.26.11; seven redistribution notices matched upstream/project sources |
| Pinned `govulncheck@v1.7.0 ./...`, root + exporter + Terraform module | Passed; zero reachable findings with Go 1.26.11; inherited uncalled findings recorded below |
| CRD regeneration with pinned controller-gen | AP schema matched both committed CRD copies |
| Release-contract tests | 20 passed, including patched toolchain floors and CI pins |
| Pinned `actionlint@v1.7.12` | Passed |
| `mkdocs build --strict` | Passed using the existing Python environment and freshly installed locked browser runtime dependencies |
| `git diff --check` | Passed |

These are source-level local checks. Opt-in hardware tests and integration
checks requiring external lab/envtest assets are not established by this record.
The final image/Helm gates and protected CI still apply; a local documentation
build is not the full pinned CI environment.

## Required evidence before inclusion

1. Run the protected repository checks on the final reviewed commit, including
   root and nested-module tests/vet/vulnerability scans, generated-file parity,
   documentation, and the established release gates in `RELEASE.md`. Existing
   Cat8kv/Cat9k results do not qualify the AP transport.
2. On the intended AP model and firmware, capture package digest, architecture,
   CAF/API version and TLS trust configuration. Record install → activate →
   start → endpoint response → stop → deactivate → uninstall and unchanged
   pre-existing applications. Re-run against the enriched candidate; older
   smoke results do not cover these new state-transition waits.
3. Kill/restart the worker with a running app; confirm UID resolution, stable
   inventory/status and no duplicate installation. Delete a Pod while its
   informer entry is disappearing, and confirm exact-UID cleanup. Test token
   expiry and an interrupted install/start/delete with eventual recovery.
4. Schedule two eligible Pods and confirm a third remains unscheduled; test
   `maxPods: 1`, limited AP resources and a pre-existing non-CVK app. Verify
   package port mappings and application availability independently of CAF
   `RUNNING`. Do not claim CNI networking or probe-based readiness.
5. If the exporter is included in the demonstrated scope, verify its packaged
   ARM64 binary and notices, authenticate the AP SSH host key, provision SSH
   credentials through a protected IOx configuration path, check live CLI
   fixtures for every enabled radio, and test SSH failure/recovery and scrape
   output. The driver does not provision exporter configuration secrets.

## Inherited dependency findings

The baseline verbose Go scan also reported the following **uncalled** findings.
No affected symbol was reachable in that scan; they are not dismissed or treated
as proof that the final image gate will pass. Dependency upgrades are left out
of this AP feature change to avoid unrelated compatibility changes. The release
owner must assess the exact built artifacts and current advisory mappings.

| Advisory | Inherited version | Go database remediation |
| --- | --- | --- |
| [GO-2026-6107](https://pkg.go.dev/vuln/GO-2026-6107) | `go.etcd.io/etcd/client/pkg/v3 v3.7.0` | `v3.7.1`; vulnerable TLS-listener entry points are not called |
| [GO-2026-6094](https://pkg.go.dev/vuln/GO-2026-6094) | `github.com/google/cel-go v0.29.2` | `v0.30.0`; affected NativeTypes/ParseStructTag behavior is not called |
| [GO-2026-6860](https://pkg.go.dev/vuln/GO-2026-6860), [GO-2026-6866](https://pkg.go.dev/vuln/GO-2026-6866) | `golang.org/x/net v0.60.0` | `v0.62.0`; `html` is not imported by the controller |
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | `golang.org/x/crypto v0.57.0` | `v0.59.0` is the first stable release after removal of the affected OpenPGP package; AP uses SSH, not OpenPGP |

## Remaining boundaries

- This candidate supports prebuilt IOx packages at unauthenticated HTTP(S) URLs,
  not OCI pulls or authenticated artifact delivery. Use trusted immutable HTTPS
  endpoints. It does not enforce a package signature or content digest.
- Package and activation updates require replacement with a new Pod UID. CAF
  does not expose the original URL or annotation; in-place changes cannot always
  be detected after restart. Kubernetes CPU/memory limits, security settings,
  probes, volumes, exec/log streaming and Service networking are not translated.
- AP physical inventory is not attested. Managed topology, software campaigns,
  controller-based provisioning, broad model support and production maturity
  are not established by these tests.
- The public artifacts and global version selectors remain the November
  baseline. The final December preparation must choose its version, review the
  complete release delta and satisfy `RELEASE.md` before tagging/publication.

See the [AP operator guide](../configuration-ap.md) for configuration and the
[Cisco prerequisites](https://developer.cisco.com/docs/app-hosting-ap/device-prerequisites/)
and [hosting FAQ](https://developer.cisco.com/docs/app-hosting-ap/faqs/) for
platform limits. Cisco documentation describes platform capability; it is not
evidence that this candidate passed on the user's hardware.
