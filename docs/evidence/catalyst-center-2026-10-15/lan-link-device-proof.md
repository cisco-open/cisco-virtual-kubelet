# LAN ADD device-worker proof bridge

## Execution path

Retained acknowledged LAN ADD work still enters Verifying through the existing
group protocol. Verifying now calls the existing atomic application bridge with
a concrete proof provider instead of returning an unavailable-provider marker.
The provider creates bounded **read-only DeviceOperation** requests for both
admitted endpoint workers. No device credentials, SSH client, transport factory,
mutation permit, reservation or status store was added to the CC connector.

Each request runs, in order:

```text
show version
show running-config interface <exact physical interface>
show isis neighbors
```

The command/result profile requires one matching processor-board serial, exactly
one interface with the expected allocated IPv4 address and mask, IS-IS enabled,
no shutdown, and an UP neighbor on the exact local interface with the opposite
endpoint's independently read IPv4 address. Both endpoints must pass reciprocally.
The supported neighbor table fields follow Cisco's
[IOS / IOS-XE IS-IS command reference](https://www.cisco.com/c/en/us/td/docs/ios/iproute_isis/command/reference/irs_book/irs_is2.html).
Only explicit known interface abbreviations are recognised; generic prefix
matching is refused. Unfamiliar CLI layouts remain unqualified.

## Identity and lifecycle

Requests bind controller UID, immutable journal/group identity, task and exact
native completion boundary, device/worker UIDs and verified native result hash.
Their owner is the original NetworkController. The existing manager supplies
protected device/network-worker annotations; the connector does not manufacture
that identity envelope. Every admitted member remains subject to the existing
runtime authorizer before reads/creation and after successful proof.

Consumption requires matching immutable request spec, owner and binding, the
original protected network worker/device identity, observed generation, a
successful Ready status, and creation/start/completion strictly after native task
completion and no later than the observation time. Results older than five
minutes, missing outputs, errors, redaction, truncation and external artifacts
cannot prove convergence. The per-command 64 KiB cap follows existing
DeviceOperation inline limits.

Names are deterministic per operation/worker/native result. Repeated observation
cannot accumulate pending reads while a worker is offline. Existing DeviceOperation
TTL cleanup removes completed or failed reads after five minutes, allowing a new
read without a second retry journal, request-spec update or connector delete
permission. Pending reads wait for their existing worker; replacement worker UIDs
produce a separate bound identity. These are read retries, never another LAN
mutation or an inferred task outcome.

The existing native/managed observation bracket encloses this proof. The existing
source/owner/journal guards and atomic group writer publish Verified plus its
result receipt. Member completion, Lease release and journal retirement remain
the existing worker/manager settlement protocol's responsibility.

## Permissions and compatibility

Only the existing **Catalyst Center-specific worker role** gains `get` and
`create` for `deviceoperations`. It gains no device-operation status, metadata,
delete or general update permission, and no device credential or Lease write
permission. The existing compiled CC namespace-audit role contract carries the same two
verbs and remains checked against the rendered chart. Base/ND roles and the
DeviceOperation CRD are unchanged. The CC
descriptor's `lan-device-verification` capability changes its contract digest,
so a worker from the old contract cannot be mistaken for this implementation.
Deploy the matching manager/worker image and chart together before qualification.

## Validation and remaining qualification

Native HTTPS/fake Kubernetes tests cover reciprocal CLI success and malformed
or conflicting serial/interface/address/routing/neighbor output. The bridge
creates both reads, remains pending on repeated incomplete observations, consumes
explicit fake bound-worker results, and reaches the existing atomic receipt only
for valid proof. Wrong owner/spec/command/worker, failed or stale generations,
inconsistent Ready, pre-task/future timestamps, redacted/truncated results and
revoked authority hold. Native update count stays one; both claims remain held.

The focused LAN/descriptor race suite passed in **30.657s**. The exact CC role
permission test passed in **2.019s**; runtime descriptor/admission and managed
worker role checks passed in **4.024s**. See the
[focused](lan-link-device-proof-focused.log),
[RBAC](lan-link-device-proof-rbac.log) and
[runtime](lan-link-device-proof-runtime.log) logs. These are fixture/contract checks, **not** an
installed actor or physical-switch mutation qualification.

Fresh read-only CC qualification passed in **5.770s**: two stable native
adjacencies, maximum five sessions, zero active sessions and no recorded discovery
history. See [live prerequisites](lan-link-device-proof-live.log). This is not a
device-worker CLI proof or a submitted LAN operation.

The final complete affected race suite passed against the stable-name lifecycle
and corrected compiled role: Catalyst Center **317.182s**, controller/manager
**33.857s**, plus NetAsCode parsing, shared controller configuration, Nexus
Dashboard and managed protocol (cached passes). See
[full regression](lan-link-device-proof-regression.log). The exact rendered role
and compiled namespace-audit checks passed in **2.251s**; see
[role audit](lan-link-device-proof-role-audit.log). Strict documentation build and
whitespace validation passed.

Current installed qualification probe:

```text
kubectl --context kind-cisco-vk --request-timeout=5s get --raw=/readyz
Unable to connect to the server: EOF
```

The control-plane container is running, but containerd fatally fails to recover
stored sandbox metadata, leaving kubelet unable to connect to its CRI socket.
[Captured diagnostics](lan-link-device-proof-cluster.log) confirm that runtime
failure. No metadata was deleted or control plane rebuilt.

The subsequent [public add-only Apply path](lan-link-public-apply.md) now enables
new work under the installed all-member protocol. Actual lab LAN execution still
requires a suitable recorded discovery origin, live CLI profile qualification
and installed group actor/Lease settlement. No factory reset, LAN session or
device/link mutation was performed to manufacture that missing history.
