# Getting Started

This guide walks you through deploying Cisco Virtual Kubelet against a Kubernetes cluster and running your first pod on a Cisco IOS-XE device.

You'll install the Helm chart, which deploys a Kubernetes controller. The controller watches `CiscoDevice` custom resources and stands up a Virtual Kubelet pod for each one — this is the supported way to run Cisco Virtual Kubelet.

!!! info "NX-OS (Beta)"
    This walkthrough uses an IOS-XE device, the production-focused path. Cisco
    Nexus (NX-OS) switches are also supported in Beta — the install and pod
    steps are identical; only the `CiscoDevice` spec differs (`driver: NXOS`
    with an `nxos:` block). See [Deploying to NX-OS](#deploying-to-nx-os-beta)
    below and the [NX-OS Family Reference](reference/families-nxos.md).

## Prerequisites

| Requirement | Notes |
|---|---|
| Kubernetes cluster | v1.28+ recommended |
| Helm v3 | For installing the chart |
| Docker (or compatible) | Optional; only needed to build a custom VK image |
| Container registry | Optional for custom images; the published image is on GHCR |
| Cisco IOS-XE device | Catalyst 8000V (17.15.4c+), Catalyst 9000 (17.18.2+), IR1100 Series (17.12.4+), or IE3500 Series (17.18.2+) |
| `kubectl` | Configured to talk to your cluster |

On the device you need:

- `iox` configured — Cisco's on-device container runtime.
- `restconf` enabled on the HTTPS listener.
- App-hosting support (standard on Catalyst 8000V and 9000).
- A signed container tar on device flash (e.g. `flash:/hello-app.iosxe.tar` on Cat8000V/Cat9000/IE3500, or `bootflash:/hello-app.iosxe.tar` on IR1100) unless you explicitly allow unsigned packages.

See the platform-specific install guide for the exact IOS-XE CLI snippets: [Catalyst 8000V](configuration-cat8000v.md), [Catalyst 9000](configuration-cat9000.md), [IR1100 Series](configuration-ir1101.md), or [IE3500 Series](configuration-ie3500.md).

## 1. Use the published image or build a custom image

Monthly releases publish a signed multi-architecture image at
`ghcr.io/cisco-open/cisco-virtual-kubelet`. The published Helm chart selects
the matching image tag automatically, so skip to step 2 unless you need a
custom build.

Official releases are cut monthly. For a stable base, check out the [latest release](https://github.com/cisco-open/cisco-virtual-kubelet/releases/latest) tag rather than `main` — `main` can contain unreleased in-flight changes.

To build and publish a custom image:

```bash
git clone https://github.com/cisco-open/cisco-virtual-kubelet.git
cd cisco-virtual-kubelet

# Check out the latest release (recommended)
LATEST=$(git ls-remote --tags --refs --sort=-v:refname origin 'v*' | head -1 | awk '{print $2}' | sed 's|refs/tags/||')
git checkout "$LATEST"

# Build
docker build -t <your-registry>/cisco-vk:"$LATEST" .

# Push
docker push <your-registry>/cisco-vk:"$LATEST"
```

If your cluster is KIND or minikube you can side-load instead of pushing:

```bash
# KIND
kind load docker-image <your-registry>/cisco-vk:"$LATEST" --name <cluster>

# minikube
minikube image load <your-registry>/cisco-vk:"$LATEST"
```

## 2. Install the controller

The controller watches `CiscoDevice` custom resources and creates a Virtual Kubelet deployment for each one.

**Published chart (recommended)** — the chart and image are released to GHCR,
so step 1's custom build is optional and you can install directly:

```bash
helm install cvk oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.7.1 \
  --namespace cvk-system --create-namespace
```

This pulls the signed `ghcr.io/cisco-open/cisco-virtual-kubelet` image by default. The chart `--version` is the release normalised to SemVer (e.g. `v2026.07.1` → `2026.7.1`); see [Releases](https://github.com/cisco-open/cisco-virtual-kubelet/releases) for the current one.

**From source with a custom image** — to run the build from step 1 instead:

```bash
helm install cvk ./charts/cisco-virtual-kubelet \
  --namespace cvk-system --create-namespace \
  --set image.repository=<your-registry>/cisco-vk \
  --set image.tag="$LATEST"
```

The chart installs:

- The `CiscoDevice` CRD
- The controller `Deployment` (single replica; uses leader election for HA)
- RBAC: controller service account with permissions on `ciscodevices`, ConfigMaps, Deployments, and Nodes; VK service account with pod/node/events permissions

Both the controller pod and each VK pod it spawns use this image. To use different images for the two, set `controllerImage.*` and `vkImage.*` instead.

Verify:

```bash
kubectl -n cvk-system get pods
kubectl get crd ciscodevices.cisco.vk
```

If the controller pod is not `Running`, see [Troubleshooting → CiscoDevice stuck in Provisioning](troubleshooting.md#ciscodevice-stuck-in-provisioning).

## 3. Create a credential Secret

Device credentials are injected into the VK pod via a Kubernetes Secret — the controller never stores them in the ConfigMap. The Secret must be in the same namespace as the `CiscoDevice` and have a key named exactly `password`.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cat9000-1-creds
  namespace: default
type: Opaque
stringData:
  password: <device-password>
```

```bash
kubectl apply -f secret.yaml
```

Need multiple devices with different credentials? See [Security → Managing credentials across multiple devices](security.md#managing-credentials-across-multiple-devices).

## 4. Create a CiscoDevice

The `CiscoDevice` CR describes how to reach the device and how containers should be networked on it.

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9000-1
  namespace: default
spec:
  driver: XE
  address: "192.168.1.100"
  port: 443
  username: admin
  credentialSecretRef:
    name: cat9000-1-creds      # matches the Secret from step 3
  tls:
    enabled: true
    insecureSkipVerify: true    # acceptable for lab; use caFile in production
  # allowUnsignedApps: true     # uncomment for unsigned packages (own builds /
                                 # test images). CVK disables device signing via
                                 # RESTCONF and relaxes the reconciler guard.
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: true
          interface: "0"
          guestInterface: 0
```

```bash
kubectl apply -f ciscodevice.yaml
```

Within a few seconds you should see:

```bash
$ kubectl get ciscodevice
NAME         DRIVER   ADDRESS         PHASE      AGE
cat9000-1    XE       192.168.1.100   Ready      15s

$ kubectl get nodes
NAME         STATUS   ROLES   AGE
cat9000-1    Ready    agent   15s
```

If `PHASE` stays on `Provisioning` or shows `Error`, see [Troubleshooting](troubleshooting.md#ciscodevice-stuck-in-provisioning).

### Deploying to NX-OS (Beta)

For a Cisco Nexus (NX-OS) switch, the controller install (step 2), credential
Secret (step 3), and pod scheduling (step 5) are unchanged. Only the
`CiscoDevice` spec differs: set `driver: NXOS` and use an `nxos:` block instead
of `xe:`. App-hosting is driven over NX-API CLI; the only supported host-side
attachment today is the `Management` interface.

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: nexus9300v-01
  namespace: default
spec:
  driver: NXOS
  address: "192.168.1.120"
  port: 443
  username: admin
  credentialSecretRef:
    name: nexus9300v-01-creds
  tls:
    enabled: true
    insecureSkipVerify: true    # acceptable for lab; use caFile in production
  nxos:
    networking:
      interface:
        type: Management
        management:
          guestInterface: 0
```

Declarative NX-OS device configuration (VLANs, interfaces, features, and so on)
is managed separately through the `NXOSConfig` CRD over NX-API REST/DME — see
the [NX-OS Family Reference](reference/families-nxos.md) for the supported
families and an example. NX-OS is a Beta runtime slice; the
[Production Readiness](production-readiness.md) page tracks its current scope.

## 5. Deploy a pod

Schedule a pod directly onto the virtual node.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: hello-app
spec:
  nodeName: cat9000-1
  tolerations:
  - key: virtual-kubelet.io/provider
    operator: Exists
  containers:
  - name: hello
    image: flash:/hello-app.iosxe.tar   # path on device flash (use bootflash: on IR1100)
    resources:
      requests:
        memory: "512Mi"
        cpu: "500m"
      limits:
        memory: "1Gi"
        cpu: "1000m"
```

The image reference is a path on the device's flash storage, not a container registry — the container tar must already be on the device.

```bash
kubectl apply -f pod.yaml
kubectl get pod hello-app -w
```

If the pod stays in `Pending` for more than a minute or ends up `Failed`, see [Troubleshooting → Pod stuck in Pending](troubleshooting.md#pod-stuck-in-pending) or [PackagePolicyInvalid false positives](troubleshooting.md#packagepolicyinvalid-false-positives).

## 6. Verify on the device

```bash
ssh admin@192.168.1.100
# show app-hosting list
# show app-hosting detail appid cvk00000_<pod-uid>
```

You should see the container transition through `DEPLOYED → ACTIVATED → RUNNING`. Typical output of `show app-hosting list`:

```
App id                                    State
----------------------------------------------------
cvk00000_a1b2c3d4...                      RUNNING
```

## What's next

- [Configuration](CONFIGURATION.md) — Every field with defaults and validation rules
- [Observability](observability.md) — Enable OpenTelemetry topology traces and scrape Prometheus metrics
- [Security](security.md) — TLS, credential rotation, and RBAC
- [Troubleshooting](troubleshooting.md) — If anything above did not work
