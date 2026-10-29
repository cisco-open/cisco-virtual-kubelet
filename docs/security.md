# Security

Secure a CVK installation by deciding who can read credentials, which remote
identities each connection trusts, and which worker may perform an operation.
The three guides below separate those decisions while retaining the detailed
contracts needed for review.

## Choose your task

| You are… | Start here | Expected result |
|---|---|---|
| Installing a device | [Credentials and rotation](security-credentials.md), then [TLS](security-tls.md) | The worker connects with a referenced Secret and verified server identity |
| Operating a network fleet | [Credential rotation](security-credentials.md#rotating-a-single-password) or [gNOI trust](security-tls.md#secure-ios-xe-gnoi) | The intended workers restart or reconnect, with obsolete signer material removed |
| Reviewing or delegating access | [Authorization and worker identities](security-authorization.md) | Bindings and admission rules match the approved runtime and namespace boundaries |

For the first installation, follow [Getting Started](getting-started.md).
Keep the chart, CRDs and runtime image matched. These pages target `v2026.11.0`,
which introduces managed public-CA Secret projection and the Alpha controller
adapters. When upgrading from October `v2026.10.0`, follow their prerequisites;
a newer documentation example does not upgrade an installed release.

## Decisions to make before deployment

Use a same-namespace `credentialSecretRef` with the exact key `password` for
each device. Secret storage needs Kubernetes encryption at rest and appropriate
RBAC; base64 encoding is not encryption. Application environment Secrets also
become device run options visible to device administrators.

Verify device HTTPS, gNOI and kubelet serving certificates separately. A valid
certificate for one connection does not establish trust for another. Explicit
gNOI TLS and signer retirement have their own procedure; read that procedure
before a certificate change.

Review worker authority before sharing a cluster. Functional app hosting
requires cluster-wide Secret reads, including with managed topology. Splitting
fleets into namespaces does not remove that exposure. Network-management and
controller-adapter identities have different, bounded permissions; cache
filters are not authorization boundaries.

## Confirm the outcome

Check the worker's readiness and referenced Secret revision after a credential
change, verify the intended transport, and inspect the installed identities and
bindings. Device mutation gates remain off until deliberately enabled. Use
[worker logs and device diagnostics](troubleshooting.md#first-gather-the-basics)
for failures: hosted-application logs, exec, attach and port-forward are not
supported through the VK kubelet API.

## Related reading

[Configuration](CONFIGURATION.md) defines fields; [Getting Started](getting-started.md)
provides the first-device path. [Managed topology](topology-awareness.md) and
the [gNOI runbook](gnoi-iosxe-upgrade-runbook.md) own their migration and operation
sequences. The focused security pages explain the trust assumptions behind
those workflows.

<details markdown="1">
<summary>Links from earlier versions of this guide</summary>

## Detailed section links

Existing section links continue here. Choose **Continue** to open the full
contract on its focused page.

| Topic | Continue |
|---|---|
| <span id="credential-injection"></span>Injection contract | [Continue](security-credentials.md#credential-injection) |
| <span id="recommended-secret-credentialsecretref"></span>Create a Secret | [Continue](security-credentials.md#recommended-secret-credentialsecretref) |
| <span id="legacy-fallback-inline-password"></span>Inline-password fallback | [Continue](security-credentials.md#legacy-fallback-inline-password) |
| <span id="vk_device_password-precedence"></span>Password precedence | [Continue](security-credentials.md#vk_device_password-precedence) |
| <span id="rotating-a-single-password"></span>Rotate one password | [Continue](security-credentials.md#rotating-a-single-password) |
| <span id="managing-credentials-across-multiple-devices"></span>Fleet credentials | [Continue](security-credentials.md#managing-credentials-across-multiple-devices) |
| <span id="pattern-1-one-secret-per-device-different-credentials"></span>One Secret per device | [Continue](security-credentials.md#pattern-1-one-secret-per-device-different-credentials) |
| <span id="pattern-2-shared-secret-across-a-fleet-identical-credentials"></span>Shared fleet Secret | [Continue](security-credentials.md#pattern-2-shared-secret-across-a-fleet-identical-credentials) |
| <span id="pattern-3-same-password-different-usernames"></span>Shared password, distinct users | [Continue](security-credentials.md#pattern-3-same-password-different-usernames) |
| <span id="namespace-boundaries"></span>Namespace references | [Continue](security-credentials.md#namespace-boundaries) |
| <span id="bulk-rotation"></span>Bulk rotation | [Continue](security-credentials.md#bulk-rotation) |
| <span id="gitops-and-external-secret-managers"></span>External secret managers | [Continue](security-credentials.md#gitops-and-external-secret-managers) |
| <span id="hosted-application-credentials"></span>Application credentials | [Continue](security-credentials.md#hosted-application-credentials) |
| <span id="tls"></span>Device HTTPS | [Continue](security-tls.md#device-https) |
| <span id="secure-ios-xe-gnoi"></span>Secure gNOI | [Continue](security-tls.md#secure-ios-xe-gnoi) |
| <span id="software-upgrade-image-transport"></span>Upgrade image trust | [Continue](security-tls.md#software-upgrade-image-transport) |
| <span id="ssh-host-keys"></span>SSH host keys | [Continue](security-tls.md#ssh-host-keys) |
| <span id="kubelet-side-tls"></span>Kubelet serving TLS | [Continue](security-tls.md#kubelet-side-tls) |
| <span id="rbac-model"></span>RBAC model | [Continue](security-authorization.md#rbac-model) |
| <span id="controller-service-account-cisco-virtual-kubelet-controller"></span>Manager permissions | [Continue](security-authorization.md#controller-service-account-cisco-virtual-kubelet-controller) |
| <span id="vk-pod-service-account-cisco-virtual-kubelet"></span>Worker permissions | [Continue](security-authorization.md#vk-pod-service-account-cisco-virtual-kubelet) |
| <span id="network-controller-crd-validation-prerequisite"></span>Controller CEL prerequisite | [Continue](security-authorization.md#network-controller-crd-validation-prerequisite) |
| <span id="network-controller-worker-service-accounts"></span>Adapter identities | [Continue](security-authorization.md#network-controller-worker-service-accounts) |
| <span id="controller-intent-secret-authorization"></span>Intent Secret authorization | [Continue](security-authorization.md#controller-intent-secret-authorization) |
| <span id="production-rbac-hardening"></span>Delegation and hardening | [Continue](security-authorization.md#production-rbac-hardening) |
| <span id="finalizer-and-deletion"></span>Safe deletion | [Continue](security-authorization.md#finalizer-and-deletion) |
| <span id="principles"></span>Security principles | [Continue](security-authorization.md#principles) |

</details>
