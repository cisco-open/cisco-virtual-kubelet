# Catalyst Access Point app hosting

The `AP` driver is an **Alpha candidate for the 1 December 2026 integration**.
It connects directly to the AP's native IOx CAF HTTPS API to install, activate,
start, inspect, stop, deactivate, and uninstall prebuilt application packages.
The branch name `wlc` does not mean that CVK manages a wireless LAN controller.
Existing November images do not include this driver.

## Prepare the AP

Enable IOx/app hosting using the supported procedure for your AP and controller
software. Verify the exact AP model, CPU architecture, firmware, available IOx
resources, and CAF reachability before deploying. The
[Cisco prerequisites](https://developer.cisco.com/docs/app-hosting-ap/device-prerequisites/)
list model-specific requirements. CVK does not configure the WLC, enable IOx,
provision AP certificates, or build IOx packages.

The worker needs access to the AP's HTTPS API (default TCP `8443`) and to the
package server. The AP needs whatever network access the packaged application
requires. Network policies for dynamic device workers must permit those paths;
the chart's manager policy does not select these workers.

Use a certificate trusted by the worker and matching the AP address. For a
private CA, use `spec.tls.caSecretRef` as described in the
[TLS guide](security-tls.md); a local worker can instead use `tls.caFile`.
AP traffic always uses HTTPS. Disabling the shared `tls.enabled` flag does not
select plaintext. `insecureSkipVerify` is only suitable for an isolated lab.

## Enroll and schedule

Build the controller from the assessed candidate and install its matching
CiscoDevice CRD; the schema must allow `spec.driver: AP`. Create a credential
Secret in the device namespace containing the `password` key, then configure:

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: ap-01
  namespace: default
spec:
  driver: AP
  address: ap-01.example.net
  username: cvk-operator
  credentialSecretRef:
    name: ap-01-credentials
  tls:
    enabled: true
  maxPods: 2
  taints:
    - key: cisco.io/ap
      value: "true"
      effect: NoSchedule
```

The initial and refreshed Node advertise at most **two Pods**, with one regular
container per Pod. Set `maxPods: 1` to reserve an application slot. Values above
two, including the shared CRD default of 16, are capped at two. CPU, memory and
storage quotas are not invented from model assumptions. Account for applications
installed outside CVK and for the resources requested by each package; two
slots do not guarantee that two arbitrary packages fit.

This candidate is for standalone/compatibility workers. Managed topology,
protected physical identity, network-management operations and software campaigns
have not been qualified for APs. The node platform label is `platform: cisco-ap`.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: ap-hello
  namespace: default
spec:
  nodeSelector:
    kubernetes.io/hostname: ap-01
    platform: cisco-ap
  tolerations:
    - key: cisco.io/ap
      operator: Equal
      value: "true"
      effect: NoSchedule
  automountServiceAccountToken: false
  enableServiceLinks: false
  dnsPolicy: Default
  restartPolicy: Always
  containers:
    - name: hello
      image: https://artifacts.example.net/ap/hello-v1.tar
      imagePullPolicy: IfNotPresent
```

The explicit token, service-link, and DNS settings prevent Kubernetes admission
defaults from requesting behavior IOx cannot implement. Do not add resource
requests or limits, volumes, environment variables or `envFrom`, probes,
commands, security contexts, init containers, sidecars, or an alternative runtime
class. Admission policies that inject these settings make the Pod ineligible.
IOx packages define their own runtime identity, resources, files and environment.
Use IOx network mappings to access the app; this driver does not assign a CNI Pod
IP or provide Kubernetes Service networking, exec, or application log streaming.

## Package and activation contract

`image` must be an HTTP(S) URL ending in `.tar` or `.tar.gz` serving a prebuilt
IOx archive with a `package.yaml` descriptor. OCI references, authenticated
artifact servers and redirects are not supported. Use trusted, immutable,
versioned HTTPS URLs: this driver does not verify a package signature or a
user-specified content digest, and AP trust does not configure artifact-server
trust. The final upload is limited to 20,000,000 bytes.

Cisco also describes an approximately 80 MB budget for the package plus the
expanded root filesystem. CVK's archive checks do not prove that the app fits
that on-device budget or has the correct architecture. See the
[Cisco hosting FAQ](https://developer.cisco.com/docs/app-hosting-ap/faqs/).

The default activation connects package interface `eth0` to `iox-nat0` using
`1to1` port mapping. To replace the activation body, set
`cisco.io/ap-activation-payload` to a JSON object with a nonempty `resources`
object. Network names and resource values must match the AP and package.
The annotation is trusted deployment configuration with the authority of the
worker's device credentials; restrict who may schedule to AP nodes.

Create a new Pod (new UID) to change a package or activation configuration.
In-place image replacement and activation changes are not supported. CAF does
not retain the original package URL or Kubernetes metadata, so these changes
cannot always be detected after a restart; use immutable workload templates.

## Recovery, observation and qualification

CVK-generated app IDs encode Pod UID and container index. The worker resolves
inventory against Kubernetes by UID after a restart; it does not adopt an app
by matching a Pod name. Reconciliation retries incomplete lifecycle operations
and restarts stopped applications under the existing mutation guard. A CAF
`RUNNING` state means that the container started, not that its service is healthy;
application readiness must be verified separately. API errors remain errors,
not empty inventory. Do not rename CVK app IDs on the AP.

The optional exporter under `examples/ap-metrics` collects device metrics by
SSH from inside an ARM64 IOx application. It is independent of the driver's
readiness check and consumes one of the two application slots. Its credentials,
SSH host key, CPU architecture, license bundle, network exposure and output
formats need validation for the target AP. The driver does not provision SSH
secrets into the application; use authenticated IOx configuration provisioning
for credential-free distributable packages. Do not put real credentials in
packages served by a shared artifact server. See its repository README for build
instructions.

The [December AP assessment](releases/2026-12-ap-readiness.md) records code changes,
offline checks and the remaining live tests. Passing mock tests does not qualify
all AP models or establish a published release.
