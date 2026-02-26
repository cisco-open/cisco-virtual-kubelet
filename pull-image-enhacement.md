# Pull image enhancement plan

## Problem statement

During IOS-XE AppHosting install, the install/config operation can return success even when the image/package is not actually pulled/activated, resulting in pods that never come up and no retry being triggered.

## Goals

- Avoid "false positive" app installs: only report success when device-side operational state indicates the app is actually installed/activated/running (or at least image pull completed).
- Trigger retry (by returning an error) when the image pull/install does not complete within a bounded timeout.
- Default timeout: **180s**.
- Allow per-pod override via annotation: `virtual-kubelet.cisco.com/iosxe-apphost-package-timeout`.

## Non-goals

- Changing the fundamental AppHosting packaging mechanism.
- Introducing new external dependencies.

## Working hypothesis / expected state machine

When successful, AppHosting operational state should transition roughly:

`INSTALLING -> DEPLOYED -> ACTIVATED -> RUNNING`

We should gate success on reaching a terminal-good state (ideally `RUNNING`).

## Implementation plan (high level)

### Code pointers (current behavior)

- App config + install RPC success is treated as install success:
  - `CreateAppHostingApp()` posts config then calls `installWithRecovery()` and logs success.
  - `InstallApp()` logs "Successfully installed" immediately after the RESTCONF RPC returns OK.
  - See: `internal/drivers/iosxe/app_hosting.go:31-54` and `internal/drivers/iosxe/app_hosting.go:104-115`.

- Operational data is fetched later; missing oper data triggers warning but does not retroactively fail install:
  - `GetPodStatus()` calls `GetAppOperationalData()` and warns when an app is configured but has no oper entry.
  - See: `internal/drivers/iosxe/pod_lifecycle.go:201-241` (warning at `pod_lifecycle.go:229`).

- Oper endpoint currently used for status/state polling:
  - `WaitForAppStatus()` polls `GET /restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data` and checks `app.Details.State`.
  - See: `internal/drivers/iosxe/app_hosting.go:428-471`.

- Current container-state mapping already assumes oper states:
  - `transformers.go` maps `Details.State` values:
    - `RUNNING` => container ready
    - `DEPLOYED`/`ACTIVATED` => `ContainerCreating`
    - missing oper => `No operational data available`
  - See: `internal/drivers/iosxe/transformers.go:652-703`.


1. **Locate current install flow**

   - Find where AppHosting app is created and installed.
   - Identify what constitutes "success" today (likely only RESTCONF response checks).

2. **Locate current operational checks**

   - Find where we query operational data and emit warnings like:
     - "configured but no operational data found"
     - "No operational data"
   - Identify the RESTCONF/YANG paths and returned fields.

3. **Define readiness predicate**

   - Preferred: operational state == `RUNNING`.
   - Acceptable alternatives (if RUNNING is unreliable/late): `ACTIVATED` + container exists.
   - Define failure predicates if exposed by YANG (e.g., `FAILED`/`ERROR`).

4. **Add post-install verification wait**

   - After install/config returns success, poll operational state until readiness predicate true.
   - Use exponential backoff with a cap, bounded by timeout.
   - If timeout expires or explicit failure state observed, return a retryable error so VK will retry.

5. **Add annotation-configurable timeout**

   - Parse `virtual-kubelet.cisco.com/iosxe-apphost-package-timeout`.
   - Accept Go durations (e.g. `180s`, `3m`) and optionally integer seconds.
   - Apply default 180s; clamp to sane min/max.

6. **Tests**

   - Add unit tests with mocked RESTCONF client:
     - success path: transitions to RUNNING within timeout.
     - timeout path: operational data never appears.
     - failure state path (if modeled).
     - annotation parsing edge cases.

7. **Validation**

   - Run `go test ./...`.
   - (Optional lab) verify that failed pulls now cause retries and events show meaningful errors.

## Progress log

- 2026-02-24: Plan created.
- 2026-02-24: Fixed markdownlint spacing around headings/lists.
- 2026-02-24: Documented current code pointers for install-vs-oper mismatch.
- 2026-02-24: Confirmed oper `Details.State` is already interpreted for RUNNING/DEPLOYED/ACTIVATED.
- 2026-02-24: **Implementation complete.** Changes applied to 3 files:

## Changes applied

### `internal/drivers/iosxe/transformers.go`

- Added `time` and `strconv` imports.
- Added `PackageTimeout time.Duration` field to `AppHostingConfig` struct.
- Added `getIOSXEAppHostPackageTimeout()` parser function:
  - Reads `virtual-kubelet.cisco.com/iosxe-apphost-package-timeout` annotation.
  - Accepts Go duration strings (`180s`, `3m`) and bare integer seconds (`180`).
  - Default: 180s. Clamped to [10s, 30m].
- Wired `packageTimeout` into `ConvertPodToAppConfigs()` alongside existing `packageDest`.

### `internal/drivers/iosxe/app_hosting.go`

- Added annotation constant `podAnnotationIOSXEAppHostPackageTimeout`.
- In `CreateAppHostingApp()`: after `installWithRecovery()` succeeds, added
  `WaitForAppStatus(ctx, appName, "RUNNING", timeout)` call.
  If oper state never reaches RUNNING within timeout, returns error (triggers VK retry).

### `internal/drivers/iosxe/app_hosting_test.go`

- Extended `fakeNetworkClient` with `getHook` for oper data simulation.
- Added `makeOperData()` helper for building test oper payloads.
- Added test: `TestCreateAppHostingApp_WaitsForRunning` (success path with state progression).
- Added test: `TestCreateAppHostingApp_TimeoutWhenNeverRunning` (no oper data -> timeout error).
- Added test: `TestCreateAppHostingApp_TimeoutWhenStuckAtActivated` (stuck at ACTIVATED -> timeout error).
- Added table-driven test: `TestGetIOSXEAppHostPackageTimeout` (11 subtests covering defaults,
  durations, bare ints, invalid values, clamping, whitespace).
- Added test: `TestGetIOSXEAppHostPackageTimeout_NilPod`.

### Test results

All tests pass: `go test ./...` and `go test -v -run "TestCreateAppHostingApp_|TestGetIOSXEAppHostPackageTimeout"`.

## Post-lab-test fixes

### 2026-02-26: Improved `WaitForAppStatus` logging (observability fix)

**Problem:** Lab testing showed the wait mechanism IS working (`Waiting for app ... to reach RUNNING state (timeout: 3m0s)` appeared in logs), but there was no visible output from the polling loop showing what the device was returning during each poll cycle. This made it look like no checks were being performed.

**Root cause:** Two logging gaps:

1. State check logging used `Debugf` (invisible at default log level).
2. No log message at all when the app was not present in the oper data map (the exact failure case for a failed image pull).

**Fix applied to `internal/drivers/iosxe/app_hosting.go` `WaitForAppStatus()`:**

- Promoted state check logging from `Debugf` to `Infof`: `"App %s current state: %s (waiting for: %s)"`.
- Added `Warnf` when app not found in oper data: `"App %s not yet present in operational data (will retry until timeout)"`.
- Added `Warnf` when app found but has no state details: `"App %s found in oper data but has no state details yet"`.

**Expected log output after fix (failed pull scenario):**

```text
Waiting for app <name> to reach RUNNING state (timeout: 3m0s)
Waiting for app <name> to reach status: RUNNING
App <name> not yet present in operational data (will retry until timeout)
App <name> not yet present in operational data (will retry until timeout)
...
timeout waiting for app <name> to reach status RUNNING after 3m0s
app <name> did not reach RUNNING state after install: timeout ...
```

**Expected log output after fix (successful pull scenario):**

```text
Waiting for app <name> to reach RUNNING state (timeout: 3m0s)
Waiting for app <name> to reach status: RUNNING
App <name> not yet present in operational data (will retry until timeout)
App <name> current state: DEPLOYED (waiting for: RUNNING)
App <name> current state: ACTIVATED (waiting for: RUNNING)
App <name> current state: RUNNING (waiting for: RUNNING)
App <name> reached expected status: RUNNING
Successfully created and installed app <name>
```

All unit tests pass after this change.

### 2026-02-26: Fixed UpdatePod retry path (recovery fix)

**Problem:** Lab testing confirmed the post-install timeout fires correctly, but the pod never retries the install. After the timeout error, the VK framework requeues the pod but calls `UpdatePod` instead of `CreatePod`. `UpdatePod` was a no-op, so the failed app config sat stale on the device forever.

**Root cause:** The VK framework's `createOrUpdatePod` logic (in `node/pod.go`) calls `GetPod` first. Since the app config was already posted to the device before the install timeout, `GetPodContainers` finds the config, `GetPodStatus` returns a non-nil pod. The framework sees "pod exists + spec unchanged" and routes to `UpdatePod` instead of `CreatePod`. With `UpdatePod` as a no-op, the retry never actually re-attempts the install.

**Fix applied to `internal/drivers/iosxe/pod_lifecycle.go` `UpdatePod()`:**

Replaced the no-op `UpdatePod` with recovery logic that:

1. **Discovers containers** on the device via `GetPodContainers()`.
2. **Fetches operational data** via `GetAppOperationalData()`.
3. **Checks each app's state** — if not `RUNNING`, marks for redeploy.
4. **If all apps healthy**, returns nil (no action needed).
5. **Cleans up stale apps** via `DeleteApp()` (full lifecycle: stop → deactivate → uninstall → delete config), with fallback to config-only delete if the full lifecycle fails.
6. **Re-converts pod** to fresh app configs via `ConvertPodToAppConfigs()`.
7. **Re-deploys** only the unhealthy apps via `CreateAppHostingApp()` (which includes the post-install wait for RUNNING state).

**Expected behavior after fix (failed pull → retry):**

```text
# Initial install attempt
Waiting for app <name> to reach RUNNING state (timeout: 3m0s)
App <name> not yet present in operational data (will retry until timeout)
...
timeout waiting for app <name> to reach status RUNNING after 3m0s
app <name> did not reach RUNNING state after install: timeout ...
# VK framework requeues → calls UpdatePod
UpdatePod request received for pod default/test-pod
App <appID> for container test-app has no operational data, needs redeploy
Cleaning up stale app <appID> for container test-app before redeploy
Re-deploying app <appID> for container test-app
# Second attempt with fresh install
Waiting for app <name> to reach RUNNING state (timeout: 3m0s)
...
```

**Tests added to `internal/drivers/iosxe/app_hosting_test.go`:**

- `TestUpdatePod_NoActionWhenRunning` — All apps RUNNING → no redeploy triggered.
- `TestUpdatePod_RedeploysWhenNoOperData` — App has no oper data → cleanup + redeploy.
- `TestUpdatePod_RedeploysWhenStuckState` — App stuck at ACTIVATED → cleanup + redeploy.

Tests use a phase-aware state machine in `getHook` that simulates the full delete lifecycle (stopping→ACTIVATED, deactivating→DEPLOYED, uninstalling→empty) and redeploy (→RUNNING), keeping test execution fast (~0.7s total).

All unit tests pass: `go test ./...` completes successfully.

### 2026-02-26: Fixed package-dest annotation regex (usability fix)

**Problem:** The `virtual-kubelet.cisco.com/iosxe-apphost-package-dest` annotation validation required a slash after the filesystem prefix (e.g., `flash:/path/file.tar`), but IOS-XE also accepts paths without a slash for files in the root (e.g., `flash:file.tar`). This caused validation errors for valid IOS-XE paths.

**Error seen:**

```text
invalid virtual-kubelet.cisco.com/iosxe-apphost-package-dest annotation "flash:nginx_latest.tar":
expected e.g. flash:/virtual-kubelet/app.tar
```

**Fix applied to `internal/drivers/iosxe/transformers.go` `getIOSXEAppHostPackageDest()`:**

Changed the validation regex from:
```go
valid := regexp.MustCompile(`^(bootflash:|harddisk:|flash:|nvram:|usb:)/.+`)
```

To:
```go
valid := regexp.MustCompile(`^(bootflash:|harddisk:|flash:|nvram:|usb:)/?(.+)`)
```

Now accepts both forms:
- `flash:app.tar` (file in root)
- `flash:/path/app.tar` (file in subdirectory)

**Tests added to `internal/drivers/iosxe/transformers_test.go`:**

Extended `TestConvertPodToAppConfigs_PackageDestAnnotation` with table-driven tests:
- ✅ `flash:/virtual-kubelet/custom.tar` (path with slash)
- ✅ `flash:nginx_latest.tar` (path without slash)
- ✅ `bootflash:app.tar` (bootflash without slash)
- ✅ `bootflash:/apps/myapp.tar` (bootflash with slash)
- ❌ `flash:` (invalid: no filename)
- ❌ `http://example.com/app.tar` (invalid: wrong scheme)

All unit tests pass: `go test ./...` completes successfully.

## Investigation: UpdatePod retry behavior (2026-02-26)

**User observation:** After implementing the UpdatePod retry logic, lab testing showed the first install timeout correctly triggered a retry, but the second attempt also timed out with `ProviderUpdateFailed` event.

**Investigation findings:**

The UpdatePod retry mechanism IS working correctly:

1. **Evidence from logs:**
   - Event reason changed from `ProviderCreateFailed` (first attempt) to `ProviderUpdateFailed` (retry attempt)
   - This confirms `UpdatePod` was called and attempted redeploy
   - Error message says "failed to **redeploy** app" (not "failed to deploy")

2. **Expected behavior:**
   - If the underlying issue (image pull failure due to unreachable URL or network issue) persists, subsequent retry attempts will also timeout
   - The pod will continue retrying until:
     - The image becomes reachable (network/URL fixed)
     - User fixes the pod spec (image URL)
     - User deletes the pod

3. **What's working:**
   - ✅ First install timeout detection
   - ✅ VK framework requeue
   - ✅ `UpdatePod` detects stale app (no oper data)
   - ✅ `UpdatePod` cleans up stale config via `DeleteApp`
   - ✅ `UpdatePod` re-deploys via `CreateAppHostingApp`
   - ✅ Second install attempt with fresh install
   - ✅ Proper event tracking (`ProviderUpdateFailed` vs `ProviderCreateFailed`)

4. **What needs user action:**
   - Fix the underlying issue (image URL, network connectivity, device reachability)
   - The retry mechanism will then succeed on a subsequent attempt

**Conclusion:** The retry mechanism is working as designed. Multiple timeouts indicate a persistent image pull issue that requires fixing the image source or network configuration, not a code issue.

### 2026-02-26: Implemented device-side copy recovery on timeout (critical fix)

**Problem:** The existing `installWithRecovery` mechanism with `copyRPC` fallback was only triggered when the install RPC **immediately failed**. However, the install RPC typically **succeeds** even when the device can't reach the image URL—the device just never actually pulls the image. This meant the copy fallback never triggered, and pods would timeout forever.

**Root cause:** The install flow was:
1. `InstallApp` calls install RPC with HTTP URL → RPC succeeds (returns no error)
2. `installWithRecovery` returns early at line 134 (no error to recover from)
3. `WaitForAppStatus` polls for RUNNING state
4. Wait times out because image was never pulled
5. **Copy fallback never triggered** because there was no install RPC error

**Fix applied to `internal/drivers/iosxe/app_hosting.go` `CreateAppHostingApp()`:**

Moved the copy recovery logic from `installWithRecovery` (triggered on install RPC failure) to the post-install timeout path:

```go
// First attempt: install using the image path as provided
if err := d.InstallApp(ctx, appConfig.AppName, appConfig.ImagePath); err != nil {
    return fmt.Errorf("failed to install app %s: %w", appConfig.AppName, err)
}

// Wait for RUNNING state
waitErr := d.WaitForAppStatus(ctx, appConfig.AppName, "RUNNING", timeout)
if waitErr == nil {
    // Success!
    return nil
}

// Wait timed out - attempt recovery via device-side copy
if imagePullPolicy != "Never" && isHTTPURL(appConfig.ImagePath) {
    // Clean up failed install
    d.DeleteApp(ctx, appConfig.AppName)

    // Re-post config (DeleteApp removes it)
    d.client.Post(ctx, configPath, appConfig.Apps, d.marshaller)

    // Use device-side copy to pull image
    dest := appConfig.PackageDest  // from annotation
    if dest == "" {
        dest = fmt.Sprintf("flash:/virtual-kubelet/%s.tar", appConfig.AppName)
    }
    d.copyRPC(ctx, appConfig.ImagePath, dest)

    // Retry install with local path
    d.InstallApp(ctx, appConfig.AppName, dest)

    // Wait again for RUNNING
    d.WaitForAppStatus(ctx, appConfig.AppName, "RUNNING", timeout)
}
```

**Recovery flow:**
1. Initial install with HTTP URL
2. Wait for RUNNING (timeout: 3m default)
3. **If timeout**: trigger copy recovery
   - Delete stale app config via `DeleteApp()` (stop → deactivate → uninstall → delete config)
   - Re-post app config
   - Call `copyRPC(httpURL, flash:/path/app.tar)` to download image to device flash
   - Retry install with local flash path
   - Wait again for RUNNING (second 3m timeout)
4. If second timeout or copy fails: return error (triggers UpdatePod retry)

**Expected log output (successful recovery):**

```text
# First install attempt
Installing app cvk0000_... from package: http://example.com/nginx.tar
Successfully installed app cvk0000_...
Waiting for app cvk0000_... to reach RUNNING state (timeout: 3m0s)
App cvk0000_... not yet present in operational data (will retry until timeout)
...
timeout waiting for app cvk0000_... to reach status RUNNING after 3m0s
App cvk0000_... did not reach RUNNING state after install: timeout...

# Copy recovery triggered
Attempting image recovery for app cvk0000_... (policy=IfNotPresent)
Install recovery destination for app cvk0000_...: flash:/virtual-kubelet/cvk0000_....tar
Cleaning up failed install for app cvk0000_... before recovery
Re-posting config for app cvk0000_... before recovery
# Device-side copy RPC called
Installing app cvk0000_... from package: flash:/virtual-kubelet/cvk0000_....tar
Waiting for app cvk0000_... to reach RUNNING state after recovery (timeout: 3m0s)
App cvk0000_... current state: DEPLOYED (waiting for: RUNNING)
App cvk0000_... current state: ACTIVATED (waiting for: RUNNING)
App cvk0000_... current state: RUNNING (waiting for: RUNNING)
Successfully recovered and installed app cvk0000_...
```

**Tests added to `internal/drivers/iosxe/app_hosting_test.go`:**

- `TestCreateAppHostingApp_CopyRecoveryAfterTimeout` — Verifies copy RPC is called after timeout and second install succeeds

**Code cleanup:**
- Removed obsolete `installWithRecovery()` function (replaced with inline recovery in `CreateAppHostingApp`)
- Removed obsolete tests `TestInstallWithRecovery_CopySuccessThenInstallDest` and `TestInstallWithRecovery_CopyFailsThenRetryOriginalFails`

All unit tests pass.

### 2026-02-26: Fixed copy RPC timeout and added file existence check (complete production fix)

**Problems identified from lab testing:**

1. **Copy RPC timeout**: Device WAS successfully copying images to flash during recovery, but the RESTCONF copy RPC was timing out before the transfer completed, causing recovery to fail and retry with HTTP URL instead of flash path
2. **Immediate re-deployment**: Recovery flow wasn't properly sequenced - pod was being re-deployed before copy completed
3. **Redundant copies**: No check if image already exists on flash before initiating copy

**Root causes:**

1. **HTTP client timeout**: The HTTP client timeout was set to 30 seconds at `driver.go:94`, but IOS-XE copy RPC is synchronous and can take several minutes for large images
2. **Recovery flow**: The recovery mechanism correctly detected timeout and initiated copy, but the flow was properly sequenced
3. **No file check**: Every recovery attempt copied the image even if it already existed

**Fixes applied:**

#### 1. Increased HTTP client timeout (`internal/drivers/iosxe/driver.go`)

Changed from 30 seconds to 10 minutes:

```go
// Before:
Timeout := 30 * time.Second

// After:
// Increased timeout to 10 minutes to support long-running copy operations for large container images
Timeout := 10 * time.Minute
```

This affects all RESTCONF operations but is necessary for copy operations to complete.

#### 2. Added file existence check (`internal/drivers/iosxe/app_hosting.go`)

Added `fileExists()` function that uses IOS-XE exec RPC to run `dir <filepath>` command:

```go
// Check if file already exists on device before initiating copy
fileExists, err := d.fileExists(ctx, dest)
if err != nil {
    log.G(ctx).Warnf("Failed to check if file exists at %s: %v (will attempt copy anyway)", dest, err)
    fileExists = false
}

if fileExists {
    log.G(ctx).Infof("Image file already exists on device at %s, skipping copy", dest)
} else {
    // Attempt device-side copy
    if err := d.copyRPC(ctx, src, dest); err != nil {
        // ... handle error
    }
}
```

Benefits:
- Avoids unnecessary multi-minute copy operations on retry
- Reduces network bandwidth usage
- Faster recovery when image is already on device

#### 3. Recovery flow was already correct

The recovery flow in `CreateAppHostingApp` already properly sequences operations:
1. First install attempt times out
2. Cleanup via `DeleteApp`
3. Re-post config
4. Check if file exists (NEW)
5. Copy if needed (waits for completion with 10-minute timeout)
6. Retry install with flash path
7. Wait for RUNNING state

The issue was only the timeout - the flow was correct.

**Expected behavior after fixes:**

First deployment attempt (image not reachable):
```text
Installing app cvk0000_... from package: http://192.0.2.33/docker/nginx.tar
Waiting for app cvk0000_... to reach RUNNING state (timeout: 3m0s)
App cvk0000_... not yet present in operational data (will retry until timeout)
...
App cvk0000_... did not reach RUNNING state after install: timeout

Attempting image recovery for app cvk0000_... (policy=Always)
Install recovery destination for app cvk0000_...: flash:nginx_latest.tar
Cleaning up failed install for app cvk0000_... before recovery
Re-posting config for app cvk0000_... before recovery
Starting copy operation (may take several minutes for large images): http://192.0.2.33/docker/nginx.tar -> flash:nginx_latest.tar
Copy operation completed successfully: http://192.0.2.33/docker/nginx.tar -> flash:nginx_latest.tar
Image recovery copy succeeded for app cvk0000_... (dest=flash:nginx_latest.tar), retrying install
Installing app cvk0000_... from package: flash:nginx_latest.tar
Waiting for app cvk0000_... to reach RUNNING state after recovery (timeout: 3m0s)
App cvk0000_... current state: DEPLOYED (waiting for: RUNNING)
App cvk0000_... current state: ACTIVATED (waiting for: RUNNING)
App cvk0000_... current state: RUNNING (waiting for: RUNNING)
Successfully recovered and installed app cvk0000_...
```

Subsequent retry (image already on flash):
```text
Attempting image recovery for app cvk0000_... (policy=Always)
Install recovery destination for app cvk0000_...: flash:nginx_latest.tar
Cleaning up failed install for app cvk0000_... before recovery
Re-posting config for app cvk0000_... before recovery
Image file already exists on device at flash:nginx_latest.tar, skipping copy
Installing app cvk0000_... from package: flash:nginx_latest.tar
...
Successfully recovered and installed app cvk0000_...
```

**Files modified:**

1. `internal/drivers/iosxe/driver.go` - Increased HTTP client timeout from 30s to 10m
2. `internal/drivers/iosxe/app_hosting.go`:
   - Added `fileExists()` function using IOS-XE exec RPC
   - Integrated file existence check before copy in recovery flow
   - Updated copyRPC comments to reference 10-minute timeout

All unit tests pass.
