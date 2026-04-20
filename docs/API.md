# API Reference

This page documents the two API surfaces you may interact with as a user or operator:

1. **Device-side RESTCONF** — the HTTP/JSON management API exposed by IOS-XE, which Cisco Virtual Kubelet uses to configure and observe App-Hosting. Useful to know about for troubleshooting with `curl`, and for understanding which YANG models are involved.
2. **VK-side kubelet endpoints** — the HTTPS endpoints each VK pod exposes on `:10250`. These are what `kubectl top node`, metrics-server, and Prometheus talk to.

You don't normally call RESTCONF by hand — the VK does it on your behalf as pods are created and deleted — but the reference below is accurate for manual debugging (see [Testing with curl](#testing-with-curl) at the bottom).

## Device side — RESTCONF

### Base URL

```
https://<device-ip>/restconf
```

### Authentication

HTTP Basic authentication with the device credentials. The VK sources the password from the `VK_DEVICE_PASSWORD` environment variable (injected from a Kubernetes Secret — see [Security → Credential injection](security.md#credential-injection)).

When testing with `curl`, `-k` is commonly needed because lab devices often present self-signed certificates. In production, supply `caFile` in the TLS config instead.

### App-hosting configuration

#### Create / list application configuration

| Operation | Path |
|---|---|
| List / create | `POST/GET /restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps` |
| Delete | `DELETE /restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app={appID}` |

**Create payload:**

```json
{
  "Cisco-IOS-XE-app-hosting-cfg:app": {
    "application-name": "cvk00000_a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
    "application-network-resource": {
      "appintf-vlan-rules": {
        "appintf-vlan-rule": [
          {
            "vlan-id": 100,
            "guest-interface": 0,
            "guest-ipaddress": "192.168.1.200",
            "guest-netmask": "255.255.255.0",
            "guest-gateway": "192.168.1.1"
          }
        ]
      }
    },
    "application-resource-profile": {
      "profile-name": "custom",
      "cpu": 1000,
      "memory": 512,
      "vcpu": 1
    }
  }
}
```

The `application-name` follows the `cvk<index>_<podUID32char>` convention. See [Architecture → Pod-to-app naming](ARCHITECTURE.md#pod-to-app-naming).

#### Get application oper-data

```
GET /restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app={app-id}
```

Response includes `application-state`, `pkg-policy`, resource usage, and IPv4 / MAC info when DHCP has resolved.

### Lifecycle RPCs

All lifecycle operations target the app-hosting RPC endpoints:

| Operation | Path |
|---|---|
| Install | `POST /restconf/operations/Cisco-IOS-XE-app-hosting-rpcs:app-install` |
| Activate | `POST /restconf/operations/Cisco-IOS-XE-app-hosting-rpcs:app-activate` |
| Start | `POST /restconf/operations/Cisco-IOS-XE-app-hosting-rpcs:app-start` |
| Stop | `POST /restconf/operations/Cisco-IOS-XE-app-hosting-rpcs:app-stop` |
| Deactivate | `POST /restconf/operations/Cisco-IOS-XE-app-hosting-rpcs:app-deactivate` |
| Uninstall | `POST /restconf/operations/Cisco-IOS-XE-app-hosting-rpcs:app-uninstall` |

**Install payload:**

```json
{
  "Cisco-IOS-XE-app-hosting-rpcs:input": {
    "appid": "cvk00000_<pod-uid>",
    "package": "flash:/hello-app.iosxe.tar"
  }
}
```

Activate / Start / Stop / Deactivate / Uninstall payloads take only `appid`.

### Topology (used by `TopologyProvider`)

| Operation | Path |
|---|---|
| CDP neighbors | `GET /restconf/data/Cisco-IOS-XE-cdp-oper:cdp-neighbor-details` |
| OSPF neighbors | `GET /restconf/data/Cisco-IOS-XE-ospf-oper:ospf-oper-data` |
| Interfaces (including stats and IPs) | `GET /restconf/data/ietf-interfaces:interfaces` |
| ARP table (IP discovery fallback) | `GET /restconf/data/Cisco-IOS-XE-arp-oper:arp-data` |

### Device info

| Operation | Path |
|---|---|
| Software version | `GET /restconf/data/Cisco-IOS-XE-native:native/version` |
| Hostname | `GET /restconf/data/Cisco-IOS-XE-native:native/hostname` |

## Application states

Live oper-data can report the following values in `application-state`:

| State | Meaning |
|---|---|
| `INSTALLED` | Package extracted, config applied, not yet deployed |
| `INSTALLING` | Install in progress — transient |
| `DEPLOYED` | Ready for activation |
| `ACTIVATED` | Activated, ready to start |
| `RUNNING` | Currently running |
| `STOPPED` | Stopped but still activated — can be restarted directly |
| `UNINSTALLED` | Package removed (typically not visible for long) |

The reconciler drives transitions via the lifecycle RPCs. See [Architecture → App lifecycle state machine](ARCHITECTURE.md#app-lifecycle-state-machine) for the full state diagram.

### Package policy

The oper-data also exposes `pkg-policy`. Values:

| Value | Meaning |
|---|---|
| `iox-pkg-policy-signed` | Package passed signature verification |
| `iox-pkg-policy-unsigned` | Package unsigned, but device allows unsigned (`no app-hosting signed-verification`) |
| `iox-pkg-policy-invalid` | **Ambiguous** — either the package failed verification, **or** the YANG default during the first seconds of every install before validation completes |

The reconciler treats `iox-pkg-policy-invalid` as a fatal install blocker only when:

1. `spec.allowUnsignedApps = false` (the default), **and**
2. A confirming install notification has been received from the device.

The kubelet-exposed pod `status.reason` for this case is **`PackagePolicyInvalid`**.

## HTTP error responses

| Status | Meaning |
|---|---|
| `400` | Invalid request payload or parameters |
| `401` | Authentication failed |
| `404` | Resource (app) not found |
| `409` | Operation invalid in current state (e.g. start from `DEPLOYED` without activating first) |
| `500` | Device-side error |

## YANG models referenced

- `Cisco-IOS-XE-app-hosting-cfg` — app config
- `Cisco-IOS-XE-app-hosting-oper` — app runtime state (`application-state`, `pkg-policy`, IPs, MACs)
- `Cisco-IOS-XE-app-hosting-rpcs` — lifecycle RPCs
- `Cisco-IOS-XE-cdp-oper` — CDP neighbor discovery
- `Cisco-IOS-XE-ospf-oper` — OSPF neighbor state
- `Cisco-IOS-XE-arp-oper` — ARP-based pod IP discovery
- `Cisco-IOS-XE-native` — device identity (hostname, version)
- `ietf-interfaces` — interface list, addresses, and statistics

## VK side — kubelet endpoints

Each VK pod runs an HTTPS listener on `:10250`:

| Path | Handler | Purpose |
|---|---|---|
| `/stats/summary` | `GetStatsSummary` | Kubernetes stats — powers `kubectl top node` and HPA/VPA |
| `/metrics/resource` | `GetMetricsResource` | Prometheus metrics — device CPU/memory/storage + interface/topology metrics |
| `/containerLogs/…` | (unsupported) | Returns HTTP 501 |
| `/exec/…` | (unsupported) | Returns HTTP 501 |
| `/attach/…` | (unsupported) | Returns HTTP 501 |
| `/portForward/…` | (unsupported) | Returns HTTP 501 |

See [Observability](observability.md) for the full metrics catalog and stats schema.

## Testing with curl

```bash
# Device hostname
curl -k -u admin:<password> \
  https://192.168.1.100/restconf/data/Cisco-IOS-XE-native:native/hostname

# List app-hosting apps
curl -k -u admin:<password> \
  https://192.168.1.100/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data

# Install
curl -k -u admin:<password> \
  -X POST \
  -H "Content-Type: application/yang-data+json" \
  -d '{"Cisco-IOS-XE-app-hosting-rpcs:input":{"appid":"test","package":"flash:/test.tar"}}' \
  https://192.168.1.100/restconf/operations/Cisco-IOS-XE-app-hosting-rpcs:app-install
```
