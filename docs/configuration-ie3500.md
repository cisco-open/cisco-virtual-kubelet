# IE3500 Series

This page covers installation on the **Cisco IE3500 Series Industrial Ethernet Switch** (IE-3505, IE-3510). The networking mode on this platform is **AppGigabitEthernet** in trunk mode — a dedicated hardware port that bridges container traffic into a VLAN with a DHCP pool on the device.

## Validated software

| Device | Validated IOS-XE |
|---|---|
| Cisco IE-3505-8P3S | 17.18.2 |

Other IOS-XE versions with App Hosting may work but are not validated.

!!! tip "Config writer support"
    IOS-XE 17.18 is fully supported by the CVK Network-as-Code config writer.
    VirtualPortGroup / DHCP pool creation and all `IOSXEConfig` writes are handled
    automatically — **no manual RESTCONF workaround is required.**

## Platform notes

### Flash storage path

The IE3500 uses `flash:` as its primary storage identifier. When setting the pod `image`
field, use `flash:` rather than `bootflash:` (which is IR1100-specific):

```yaml
image: "flash:hello-app.iosxe.tar"  # IE3500 / Cat8000V / Cat9000
# image: "bootflash:hello-app.iosxe.tar"   # IR1100 only
```

### Copying images to the device

The IE3500 requires the SCP server to be enabled before accepting file transfers:

```
ip scp server enable
```

Transfer using legacy SCP mode (`-O`):

```bash
scp -O hello-app.iosxe.tar admin@<device-ip>:/hello-app.iosxe.tar
```

Verify the file arrived:

```
ie3500# dir flash:/*tar
Directory of flash:/

163979  -rw-    15123968   Jun 2 2026 03:25:35 +00:00  hello-app.iosxe.tar

5284429824 bytes total (4388491264 bytes free)
```

### Package signing enforcement

IOS-XE 17.18 on the IE3500 enforces package signature validation by default. The
`hello-app.iosxe.tar` test image does not carry a signature file (`package.cert` /
`package.sign`). Set `allowUnsignedApps: true` in the `CiscoDevice` spec:

```yaml
spec:
  allowUnsignedApps: true
```

When this flag is set CVK performs two actions:

1. **Device-side** — on first connect it applies a RESTCONF PUT to disable
   the global signature check on the device:
   ```
   PUT /restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/controls
   {"Cisco-IOS-XE-app-hosting-cfg:controls": {"sign-verification": false}}
   ```
   This is equivalent to the CLI `no app-hosting signed-verification` and is
   reflected in `show app-hosting infra` as `App signature verification: disabled`.

2. **Controller-side** — the reconciler treats a transient `iox-pkg-policy-invalid`
   state as non-fatal while the device completes the install.

Without this flag, installs of unsigned packages fail with:

```
App signature validation is required.
App signature file package.cert or package.sign not found in package
```

!!! note
    The `sign-verification` leaf lives in the `controls` presence container of the
    `Cisco-IOS-XE-app-hosting-cfg` YANG module (version 2022-11-01). If the PUT
    fails (e.g. insufficient privilege), CVK logs a warning and continues — the
    device policy may then still block unsigned installs.

## Device prerequisites

Apply the following IOS-XE config on the device before deploying pods.

### Enable RESTCONF and SCP

```
! Enable the RESTCONF HTTPS listener
restconf

! Enable SCP server (required on IE3500, not needed on IR1100)
ip scp server enable
```

### Configure app-hosting networking

The IE3500's AppGigabitEthernet port (`Ap1/1`) must be put in trunk mode with a dedicated
VLAN. Create the VLAN, SVI, and DHCP pool:

```
vlan 200
 name app-hosting

interface Vlan200
 ip address 192.168.200.1 255.255.255.0
 no shutdown

ip dhcp excluded-address 192.168.200.1

ip dhcp pool app-hosting-vlan200
 network 192.168.200.0 255.255.255.0
 default-router 192.168.200.1
 dns-server 8.8.8.8
 lease 0 1

interface AppGigabitEthernet1/1
 switchport mode trunk
 switchport trunk allowed vlan 200
 no shutdown
```

Adjust the VLAN ID, IP range, and DNS to match your environment.

!!! note "Access vs trunk mode"
    The IE3500 AppGigabitEthernet port carries VLAN 1 (management) natively. Using
    **trunk mode** with an explicit `switchport trunk allowed vlan` list isolates app traffic
    from management traffic. The CVK CiscoDevice CR must reference the same VLAN ID using
    the `vlanIf` networking block (see below).

## VK configuration

### CiscoDevice CR

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: ie3500
  namespace: cisco-vk
spec:
  driver: XE
  address: "10.62.8.117"        # management IP of the switch
  port: 443
  username: admin
  credentialSecretRef:
    name: ie3500-creds           # Secret with key: password
  tls:
    enabled: true
    insecureSkipVerify: true     # acceptable for lab; use caFile in production
  allowUnsignedApps: true        # required for unsigned test packages on 17.18
  xe:
    networking:
      interface:
        type: AppGigabitEthernet
        appGigabitEthernet:
          mode: trunk
          vlanIf:
            dhcp: true           # container IPs from app-hosting-vlan200 pool
            vlan: 200            # must match vlan 200 in device config above
            guestInterface: 0   # container-side eth index (0 = eth0)
```

### Credentials Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ie3500-creds
  namespace: cisco-vk
type: Opaque
stringData:
  username: admin
  password: <device-password>
```

### Pod manifest

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: hello-app-ie3500
  namespace: cisco-vk
spec:
  nodeName: ie3500
  tolerations:
    - key: "cisco.io/device"
      operator: "Exists"
      effect: "NoSchedule"
  containers:
    - name: hello-app
      image: "flash:hello-app.iosxe.tar"
      resources:
        requests:
          cpu: "500m"
          memory: "128Mi"
```

!!! note "No ephemeral-storage limit needed"
    The IE3500 has ~4 GB of free flash. The CVK default `disk-size-mb` of 1024 MB fits
    comfortably. The `ephemeral-storage` override required by the IR1100 is not needed here.

## Verification

### On the cluster

```bash
kubectl get ciscodevice ie3500 -n cisco-vk
# PHASE should reach Ready within ~30s

kubectl get node ie3500
# STATUS should be Ready; VERSION shows the IOS-XE release

kubectl get pod hello-app-ie3500 -n cisco-vk -o wide
# IP should be in the 192.168.200.0/24 DHCP pool range
```

Expected node output:

```
NAME     STATUS   ROLES   AGE   VERSION   INTERNAL-IP   OS-IMAGE   KERNEL-VERSION
ie3500   Ready    <none>  27m   v1.12.0   10.62.8.117   IOS-XE     17.18.2
```

Expected pod output:

```
NAME               READY   STATUS    RESTARTS   AGE   IP              NODE
hello-app-ie3500   1/1     Running   0          5m    192.168.200.2   ie3500
```

### On the device (RESTCONF)

```bash
curl -sk -u admin:<password> -H "Accept: application/yang-data+json" \
  "https://<device-ip>/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data" \
  | python3 -c "
import json,sys
d=json.load(sys.stdin)
for a in d.get('Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data',{}).get('app',[]):
    print(a.get('name'), '->', a.get('details',{}).get('state'))
"
```

Expected output:

```
cvk0000_<pod-uid> -> RUNNING
```

### Verify DHCP binding

```bash
curl -sk -u admin:<password> -H "Accept: application/yang-data+json" \
  "https://<device-ip>/restconf/data/Cisco-IOS-XE-dhcp-oper:dhcp-oper-data/client-details" \
  | python3 -c "
import json,sys
d=json.load(sys.stdin)
clients=d.get('Cisco-IOS-XE-dhcp-oper:client-details',{}).get('client-detail',[])
for c in clients:
    print('ip:', c.get('ip-address'), 'state:', c.get('state'))
"
```

### Verify Vlan200 SVI

```bash
curl -sk -u admin:<password> -H "Accept: application/yang-data+json" \
  "https://<device-ip>/restconf/data/Cisco-IOS-XE-interfaces-oper:interfaces/interface=Vlan200" \
  | python3 -c "
import json,sys
d=json.load(sys.stdin)
i=d.get('Cisco-IOS-XE-interfaces-oper:interface',[{}])
if isinstance(i,list): i=i[0]
print('oper-status:', i.get('oper-status'))
print('IPv4:', i.get('ipv4'))
"
```

Expected output:

```
oper-status: if-oper-state-ready
IPv4:        192.168.200.1
```

## Comparison with other platforms

| | IE3500 | IR1100 | Catalyst 9000 |
|---|---|---|---|
| Networking mode | AppGigabitEthernet trunk | VirtualPortGroup | AppGigabitEthernet |
| Storage path | `flash:` | `bootflash:` | `flash:` |
| Config writer | ✅ 17.18 supported | ❌ 17.12 not supported | ✅ 17.18 supported |
| SCP server enable | Required | Not required | Not required |
| Package signing | Enforced (need `allowUnsignedApps`) | Not enforced | Device-dependent |
| Storage headroom | ~4 GB free | ~500 MB free | Varies |

## See also

- [Getting Started](getting-started.md) — end-to-end deployment
- [Configuration](CONFIGURATION.md) — full field reference, applies to any platform
- [IR1100 Series](configuration-ir1101.md) — VirtualPortGroup networking, 17.12 workarounds
- [Catalyst 9000](configuration-cat9000.md) — AppGigabitEthernet (access/trunk) on enterprise switches
- [Troubleshooting](troubleshooting.md) — DHCP issues, IP discovery, install failures
