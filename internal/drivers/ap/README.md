# Catalyst Access Point IOx driver

The `AP` driver manages IOx applications on a Catalyst Access Point through the
AP's native CAF HTTPS API. It connects directly to the AP; it does not contact
a wireless LAN controller or Catalyst Center and does not build packages.

## Device configuration

Set `spec.driver` to `AP`. The `address`, `username`, and `password` identify
the AP's IOx management endpoint. The API port defaults to `8443`; configure
`spec.port` to override it. TLS verification uses the device's shared TLS
configuration. The AP must have IOx and app hosting enabled and be reachable
from the CVK controller.

`spec.maxPods` defaults to 8 and advertises the AP's pod capacity to the
scheduler. Set it to a value appropriate for the AP model and available IOx
resources.

## Pod image contract

Each regular container represents one IOx application. Set `container.image`
to an HTTP(S) URL ending in `.tar` or `.tar.gz` that serves a **prebuilt IOx
package** (for example, the artifact produced from the `Catalyst-AP-APP`
sample). The controller downloads the archive and uploads it to the AP. OCI
image references and Docker registries are not supported. The artifact server
must be reachable without authentication or redirects. Archives are limited
to 512 MiB.

By default, activation maps package interface `eth0` to AP network `iox-nat0`
with `1to1` port mapping. Override the complete CAF activation body with Pod
annotation `cisco.io/ap-activation-payload`, for example:

```yaml
metadata:
  annotations:
    cisco.io/ap-activation-payload: >-
      {"resources":{"profile":"custom","cpu":"50","memory":"50",
      "disk":"100","network":[{"interface-name":"eth0",
      "network-name":"iox-nat0","port_map":{"mode":"auto"}}]}}
```

IOx package descriptors and activation resources remain responsible for
application-specific CPU, memory, disk, device, and network requirements.
Kubernetes commands, arguments, environment variables, ports, probes, volumes,
resource requests/limits, and container security settings are rejected because
the driver cannot translate them into the IOx package format.

The driver supports app install, activate, start, status, stop, deactivate,
and uninstall lifecycle operations. AP CAF does not retain Kubernetes
namespace/name metadata for these apps, so inventory discovered after a CVK
restart is reconstructed from CVK's generated application IDs.
