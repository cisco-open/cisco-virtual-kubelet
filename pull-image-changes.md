# Image Pull / Copy Recovery Changes (IOS-XE)

This document summarizes the code changes made to support **dynamic image delivery** for IOS-XE App Hosting when deploying Kubernetes pods via Cisco Virtual Kubelet.

The implemented behavior targets:

- IOS-XE **device pulls** an OCI tar from a **HTTP(S) URL** (e.g., through an HTTP proxy).
- Image pull behavior is driven by **Pod/container `imagePullPolicy`**.
- Credentials are taken from **`imagePullSecrets`** if present (token preferred, then basic).
- **Fallback behavior** (implemented as recovery):
  - App-hosting install fails → device attempts to fetch/copy the image → retry install.
- Destination on the XE device is controlled by a **pod annotation**, with a default fallback path.

---

## 1. Previous behavior (baseline)

Previously, for each container:

1. Convert pod to app-hosting config (`AppHostingConfig`)
2. POST app-hosting config to the device
3. Call IOS-XE app-hosting install RPC using `container.Image` directly as the `package` argument

There was no pull/copy step or retry path.

---

## 2. New behavior (implemented)

For each container/app:

1. Convert pod to `AppHostingConfig` and thread through:
   - `ImagePullPolicy`
   - `PackageDest` (from annotation)
   - `ImagePullSecrets` (from pod spec)
2. POST app-hosting config (unchanged)
3. Call `installWithRecovery()`:
   - Attempt install using original `ImagePath`
   - If install fails and policy allows recovery:
     - Choose a destination on device:
       - annotation value if provided
       - else `flash:/virtual-kubelet/<app>.tar`
     - If `ImagePath` is HTTP(S), perform a device-side fetch using the IOS-XE RESTCONF `copy` RPC:
       - `copy <http(s)://...> <flash:/...>`
     - Retry install from destination file

If the image path is not HTTP(S), recovery currently falls back to a best-effort retry using the original `ImagePath` (no SCP/SFTP upload is implemented yet).

---

## 3. Secret plumbing (Provider → Driver)

### Provider passes namespace-scoped secret lister

**File:** `internal/provider/provider.go`

- `CreatePod()` now passes a namespace-scoped lister:

```go
p.secretLister.Secrets(pod.Namespace)
```

See: `internal/provider/provider.go:73`

---

### Driver interface updated

**File:** `internal/drivers/factory.go`

- `CiscoKubernetesDeviceDriver` now expects:

```go
DeployPod(ctx context.Context, pod *v1.Pod, secretLister corev1listers.SecretNamespaceLister) error
```

See: `internal/drivers/factory.go:47`

---

### Fake driver updated

**File:** `internal/drivers/fake/driver.go`

- Updated DeployPod signature to match the interface.

See: `internal/drivers/fake/driver.go:66`

---

## 4. IOS-XE driver: store SecretNamespaceLister

### XEDriver struct extended

**File:** `internal/drivers/iosxe/driver.go`

- New field:

```go
secretLister corev1listers.SecretNamespaceLister
```

See: `internal/drivers/iosxe/driver.go:43-51`

### DeployPod stores the lister

**File:** `internal/drivers/iosxe/pod_lifecycle.go`

- Stores for later use during install recovery:

```go
d.secretLister = secretLister
```

See: `internal/drivers/iosxe/pod_lifecycle.go:37`

---

## 5. Pod → AppHostingConfig threading

**File:** `internal/drivers/iosxe/transformers.go`

### AppHostingConfig now contains

- `ImagePullPolicy` from container
- `PackageDest` from pod annotation
- `ImagePullSecrets` from pod spec

See: `internal/drivers/iosxe/transformers.go:61-70`

### PackageDest parsing + validation

- Helper:

```go
getIOSXEAppHostPackageDest(pod *v1.Pod) (string, error)
```

See: `internal/drivers/iosxe/transformers.go:255-283`

- Reads annotation:
  - `virtual-kubelet.cisco.com/iosxe-apphost-package-dest`
- Value semantics:
  - If unset or empty: `PackageDest` will be empty in `AppHostingConfig` and install recovery will use the default destination.
  - If set: it is treated as the **on-device destination path** for the fetched OCI tar, and will be used as the install `package` path during recovery.
- Example values:
  - `flash:/virtual-kubelet/myapp.tar`
  - `bootflash:/virtual-kubelet/images/myapp.tar`
- Validation:
  - Must be a conservative IOS-XE filesystem path starting with one of:
    - `bootflash:/`, `harddisk:/`, `flash:/`, `nvram:/`, `usb:/`

### Thread secrets + policy + dest into each config

See: `internal/drivers/iosxe/transformers.go:241-249`

---

## 6. Annotation constant

**File:** `internal/drivers/iosxe/app_hosting.go`

- Constant:

```go
podAnnotationIOSXEAppHostPackageDest = "virtual-kubelet.cisco.com/iosxe-apphost-package-dest"
```

See: `internal/drivers/iosxe/app_hosting.go:181-183`

---

## 7. Install recovery + device-side copy

**File:** `internal/drivers/iosxe/app_hosting.go`

### CreateAppHostingApp uses recovery wrapper

- Calls `installWithRecovery()` instead of directly calling `InstallApp()`.

See: `internal/drivers/iosxe/app_hosting.go:42-46`

### installWithRecovery()

See: `internal/drivers/iosxe/app_hosting.go:117-176`

The recovery logic is:

1. Attempt install using `appConfig.ImagePath`
2. If failure and policy allows:
   - Determine destination:
     - If pod annotation `virtual-kubelet.cisco.com/iosxe-apphost-package-dest` is set, use that value
     - Otherwise default to `flash:/virtual-kubelet/<app>.tar` (where `<app>` is the generated IOS-XE app ID, i.e. `appConfig.AppName`)
     - This defaulting is implemented in `installWithRecovery()` at `internal/drivers/iosxe/app_hosting.go:149-155`
   - If `ImagePath` is HTTP(S):
     - Apply imagePullSecrets (basic auth-in-URL best effort)
     - Call `copyRPC(sourceURL, dest)`
     - Retry install using `package = dest`

### copyRPC()

- Implements IOS-XE RESTCONF `copy` RPC:

- POST path:
  - `/restconf/operations/Cisco-IOS-XE-rpc:copy`

- Payload:

```json
{
  "Cisco-IOS-XE-rpc:copy": {
    "source-drop-node-name": "http(s)://...",
    "destination-drop-node-name": "flash:/..."
  }
}
```

See: `internal/drivers/iosxe/app_hosting.go:74-98`

YANG reference:

- `tests/yang/Cisco-IOS-XE-rpc.yang:771-791`

---

## 8. imagePullSecrets parsing (token then basic)

**File:** `internal/drivers/iosxe/app_hosting.go`

### Control flow

- In recovery (for HTTP URLs), source URL is optionally rewritten:
  - see `maybeAddAuthToURL()`

### maybeAddAuthToURL()

- Attempts to embed basic auth into the URL if:
  - basic credentials exist
  - URL doesn’t already include user info

See: `internal/drivers/iosxe/app_hosting.go:195-230`

### resolveAuthFromPullSecrets()

- Iterates `ImagePullSecrets`, retrieves secrets from the namespace-scoped `SecretNamespaceLister`.

See: `internal/drivers/iosxe/app_hosting.go:234-259`

### authFromSecret()

Parses:

1. `secret.Data["token"]` → returns token auth
2. `.dockerconfigjson`:
   - prefers `identitytoken` or `registrytoken` (token)
   - else uses `username/password`
   - else decodes `auth` field (base64 `username:password`)

See: `internal/drivers/iosxe/app_hosting.go:264-321`

**Note:** Token auth is extracted but not applied to the copy RPC yet; basic auth is applied via URL embedding as a best-effort mechanism.

---

## 9. Unit tests

### Transformers tests

**File:** `internal/drivers/iosxe/transformers_test.go`

- Added assertion that `PackageDest` is empty when annotation is not set.
- Added:
  - `TestConvertPodToAppConfigs_PackageDestAnnotation`

---

### New tests for auth + recovery

**File:** `internal/drivers/iosxe/app_hosting_test.go`

Includes:

- `authFromSecret` tests:
  - token
  - dockerconfigjson username/password
  - dockerconfigjson auth field
  - dockerconfigjson identity token preference

- `installWithRecovery` tests using a fake NetworkClient:
  - install fails → copy succeeds → retry install succeeds
  - install fails → copy fails → retry install fails

---

## 10. Known limitations / future improvements

1. **Non-HTTP sources** (local tar path on VK):
   - No SCP/SFTP upload path implemented yet.
2. **Token auth**:
   - Parsed but not applied (no header support in current copy RPC usage).
3. **Policy constants**:
   - Recovery checks policy values as strings; can be refined to use typed constants.
4. **Registry host matching**:
   - Secret selection does not match URL host; it returns the first usable auth.
