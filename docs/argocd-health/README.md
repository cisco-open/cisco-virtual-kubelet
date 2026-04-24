# ArgoCD health checks

Lua scripts that teach ArgoCD how to read CVK CR status. Without
these, ArgoCD reports CVK CRs as `Healthy` the moment they're
applied — before the controller has had a chance to reconcile —
which is misleading.

## Files

- `iosxeconfig.lua` — `IOSXEConfig.status.phase` → ArgoCD health.
- `iosxeconfigbundle.lua` — bundle status rolls up from each
  fanned-out child's phase. A bundle is `Healthy` only when every
  child is `InSync`.

## Installation

Append to ArgoCD's `argocd-cm` ConfigMap (the cluster-wide
configuration) under `resource.customizations.health.<group>_<kind>`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
data:
  resource.customizations.health.config.cisco.vk_IOSXEConfig: |
    <paste iosxeconfig.lua here>

  resource.customizations.health.config.cisco.vk_IOSXEConfigBundle: |
    <paste iosxeconfigbundle.lua here>
```

The Helm chart for ArgoCD accepts the same shape under
`configs.cm.resource.customizations`. ArgoCD picks up the
configuration on its next sync; no controller restart is needed.

## Mapping

| Phase / state                            | ArgoCD status |
|------------------------------------------|---------------|
| `InSync`                                 | Healthy       |
| `Drifted`                                | Degraded      |
| `Failed`                                 | Degraded      |
| `Paused`                                 | Suspended     |
| `Pending` / `Validating` / `Planning` …  | Progressing   |
| Bundle: any child Failed/Drifted         | Degraded      |
| Bundle: NoMatchingDevices                | Degraded      |
| Bundle: every child InSync               | Healthy       |
