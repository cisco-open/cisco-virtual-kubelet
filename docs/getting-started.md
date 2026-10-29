# Getting Started

This guide walks you through deploying Cisco Virtual Kubelet against a Kubernetes cluster and running your first pod on a Cisco IOS-XE device.

You'll install the Helm chart, which deploys a Kubernetes controller. The controller watches `CiscoDevice` custom resources and stands up a Virtual Kubelet pod for each one — this is the supported way to run Cisco Virtual Kubelet.

!!! info "NX-OS (Beta)"
    This walkthrough uses an IOS-XE device, the production-focused path. Cisco
    Nexus (NX-OS) switches are also supported in Beta — the install and pod
    workflow is the same, with platform-specific device settings, credentials,
    node name and package path. See the substitutions in
    [Deploying to NX-OS](#deploying-to-nx-os-beta) below and the
    [NX-OS Family Reference](reference/families-nxos.md).

## Prerequisites

| Requirement | Notes |
|---|---|
| Kubernetes cluster | v1.28+ recommended |
| Helm 3.21+ or 4.2+ | For installing the chart |
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

<a id="1-use-the-published-image-or-build-a-custom-image"></a>
<a id="2-install-the-controller"></a>

## 1. Install the controller

The controller watches `CiscoDevice` custom resources and creates a Virtual Kubelet deployment for each one.

The release chart needs no source checkout, image build, or private registry.
These commands target November `v2026.11.0`; run them only after that version
and its artifacts appear on the
[Releases page](https://github.com/cisco-open/cisco-virtual-kubelet/releases).
If it is not listed, use the published October `2026.10.0` chart with its
matching documentation. The chart selects its matching signed image automatically.

```bash
helm install cvk oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.11.0 \
  --namespace cvk-system --create-namespace
```

This pulls the signed `ghcr.io/cisco-open/cisco-virtual-kubelet` image by default. The chart `--version` matches the release's SemVer-compatible CalVer without the leading `v` (for example, `v2026.11.0` → `2026.11.0`); see [Releases](https://github.com/cisco-open/cisco-virtual-kubelet/releases) for the current one.

The chart installs:

- All shipped `cisco.vk`, `config.cisco.vk`, and `ops.cisco.vk` CRDs
- The controller `Deployment` (single replica by default; leader election is
  opt-in)
- RBAC for the manager and per-device VK pods

Both the controller pod and each VK pod it spawns use this image. To use different images for the two, set `controllerImage.*` and `vkImage.*` instead.

The `sdwan` and `nexus-dashboard` adapters and managed public-CA Secret
projection are introduced in `v2026.11.0`. October installations need the
[full upgrade procedure](installation-maintenance.md) before using them;
applying a November example alone does not update the CRDs or running image.
For local changes, see [custom builds](#optional-build-a-custom-image).

Verify:

```bash
kubectl -n cvk-system get pods
kubectl get crd ciscodevices.cisco.vk
```

If the controller pod is not `Running`, see [Troubleshooting → CiscoDevice stuck in Provisioning](troubleshooting.md#ciscodevice-stuck-in-provisioning).

<a id="3-create-a-credential-secret"></a>

## 2. Create a credential Secret

Device credentials are injected into the VK pod via a Kubernetes Secret — the controller never stores them in the ConfigMap. The Secret must be in the same namespace as the `CiscoDevice` and have a key named exactly `password`.

Save this as `secret.yaml`, replacing the password placeholder:

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

Need multiple devices with different credentials? See [Security → Managing credentials across multiple devices](security-credentials.md#managing-credentials-across-multiple-devices).

<a id="4-create-a-ciscodevice"></a>

## 3. Create a CiscoDevice

The `CiscoDevice` CR describes how to reach the device and how containers should be networked on it.

Save this as `ciscodevice.yaml`, adapting the address, username and networking
to your device:

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
    name: cat9000-1-creds      # matches the Secret from step 2
  tls:
    enabled: true
    insecureSkipVerify: false
  # allowUnsignedApps: true     # uncomment for unsigned packages (own builds /
                                 # test images). CVK disables device signing via
                                 # RESTCONF and relaxes the reconciler guard.
  xe:
    networking:
      interface:
        type: AppGigabitEthernet
        appGigabitEthernet:
          mode: trunk
          vlanIf:
            dhcp: true
            vlan: 200
            guestInterface: 0
```

This Catalyst 9000 example uses the app-hosting VLAN and DHCP setup from the
[platform guide](configuration-cat9000.md). For Catalyst 8000V, use its
[VirtualPortGroup configuration](configuration-cat8000v.md) instead.
The device HTTPS certificate must be trusted by the worker's system CA store
and match `spec.address` (an IP SAN when using an IP address). For a private CA,
see the managed Secret alternative below; October deployments need a custom
verified CA mount as described in [Shared TLS](CONFIGURATION.md#shared-tls).
This quick-start configuration leaves gNOI in backward-compatible `auto` mode.
Before invoking a gNOI operation, configure
[explicit verified gNOI TLS](gnoi-software-lifecycle.md#secure-ios-xe-gnxi).

??? info "Private CA support introduced in v2026.11.0"
    `spec.tls.caSecretRef` requires the November CRD, admission policies, manager
    and workers. Upgrade those components together when moving from
    `v2026.10.0`; see the [projection and rollback
    requirements](CONFIGURATION.md#shared-tls).

    Create a Secret from an independently trusted public CA bundle in the same
    namespace as the `CiscoDevice`:

    ```bash
    kubectl -n default create secret generic switch-https-public-ca \
      --from-file=ca.crt=/path/to/trusted-device-ca.crt
    ```

    Replace the device's `tls` block with:

    ```yaml
    tls:
      enabled: true
      insecureSkipVerify: false
      caSecretRef:
        name: switch-https-public-ca
    ```

    The controller projects only validated public CA certificates. This does
    not install a certificate on the device or configure dedicated gNOI trust.

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

For a Cisco Nexus (NX-OS) switch, use the same controller installation in step 1.
In step 2, name the credential Secret `nexus9300v-01-creds` in namespace
`default` and supply that switch's password. Set `driver: NXOS` and use an
`nxos:` block instead of `xe:`. App-hosting is driven over NX-API CLI; the only
supported host-side attachment today is the `Management` interface.

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
    insecureSkipVerify: false  # requires a certificate trusted by the worker
  nxos:
    networking:
      interface:
        type: Management
        management:
          guestInterface: 0
```

In the Pod manifest from step 4, replace `spec.nodeName` with `nexus9300v-01`
and `spec.containers[0].image` with the path to an NX-OS-compatible package, for
example `bootflash:/hello-app.nxos.tar`. Stage that package on the switch first.
The IOS-XE example's `flash:` prefix is not accepted: NX-OS accepts `bootflash:`
paths or supported HTTP(S) package URLs. Keep the
pod's namespace consistent with your credential and device examples (`default`).

Declarative NX-OS device configuration (VLANs, interfaces, features, and so on)
is managed separately through the `NXOSConfig` CRD over NX-API REST/DME — see
the [NX-OS Family Reference](reference/families-nxos.md) for the supported
families and an example. NX-OS is a Beta runtime slice; the
[Production Readiness](production-readiness.md) page tracks its current scope.

<a id="5-deploy-a-pod"></a>

## 4. Deploy a pod

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

The image reference is a path on the device's flash storage, not a container registry reference — the container tar must already be on the device, or be served over HTTP(S). To serve images from a registry, see [Image Distribution](image-distribution.md).

```bash
kubectl apply -f pod.yaml
kubectl get pod hello-app -w
```

If the pod stays in `Pending` for more than a minute or ends up `Failed`, see [Troubleshooting → Pod stuck in Pending](troubleshooting.md#pod-stuck-in-pending) or [PackagePolicyInvalid false positives](troubleshooting.md#packagepolicyinvalid-false-positives).

<a id="6-verify-on-the-device"></a>

## 5. Verify on the device

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

## Upgrading an existing release

Helm installs CRDs during the first install but does not upgrade them. Before
upgrading to `2026.11.0`, confirm publication as described above, pull that
chart, and back up the live definitions and custom resources. November refreshes
18 existing CRD manifests, with substantive schema changes in four and no new
kinds. Preserve reviewed Helm values, inspect the server-side diff, and apply the reviewed schemas with an
explicit Helm field-ownership handoff:

```bash
helm pull oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.11.0 --untar
kubectl get customresourcedefinitions.apiextensions.k8s.io -o yaml \
  > cvk-crds-before-upgrade.yaml
kubectl diff --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade -f cisco-virtual-kubelet/crds/
kubectl apply --server-side --force-conflicts \
  --field-manager=cvk-crd-upgrade -f cisco-virtual-kubelet/crds/
kubectl wait --for=condition=Established --timeout=60s \
  -f cisco-virtual-kubelet/crds/

helm upgrade cvk oci://ghcr.io/cisco-open/charts/cisco-virtual-kubelet \
  --version 2026.11.0 \
  --namespace cvk-system
kubectl rollout status deployment/cvk-cisco-virtual-kubelet-controller \
  --namespace cvk-system
```

`kubectl diff` returns status 1 for expected differences. The force flag is
limited to the exact CVK CRD files passed with `-f`; review the diff and keep
the backup before continuing.

The September release adds `NetworkController` and
`NetworkControllerConfig`. If either CRD is absent, the manager keeps existing
device reconcilers running but disables the Alpha controller scaffold for that
process. Apply both CRDs and restart the manager. See
[Upgrading CRDs](installation-maintenance.md#upgrade-the-crds-and-chart) for verification and rollback
guidance.

## Optional: install the kubectl plugin with Krew

The client-side `kubectl-ciscovk` plugin is optional; the controller and normal
Kubernetes workflows do not depend on it. It provides read-only, ad-hoc IOS-XE
commands and manager-accepted topology graph diagnostics. Graph output is
observational and never grants rollout authority; optional declared mappings
and links live in an admission-protected ConfigMap key separate from rollout
policy hashes. It is available in the
public Krew index; install and upgrade it with:

```bash
kubectl krew update
kubectl krew install cisco-vk
kubectl cisco-vk version

# Later releases
kubectl krew upgrade cisco-vk
```

Historical note: `v2026.08.0` predates the plugin assets, and `v2026.8.1` was
the first plugin-bearing release. The
[CLI & Plugin Reference](cisco-vk-cli.md) documents Krew, signed-archive, and
source-build paths plus the plugin's current IOS-XE-only scope.

## Optional: build a custom image

Use a custom build only when you need code changes or are evaluating a
development feature. For a released baseline, select an explicit tag from the
[published Releases page](https://github.com/cisco-open/cisco-virtual-kubelet/releases).
Do not infer publication from the greatest Git tag: draft and rejected
candidates can also have tags.

```bash
git clone https://github.com/cisco-open/cisco-virtual-kubelet.git
cd cisco-virtual-kubelet
VERSION=v2026.11.0  # use only after publication; otherwise select a published baseline
git checkout "$VERSION"
IMAGE_REPOSITORY=ghcr.io/your-org/cisco-vk  # replace with your registry path

docker build -t "$IMAGE_REPOSITORY:$VERSION" .
docker push "$IMAGE_REPOSITORY:$VERSION"

helm install cvk ./charts/cisco-virtual-kubelet \
  --namespace cvk-system --create-namespace \
  --set image.repository="$IMAGE_REPOSITORY" \
  --set image.tag="$VERSION"
```

For a development build, check out the intended commit and use a unique image
tag for it; install the chart and CRDs from that same checkout. This is an
evaluation deployment with its own provenance, not a release artifact. For KIND
or minikube, you can load the built image locally instead of pushing it:

```bash
kind load docker-image "$IMAGE_REPOSITORY:$VERSION" --name kind
# Or:
minikube image load "$IMAGE_REPOSITORY:$VERSION"
```

## What's next

- [Configuration](CONFIGURATION.md) — Every field with defaults and validation rules
- [Observability](observability.md) — Enable OpenTelemetry topology traces and scrape Prometheus metrics
- [Security](security.md) — TLS, credential rotation, and RBAC
- [Troubleshooting](troubleshooting.md) — If anything above did not work
