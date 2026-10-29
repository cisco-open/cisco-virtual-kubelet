<div class="cvk-hub" markdown>

# Reference

Look up resource fields, commands and supported interfaces. For an end-to-end
workflow, start with [Guides](../guides.md) or [Operations](../operator-guide.md).

<div class="grid cards" markdown>

-   **Kubernetes resources**

    Find the right CRD and inspect device configuration, credentials, TLS and
    resource settings.

    [CRD reference](../crds.md) ·
    [CiscoDevice configuration](../CONFIGURATION.md)

-   **Commands and environment**

    Look up `cisco-vk` commands, the optional `kubectl-ciscovk` plugin,
    environment variables and chart settings.

    [CLI and plugin reference](../cisco-vk-cli.md) ·
    [Environment variables](../environment-variables.md)

-   **Configuration families**

    Inspect the documented model families and platform coverage before choosing
    desired-state fields.

    [IOS-XE families](families/README.md) ·
    [NX-OS families](families-nxos.md) ·
    [YANG version support](../yang-version-support.md)

-   **Interfaces and lifecycle**

    Check device protocols, kubelet endpoints and the software-operation model.

    [Device and kubelet API](../API.md) ·
    [Software lifecycle design](../gnoi-software-lifecycle.md)

</div>

## Terms, support and extension contracts

- [Glossary](../glossary.md): terminology used in the documentation.
- [Status and feature maturity](../status.md): implementation scope and
  qualification limits for `v2026.11.1`.
- [Security overview](../security.md): choose credentials, transport trust or access guidance.
- [RBAC and authorization](../security-authorization.md): roles, worker boundaries and delegation.
- [Topology design](../topology-reference.md): ownership, policy and campaign contracts.
- [Architecture](../ARCHITECTURE.md): component responsibilities and data flow.
- [Controller extension contract](../controller-extension-guide.md): requirements
  for adding a product adapter.

Use the [release archive](../releases/index.md) to distinguish published artifacts
from development plans and historical evidence.

</div>
