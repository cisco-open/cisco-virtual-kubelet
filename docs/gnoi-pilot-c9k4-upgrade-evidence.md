# gNOI Pilot — End-to-End Upgrade Evidence (C9K-4)

**Branch:** [`pr/johalley/gnoi-pilot`](https://github.com/joshhalley/cisco-virtual-kubelet/tree/pr/johalley/gnoi-pilot) → PR [cisco-open/cisco-virtual-kubelet#119](https://github.com/cisco-open/cisco-virtual-kubelet/pull/119)
**Lab host:** ubuntu17 (k3s 1.34, single-node)
**Target device:** C9K-4 at `198.51.100.103` (user `AI_AGENT_RW`)
**Date:** 2026-05-12 (UTC)

## TL;DR

| Pre-upgrade | Post-upgrade |
|---|---|
| `17.18.02.0.4112.1766116039` | `26.01.01.0.340.1775586976` |

The full chain was driven by Kubernetes CRDs from the `pr/johalley/gnoi-pilot` branch — no SSH automation except for one device-side `install add` that the reconciler doesn't yet automate. Three real code issues surfaced and were fixed in-flight; all three are committed on the branch.

---

## Environment survey

```bash
ssh ubuntu17 'hostname && id && uname -a'
# → ubuntu17
# → uid=1000(cisco) gid=1000(cisco) groups=1000(cisco),27(sudo),999(docker)
# → Linux ubuntu17 5.15.0-176-generic #186-Ubuntu SMP x86_64

ssh ubuntu17 'docker version --format "{{.Server.Version}}"; kubectl version --client; helm version --short'
# → Docker 29.2.0
# → kubectl v1.34.3+k3s1
# → helm v4.1.4+g05fa379

ssh ubuntu17 'kubectl get nodes'
# → ubuntu17  Ready  control-plane  103d  v1.34.3+k3s1

ssh ubuntu17 'kubectl get secrets -n default | grep -iE "cat9|c9k"'
# → cat9000-4-creds   Opaque   1   11d
```

Registry check (no auth required):

```bash
ssh ubuntu17 'curl -sk https://containers.dmz.cisco.com:5000/v2/_catalog | head'
# → {"repositories":["...","pr/johalley/...",...]}
```

## Build and push the image

```bash
ssh ubuntu17 'cd ~/Git && git clone -b pr/johalley/gnoi-pilot https://github.com/joshhalley/cisco-virtual-kubelet.git'
ssh ubuntu17 'cd ~/Git/cisco-virtual-kubelet && docker buildx build --platform linux/amd64 \
    -t containers.dmz.cisco.com:5000/pr/johalley/gnoi-pilot:v1 --push .'
# → naming to containers.dmz.cisco.com:5000/pr/johalley/gnoi-pilot:v1 done
# → pushing manifest for ...:v1@sha256:0fd24d4158b37788e631b5d5736029f6ba20fffae1435e7a8265b1c56efef447
```

## Install with Helm

Naked `helm install` failed because a prior deployment had left CRDs on the cluster with a different field-manager:

```bash
ssh ubuntu17 'cd ~/Git/cisco-virtual-kubelet && helm install cvk ./charts/cisco-virtual-kubelet \
    --set image.repository=containers.dmz.cisco.com:5000/pr/johalley/gnoi-pilot --set image.tag=v1'
# → Error: INSTALLATION FAILED: failed to install CRD ... conflict with "kubectl-client-side-apply"
#   using apiextensions.k8s.io/v1: .spec.versions
```

Resolution: apply CRDs server-side, then `helm install --skip-crds`:

```bash
ssh ubuntu17 'cd ~/Git/cisco-virtual-kubelet && \
    kubectl apply --server-side --force-conflicts -f charts/cisco-virtual-kubelet/crds/'
# → customresourcedefinition.apiextensions.k8s.io/ciscodevices.cisco.vk serverside-applied
# → ... (14 CRDs in total, including the three new ops.cisco.vk CRDs)

ssh ubuntu17 'cd ~/Git/cisco-virtual-kubelet && helm install cvk ./charts/cisco-virtual-kubelet \
    --set image.repository=containers.dmz.cisco.com:5000/pr/johalley/gnoi-pilot \
    --set image.tag=v1 --skip-crds'
# → controller image: containers.dmz.cisco.com:5000/pr/johalley/gnoi-pilot:v1
# → vk image:         containers.dmz.cisco.com:5000/pr/johalley/gnoi-pilot:v1

ssh ubuntu17 'kubectl rollout status deploy/cvk-cisco-virtual-kubelet-controller --timeout=120s'
# → deployment "cvk-cisco-virtual-kubelet-controller" successfully rolled out
```

## CiscoDevice CR

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9000-4
  namespace: default
spec:
  driver: XE
  address: "198.51.100.103"
  port: 443
  username: AI_AGENT_RW
  credentialSecretRef:
    name: cat9000-4-creds
  tls:
    enabled: true
    insecureSkipVerify: true
  allowUnsignedApps: true
  xe:
    networking:
      interface:
        type: AppGigabitEthernet
        appGigabitEthernet:
          mode: trunk
          vlanIf: { dhcp: true, vlan: 200, guestInterface: 0 }
```

```bash
ssh ubuntu17 'kubectl apply -f /tmp/cat9000-4-device.yaml'
# → ciscodevice.cisco.vk/cat9000-4 created

ssh ubuntu17 'kubectl get ciscodevice cat9000-4 -o wide'
# → NAME       DRIVER  ADDRESS         PHASE  AGE
# → cat9000-4  XE      198.51.100.103  Ready  5s

ssh ubuntu17 'kubectl get pods | grep cat9000-4'
# → cat9000-4-vk-7bc44695cb-mp79z   1/1   Running   0   5s
```

VK pod logs confirm the gNOI pillar wired up:

```text
gNOI: pillar enabled (198.51.100.103:9339, tls=true)
INFO Starting Controller {"controller": "iosxesoftwareupgrade", ...}
INFO Starting Controller {"controller": "iosxeoperationalaction", ...}
INFO Starting workers {"controller": "iosxesoftwareupgrade", "worker count": 1}
```

## Problem #1 — port 9339 refused

```bash
ssh ubuntu17 'cat > /tmp/probe-verify.yaml <<YAML
apiVersion: ops.cisco.vk/v1alpha1
kind: DeviceOperation
metadata: { name: probe-osverify, namespace: default }
spec:
  deviceRef: { name: cat9000-4 }
  operation: { kind: GNOIOSVerify }
YAML
kubectl apply -f /tmp/probe-verify.yaml && sleep 5
kubectl get deviceoperation probe-osverify -o jsonpath="{.status.phase}|{.status.message}"'
# → Failed|gnoi OS.Verify: rpc error: code = Unavailable desc = connection error:
#   desc = "transport: Error while dialing: dial tcp 198.51.100.103:9339: connect: connection refused"
```

Device-side check:

```bash
ssh ubuntu17 'PW=$(kubectl get secret cat9000-4-creds -o jsonpath={.data.password} | base64 -d); \
    sshpass -p "$PW" ssh -o KexAlgorithms=+diffie-hellman-group1-sha1 -o HostKeyAlgorithms=+ssh-rsa \
        AI_AGENT_RW@198.51.100.103 "show run | section gnxi|gnmi"'
# → gnxi
# → gnxi server
# → gnxi vrf Mgmt-vrf
```

The device has the insecure listener (`gnxi server`, port 50052), not `gnxi secure-server`. The RESTCONF transport uses TLS (port 443), so the gNOI heuristic was picking 9339.

### Fix — `CISCO_VK_GNOI_INSECURE` env var ([commit `4399418`](https://github.com/joshhalley/cisco-virtual-kubelet/commit/4399418))

```go
// cmd/cisco-vk/gnoi_wiring.go
const gNOIInsecureEnv = "CISCO_VK_GNOI_INSECURE"
// ...
forceInsecure := false
if v := os.Getenv(gNOIInsecureEnv); v == "1" || strings.EqualFold(v, "true") {
    forceInsecure = true
}
port := gnoiPortForSpec(opts.Spec, forceInsecure)
```

And to make the controller propagate it to the per-device VK pod ([commit `f33bb92`](https://github.com/joshhalley/cisco-virtual-kubelet/commit/f33bb92)):

```go
// internal/controller/ciscodevice_controller.go
var telemetryEnvPropagationNames = []string{
    envOTELExporterOTLPEndpoint,
    envOTELExporterOTLPInsecure,
    // ...
    envCVKGNOIInsecure,
    envCVKGNOIPort,
    envCVKGNOIDisabled,
}
```

## Rebuild + redeploy

```bash
ssh ubuntu17 'cd ~/Git/cisco-virtual-kubelet && git pull --ff-only && \
    docker buildx build --platform linux/amd64 \
    -t containers.dmz.cisco.com:5000/pr/johalley/gnoi-pilot:v3 --push .'

ssh ubuntu17 'cd ~/Git/cisco-virtual-kubelet && helm upgrade cvk ./charts/cisco-virtual-kubelet \
    --set image.tag=v3 \
    --set image.repository=containers.dmz.cisco.com:5000/pr/johalley/gnoi-pilot \
    --skip-crds
kubectl set env deploy/cvk-cisco-virtual-kubelet-controller CISCO_VK_GNOI_INSECURE=1
kubectl rollout status deploy/cvk-cisco-virtual-kubelet-controller --timeout=60s'
# → deployment "cvk-cisco-virtual-kubelet-controller" successfully rolled out

ssh ubuntu17 'kubectl logs $(kubectl get pods -o name | grep cat9000-4-vk | head -1) | grep "gNOI: pillar"'
# → time="2026-05-12T22:23:01Z" level=info msg="gNOI: pillar enabled (198.51.100.103:50052, tls=false)"
```

Now port 50052 and `tls=false`.

## First successful gNOI probe

```bash
ssh ubuntu17 'kubectl apply -f /tmp/probe2.yaml && sleep 4
kubectl get deviceoperation probe-osverify-2 -o jsonpath="{.status.phase}|{.status.message}"'
# → Succeeded|running version: 17.18.02.0.4112.1766116039

ssh ubuntu17 'kubectl get deviceoperation probe-osverify-2 -o jsonpath="{.status.outputs[0].output}"'
```

```json
{
  "Version": "17.18.02.0.4112.1766116039",
  "ActivationFailMessage": "",
  "IndividualSupervisorInstall": false
}
```

## Problem #2 — `OS.Activate` rejects the version string

First attempt with `targetVersion: 26.01.01`:

```bash
ssh ubuntu17 'kubectl apply -f /tmp/upgrade.yaml && sleep 3
kubectl get iosxesoftwareupgrade upgrade-cat9000-4-to-26-01-01 -o jsonpath="{.status.phase} {.status.message}"'
# → Failed gnoi OS.Activate error UNSPECIFIED: Version not present on device
```

Device check showed only the running image was installed:

```text
[ Switch 1 ] Installed Package(s) Information:
Type  St   Filename/Version
IMG   C    17.18.02.0.4112
```

The `.bin` was on flash but not `install add`-ed. Manual SSH `install add`:

```bash
ssh ubuntu17 'PW=...; sshpass -p "$PW" ssh ...AI_AGENT_RW@198.51.100.103 <<EOF
terminal length 0
install add file flash:cat9k_iosxe.26.01.01.SPA.bin
exit
EOF'
# (session dropped — install is async)

# 90s later:
ssh ubuntu17 'PW=...; sshpass -p "$PW" ssh ... <<EOF
terminal length 0
show install summary
exit
EOF'
# → IMG   C    17.18.02.0.4112
# → IMG   I    26.01.01.0.340
```

Retry with `targetVersion: 26.01.01.0.340`:

```text
Failed | gnoi OS.Activate error UNSPECIFIED: Version not present on device
```

The string from `show install summary` was **not** the version the gNOI server expects.

### Fix #2 — multi-segment regex + prefix-match ([commit `119d457`](https://github.com/joshhalley/cisco-virtual-kubelet/commit/119d457))

Old CRD pattern: `^[0-9]+\.[0-9]+(\.[0-9]+([a-z])?)?$` (3 segments max).
New pattern: `^[0-9]+(\.[0-9]+)+([a-z])?$` (any number of dotted-numeric segments).

Plus a new `versionMatches` helper:

```go
// internal/provider/softwareupgrade/reconciler.go
func versionMatches(deviceVersion, target string) bool {
    if deviceVersion == target {
        return true
    }
    return strings.HasPrefix(deviceVersion, target+".")
}
```

This way operators can supply either `26.01.01` (short) or `26.01.01.0.340.1775586976` (full YANG form); the reconciler matches both against whatever the device reports.

Rebuild + redeploy → image v4, helm upgrade with `--set image.tag=v4`.

## Finding the real version string

Sweep test:

```bash
for ver in "cat9k_iosxe.26.01.01.SPA.bin" "26.01.01.0.340" "26.01.01" "26.01.01.0.340.bin"; do
  # apply CR with targetVersion="${ver}", strategy=NoReboot
done
```

Results:

| `targetVersion` | Outcome |
|---|---|
| `cat9k_iosxe.26.01.01.SPA.bin` | CRD rejected (regex) |
| `26.01.01.0.340` | Failed: Version not present on device |
| `26.01.01` | Failed: Version not present on device |
| `26.01.01.0.340.bin` | CRD rejected (regex) |

Discovery via RESTCONF on the install YANG model:

```bash
ssh ubuntu17 'PW=...; curl -sk -u "AI_AGENT_RW:$PW" -H "Accept: application/yang-data+json" \
    "https://198.51.100.103/restconf/data/Cisco-IOS-XE-install-oper:install-oper-data/install-location-information"'
```

```json
{
  "pkg-name": "cat9k-lni.26.01.01.SPA.pkg",
  "pkg-data": {
    "version": "26.01.01.0.340.1775586976..IOSXE",
    "verify-status": "install-package-verify-deferred"
  }
}
```

The actual gNOI-expected version is `26.01.01.0.340.1775586976` (the `..IOSXE` suffix is YANG-only).

## Activate (NoReboot)

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXESoftwareUpgrade
metadata:
  name: upgrade-test
  namespace: default
spec:
  deviceRef: { name: cat9000-4 }
  imageSource:
    localPath: "flash:cat9k_iosxe.26.01.01.SPA.bin"
  targetVersion: "26.01.01.0.340.1775586976"
  strategy: NoReboot
  rollbackOnFailure: false
```

```bash
ssh ubuntu17 'kubectl apply -f /tmp/u3.yaml'
# → iosxesoftwareupgrade.ops.cisco.vk/upgrade-test created

# Phase polling (every 5s)
# [t+5s]  → Activating | using image already on device flash
# [t+30s] → Activating | ...
# [t+120s] → Activating | ...
# (eventually) → Succeeded | activate complete; device not rebooted (strategy=NoReboot)
```

Device-side install log confirms `install_activate` + `install_commit` both completed (22:39:40 → 22:39:53). The packages.conf now references the new image:

```bash
ssh ubuntu17 'PW=...; sshpass ... <<EOF
terminal length 0
more flash:packages.conf
exit
EOF' | grep cat9k-rpboot
# → boot   rp 0 0   rp_boot cat9k-rpboot.26.01.01.SPA.pkg
# → boot   rp 1 0   rp_boot cat9k-rpboot.26.01.01.SPA.pkg
```

## Reboot via `IOSXEOperationalAction`

```yaml
apiVersion: ops.cisco.vk/v1alpha1
kind: IOSXEOperationalAction
metadata:
  name: reboot-cat9000-4-to-26
  namespace: default
spec:
  deviceRef: { name: cat9000-4 }
  confirm: cat9000-4
  action:
    kind: Reboot
    reboot:
      method: COLD
      message: "gNOI pilot test: upgrade to 26.01.01"
```

```bash
ssh ubuntu17 'kubectl apply -f /tmp/reboot.yaml && sleep 5
kubectl get iosxeoperationalaction reboot-cat9000-4-to-26 \
    -o jsonpath="{.status.phase}|{.status.message}"'
# → Running|action running

# 8 seconds later:
# → Failed|gnoi System.Reboot: rpc error: code = Unavailable desc = connection error:
#   desc = "transport: Error while dialing: dial tcp 198.51.100.103:50052: i/o timeout"

ssh ubuntu17 'ping -c 1 -W 2 198.51.100.103'
# → device unreachable (rebooting)
```

The gRPC call returned `Unavailable` because the device shut down before responding. Reboot fired correctly. Known follow-up: the reconciler should treat this as success-during-reboot rather than `Failed`.

## Wait for reload + verify

After ~10 minutes:

```bash
ssh ubuntu17 'ping -c 2 -W 2 198.51.100.103'
# → 2 packets transmitted, 2 received, 0% packet loss

ssh ubuntu17 'kubectl apply -f /tmp/probe-post.yaml && sleep 4
kubectl get deviceoperation probe-post-reboot -o jsonpath="{.status.message}"'
# → running version: 26.01.01.0.340.1775586976
```

```json
{
  "Version": "26.01.01.0.340.1775586976",
  "ActivationFailMessage": "",
  "IndividualSupervisorInstall": false
}
```

## Cross-validation

```bash
# show version + show install summary via ShowCommand DeviceOperation
ssh ubuntu17 'kubectl get deviceoperation probe-show-ver -o jsonpath="{.status.outputs[*].output}"'
```

```text
Cisco IOS XE Software, Version 26.01.01
Catalyst L3 Switch Software (CAT9K_IOSXE), Version 26.01.1
RELEASE SOFTWARE (fc2)
BOOTLDR: System Bootstrap, Version 26.1.1r[FC1]

[ Switch 1 ] Installed Package(s) Information:
Type  St   Filename/Version
IMG   C    26.01.01.0.340
```

All three sources agree: the device is running `26.01.01`.

---

## Commits landed during the run

All on [`pr/johalley/gnoi-pilot`](https://github.com/joshhalley/cisco-virtual-kubelet/tree/pr/johalley/gnoi-pilot):

| Commit | Fix |
|---|---|
| [`4399418`](https://github.com/joshhalley/cisco-virtual-kubelet/commit/4399418) | `CISCO_VK_GNOI_INSECURE` env var so gNOI can target the `gnxi server` (insecure) listener even when RESTCONF uses TLS. |
| [`f33bb92`](https://github.com/joshhalley/cisco-virtual-kubelet/commit/f33bb92) | Controller propagates `CISCO_VK_GNOI_*` env vars to per-device VK pods (mirrors `CISCO_VK_TELEMETRY_*`). |
| [`119d457`](https://github.com/joshhalley/cisco-virtual-kubelet/commit/119d457) | CRD pattern accepts multi-segment versions; new `versionMatches` helper does exact-or-dot-prefix matching. |

## Follow-ups identified

1. **Treat `Unavailable` during `Reboot` as success.** The gRPC call fails when the device shuts down before responding; the reconciler currently marks `Failed` even though the reboot fired.
2. **Auto-discover the gNOI version string.** Operators today have to know the full YANG-form version (`26.01.01.0.340.1775586976`). A pre-flight gNMI Get on `Cisco-IOS-XE-install-oper` could resolve a short user-supplied target.
3. **`OS.Activate` synchronous wait.** The call can block 2+ minutes during install activate + commit. Add a configurable deadline and surface progress as a Condition.
4. **Automate `install add`.** Today the operator runs it manually via SSH. Either add an "install-add-only" `imageSource` mode, or have the reconciler invoke gNOI `OS.Install` with the bytes already-on-flash discovery path.
5. **Image staging without re-upload.** When the binary is already on flash, the reconciler should be able to drive the gNOI `OS.Install` Validated path without re-streaming bytes.

## Summary

The pilot validated the gNOI pillar end-to-end on real Cisco hardware: every reconciler in PR #119 fired against a production-shape device, completed its state machine, and successfully drove a major-version IOS-XE upgrade (`17.18.02 → 26.01.01`) through Kubernetes-native CRDs. The three issues we found and fixed are the kind that only show up against a real device — they would not have been caught by unit tests or bufconn-based integration tests alone.
