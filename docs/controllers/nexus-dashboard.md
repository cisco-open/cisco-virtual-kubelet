# Nexus Dashboard controller adapter

The `nexus-dashboard` adapter connects a `NetworkController` to Cisco Nexus
Dashboard (ND) 4.x through the `/api/v1` APIs (Infra login, Manage). It does
not use the NDFC legacy `/appcenter/...` APIs.

**Current scope (phases 1-2):** connect, authenticate, report health, and read
the switch inventory (read-only). Node creation is planned for a later phase;
intent reconciliation is not implemented. The Network as Code stripe is `nd`, declared but not reconciled.

## What it checks

Every `spec.connection.healthCheckInterval` (default 1m) the worker:

1. Logs in with `POST /api/v1/infra/login` using the mounted credentials.
2. Calls `GET /api/v1/manage/fabrics` with the `AuthCookie` token.
3. Writes `Authenticated`, `APICompatible`, `Ready`, the `health` capability,
   `phase`, and the attempt/success timestamps to `status`.

| Outcome | Authenticated | APICompatible | Phase |
|---|---|---|---|
| Login and Manage API succeed | True | True | Ready |
| Bad credentials (401/403 on login) | False | False | Error |
| Missing or empty credential files | False | False | Error |
| Login works, Manage API 404 | True | False | Degraded |
| Login works, Manage API 403 | True | False | Degraded |
| Unreachable, TLS failure, 5xx, 429 | False | False | Degraded |

Messages are redacted and limited to 512 characters. Tokens and passwords never
reach status, events, or logs. The token is cached and rebuilt on projected
credential/CA rotation, after `MaxSessionLifetime`, and once after a 401 on a
read request. Only GET requests are retried.

## Inventory

Every 5 minutes (jittered by 10%) the worker lists fabrics with
`GET /api/v1/manage/fabrics`, then the switches of each fabric with
`GET /api/v1/manage/fabrics/{fabricName}/switches`, following
`meta.counts.remaining`. Each switch becomes an in-memory `InventoryItem`
(serial from `switchId`, hostname, management address from
`fabricManagementIp`, model, software version, fabric, and reachability from
`additionalData.discoveryStatus == ok`).

- A switch is adoptable only if its model looks like NX-OS (`N9K-...`), and it
  has a serial and a management address. Others are skipped with a reason.
- The `inventory` capability in `status.capabilities` reports counts only, for
  example `3 switches in 1 fabrics; 2 adoptable NX-OS, 1 skipped`. Serials,
  hostnames, and addresses are never written to status or logs.
- If any fabric fails to list, the whole refresh fails and the last complete
  snapshot is kept; the capability shows `Supported=false` with a redacted
  reason.
- There is no fabric or role filter. `role` comes from ND's `switchRole`.
- Inventory only becomes `CiscoDevice` objects when device adoption is
  enabled (next section).

## Device adoption

Off by default. With `spec.deviceAdoption.enabled: true`, every reachable
NX-OS switch from a complete inventory refresh becomes a standalone
`CiscoDevice` (driver `NXOS`) in the NetworkController's namespace.

```yaml
spec:
  deviceAdoption:
    enabled: true
    defaults:                       # required when enabled
      username: admin
      credentialSecretRef: {name: switch-creds}   # key "password"
      tls: {enabled: true, insecureSkipVerify: true}
    scopeOverrides:                 # per ND fabric; omitted fields inherit
      - scope: dc2-fabric
        credentialSecretRef: {name: dc2-switch-creds}
```

### Labels for grouping devices

`defaults.labels` and `scopeOverrides[].labels` add your own labels to every
adopted device, for example to group switches into upgrade waves:

```yaml
spec:
  deviceAdoption:
    defaults:
      labels: {upgrade-wave: "1", owner: netops}
    scopeOverrides:
      - scope: dc2-fabric
        labels: {upgrade-wave: "2"}      # merged per key over the defaults
```

They are written to `metadata.labels` (for `kubectl get ciscodevices -l
upgrade-wave=2`) and to `spec.labels` (projected onto the Node, for
`nodeSelector`). Labels users add themselves are never touched. A label you
remove from the NetworkController is removed from the devices again, because
the adapter records the keys it applied in the annotation
`cisco.vk/nd-managed-labels`. The `nd.cisco.vk/`, `cisco.vk/`,
`topology.cisco.vk/` and `kubernetes.io`/`k8s.io` namespaces are reserved, and
at most 16 labels are allowed per block. CVK does not act on these labels
itself; something else, such as a Job with a `nodeSelector`, has to.

### Removing devices

```yaml
spec:
  deviceAdoption:
    removal:
      policy: Prune          # Retain (default) | Prune
      gracePeriod: 24h       # 10m - 720h, default 24h
      maxPrunePercent: 25    # default 25
```

With `Prune`, an adopted CiscoDevice is deleted once it has been missing for
the grace period (measured from the annotation, so it survives worker
restarts). Deleting the CiscoDevice makes the CiscoDevice controller remove
the worker and the Node. Guards:

- only devices with this controller's UID label are ever deleted;
- an empty inventory marks and deletes nothing;
- if more than `maxPrunePercent` of the adopted devices are due at once,
  nothing is deleted and they are reported as `prune-guarded` (one device is
  always allowed, so a small fleet can still shrink);
- delete uses a UID precondition.

The worker role carries the `delete` verb for this. RBAC cannot depend on the
policy, so every ND worker holds it, even with `Retain`.

ND does not expose device passwords, so the operator supplies them. The
adapter writes only the Secret *name* and never reads the Secret; it has no
Secrets permission. Create the Secret in the same namespace, with a `password`
key, like any other `CiscoDevice` credential.

What gets created, per switch:

- name `nd-<serial>` (lowercased, unsafe characters replaced; long serials get
  a hash suffix), `spec.physicalIdentity` = serial, `spec.address` = ND's
  fabric management IP;
- `spec.nodeName` = the ND hostname, set only at creation and only when the
  hostname is already a valid Node name, unique in ND, and not used by another
  CiscoDevice. Otherwise it is left empty and the name defaults to
  `metadata.name`. It is immutable, so later hostname changes in ND do not
  rename the Node;
- labels `nd.cisco.vk/fabric`, `nd.cisco.vk/role`, `nd.cisco.vk/model` on both
  the CiscoDevice and its Node;
- ownership label `cisco.vk/network-controller-uid`. There is deliberately no
  ownerReference, so deleting the NetworkController does not garbage-collect
  devices and Nodes.

Safety rules:

- The adapter only touches CiscoDevices that carry its controller's UID label.
  A device with the same name that it did not create is counted as a name
  conflict and left alone.
- Updates are merge patches of the fields the adapter owns (address, username,
  secret, TLS, labels). Labels, ports, taints and other fields that users add
  are kept.
- Adoption runs only after a complete successful refresh. A failed or empty
  refresh changes nothing.
- A device whose switch is missing from a complete refresh gets the annotation
  `cisco.vk/nd-missing-since` (first-seen time, RFC 3339). It is removed again
  if the switch returns. An unreachable or non-NX-OS switch is still in ND and
  is not "missing". By default (policy `Retain`) nothing is ever deleted.
- Unreachable switches (`discoveryStatus` not `ok`), non-NX-OS switches, and
  switches with a serial that is not a valid `physicalIdentity` are not
  adopted.
- The `device-adoption` capability reports counts only, for example
  `2 created, 0 updated, 1 unchanged; 0 unreachable, 0 invalid, 0 name
  conflicts, 0 failed`.

The ND worker is bound to `cisco-virtual-kubelet-controller-worker-device-adoption`:
the base worker role plus get/list/watch/create/update/patch on `ciscodevices`
(delete but no deletecollection, no Secrets, no `ciscodevices/status`). The role is static, so an
ND worker holds it even when adoption is disabled.

## Credential Secret

The Secret is mounted read-only; the worker reads one file per key:

| Key | Required | Notes |
|---|---|---|
| `username` | yes | ND local or remote-auth user. A read-only role is enough. |
| `password` | yes | |
| `domain` | no | ND login domain. Defaults to `local`. |

```bash
kubectl create secret generic nd-credentials \
  --from-literal=username=cvk-reader \
  --from-literal=password='...' \
  --from-literal=domain=local
```

## Example

```yaml
apiVersion: cisco.vk/v1alpha1
kind: NetworkController
metadata:
  name: nd-lab
spec:
  type: nexus-dashboard
  endpoint: https://nd.example.com
  credentialSecretRef:
    name: nd-credentials
  tls:
    caConfigMapRef:
      name: nd-ca
      key: ca.crt
```

TLS verification is on by default. Use `tls.caConfigMapRef` for a private CA.
`tls.insecureSkipVerify: true` is for controlled labs only and cannot be
combined with a CA reference.

Rate limiting defaults to 5 requests per second with a burst of 10, and
`spec.connection.rateLimit` overrides it.

## Verifying

```bash
kubectl get networkcontroller nd-lab
kubectl get networkcontroller nd-lab -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}{"\n"}{end}'
```

## API notes

- Login body: `{"domain","userName","userPasswd"}`; the response carries `token`
  (also `jwttoken`). Requests send `Cookie: AuthCookie=<token>`.
- Documented example tokens expire about 20 minutes after issue; the adapter
  refreshes on its own schedule and on 401.
- The Manage API is GA from ND 4.2.1; Early Access releases may change schemas.
- The switch schema was checked against a live ND 4.x capture;
  `testdata/switches_page.json` is that response with identifiers replaced.
  The `max` and `offset` paging parameters are accepted and `offset` is
  honored (an offset past the end returns an empty list). Behavior with more
  than one page of data has not been observed on a live ND.
