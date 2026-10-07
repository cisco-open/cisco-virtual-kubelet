# Nexus Dashboard controller adapter

The `nexus-dashboard` adapter connects a `NetworkController` to Cisco Nexus
Dashboard (ND) 4.x through the `/api/v1` APIs (Infra login, Manage). It does
not use the NDFC legacy `/appcenter/...` APIs.

**Current scope (phase 1):** connect, authenticate, and report health. Inventory
and node creation are planned in later phases; intent reconciliation is not
implemented. The Network as Code stripe is `nd`, declared but not reconciled.

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
- The switch inventory schema is only published in the in-product swagger
  (`https://<nd>/help-center/swagger/`) and has not been verified yet.
