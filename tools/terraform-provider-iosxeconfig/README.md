# terraform-provider-iosxeconfig

Reverse-direction integration with cisco-virtual-kubelet: Terraform
authors **author** IOSXEConfig CRs in a cluster. The provider does
not run the config driver — it just speaks to the Kubernetes API.
The driver itself stays Kubernetes-native (no Terraform in the
runtime, no Terraform state for device intent).

This is a Phase-8 scaffold. The directory structure, module
boundary, and provider/resource skeletons are in place; building
a release-quality binary is the next iteration. Everything here
lives in its own Go module so the main `cisco-virtual-kubelet`
module's dependency graph stays clean.

## Layout

```
.
├── go.mod                       # separate module
├── main.go                      # plugin entrypoint
└── internal/provider/
    ├── provider.go              # provider configuration (kubeconfig)
    └── resource_iosxeconfig.go  # iosxeconfig_config resource
```

## Use

Operators write `.tf` files like:

```hcl
terraform {
  required_providers {
    iosxeconfig = {
      source  = "cisco-open/iosxeconfig"
      version = "0.1.0"
    }
  }
}

provider "iosxeconfig" {
  # Defaults to KUBECONFIG / in-cluster auto-detection.
}

resource "iosxeconfig_config" "edge_01" {
  namespace        = "network"
  name             = "edge-01"
  device_ref       = "edge-01"
  managed_families = ["vlan", "vrf", "interface_ethernet"]
  drift_policy     = "report"
  source_inline    = file("${path.module}/edge-01.yaml")
}
```

Terraform's `apply` writes the IOSXEConfig CR; the per-device
`cisco-vk run` pod picks it up via the existing reconcile loop.

## Why a separate module

- Pulls `terraform-plugin-framework` and the Hashicorp
  `terraform-plugin-go` runtime, neither of which the controller
  has any business depending on.
- Lets the provider release on its own cadence (Terraform Registry
  publishes the binary; the controller's Helm chart ships
  independently).
- Keeps the provider's MPL-2.0 / Apache-2.0 licensing analysis
  scoped to its own go.sum.

## Status

The skeleton compiles in isolation but the provider is not yet
published to the Terraform Registry. CRUD methods on the
`iosxeconfig_config` resource currently return `not implemented`
and serve as the contract surface for the next iteration.
