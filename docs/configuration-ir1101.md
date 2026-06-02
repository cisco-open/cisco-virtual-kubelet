# IR1100 Series

This page covers installation on the **Cisco IR1100 Series Industrial Router** (IR1101 and IR1109). The typical networking mode on this platform is **VirtualPortGroup** — a logical L3 interface that containers share, with IPs handed out by a DHCP pool on the device.

## Validated software

| Device | Validated IOS-XE |
|---|---|
| Cisco IR1101-K9 | 17.12.4 |

Other IOS-XE versions with App Hosting may work but are not validated.

!!! warning "Config writer limitation on 17.12"
    The CVK Network-as-Code config writer supports IOS-XE 17.9, 17.15, 17.16, 17.18, and 26.1.
    **IOS-XE 17.12 is not in the supported set.** `IOSXEConfig` writes are disabled at runtime and
    a warning is logged at startup:

    ```
    device version is not in the supported release set; IOSXEConfig writes disabled
    IOSXEConfig reconciler not registered; continuing with diagnostics and gNOI lifecycle controllers
    ```

    App-hosting pod lifecycle (create, update, delete) works normally — only the declarative
    config CRDs (`IOSXEConfig`, bundles, templates, etc.) are affected.
    The [VirtualPortGroup and DHCP pool](#device-prerequisites) must be created manually
    before deploying pods. See [Manual RESTCONF setup](#manual-restconf-setup-1712) below.

## Platform notes

### Flash storage path

The IR1100 uses `bootflash:` as its primary storage identifier. When setting the pod `image`
field, use `bootflash:` rather than `flash:`:

```yaml
image: "bootflash:hello-app.iosxe.tar"  # IR1100
# image: "flash:hello-app.iosxe.tar"   # Cat8000V / Cat9000
```

### Copying images to the device

SSH port 22 is not exposed on the IR1100's management interface by default. Use `scp -O`
(legacy SCP mode, not SFTP) with the bare filename path:

```bash
scp -O hello-app.iosxe.tar admin@<device-ip>:/hello-app.iosxe.tar
```

The `-O` flag forces the legacy SCP protocol. Without it, modern OpenSSH clients attempt
SFTP-based transfers that the device rejects. The device closes the connection after transfer;
this is normal.

Verify the file arrived:

```
ir1101# dir bootflash:/*tar
Directory of bootflash:/

   47  -rw-    15123968   Jun 1 2026 12:40:28 +01:00  hello-app.iosxe.tar

2896027648 bytes total (628789248 bytes free)
```

### IOx storage capacity

The IR1100 has limited IOx persist-disk space (typically 2–3 GB quota, with ~500–600 MB free
after the base image). The CVK default `disk-size-mb` for a pod is **1024 MB**, which exceeds
the available capacity on a fresh device. Set `ephemeral-storage` in pod resources to control
the allocated disk size:

```yaml
resources:
  requests:
    ephemeral-storage: "512Mi"
  limits:
    ephemeral-storage: "512Mi"
```

CVK maps `ephemeral-storage` directly to `disk-size-mb` in the app-hosting profile. Choose a
value that fits within the available capacity shown by `dir bootflash:`.

## Device prerequisites

Apply the following IOS-XE config on the device before deploying pods.

### Enable IOx and RESTCONF

```
! Enable IOx (on-device container platform)
iox

! Enable the RESTCONF HTTPS listener
ip http secure-server
restconf

! Required if you plan to run unsigned container packages on IOS-XE 17.12.
! Note: spec.allowUnsignedApps: true will attempt to configure this via RESTCONF,
! but IOS-XE 17.12 may not support the sign-verification YANG leaf — apply
! these CLI commands manually to ensure unsigned installs are permitted.
app-hosting verification disable
no app-hosting signed-verification
```

### Create the VirtualPortGroup and DHCP pool

The VirtualPortGroup is the device's logical L3 interface used by App Hosting. Its IP address
becomes the default gateway for every container using DHCP on this device.

```
interface VirtualPortGroup0
 ip address 192.168.100.1 255.255.255.0

ip dhcp excluded-address 192.168.100.1

ip dhcp pool vpg0-pool
 network 192.168.100.0 255.255.255.0
 default-router 192.168.100.1
 dns-server 8.8.8.8
 lease 0 1
```

Adjust the IP range and DNS to match your environment.

#### Manual RESTCONF setup (17.12)

If you are running IOS-XE 17.12 and cannot apply the CLI above directly (e.g. in a
fully-automated pipeline), the same config can be applied via RESTCONF:

**Create VirtualPortGroup0:**

```bash
curl -sk -u admin:<password> -X PUT \
  "https://<device-ip>/restconf/data/Cisco-IOS-XE-native:native/interface/VirtualPortGroup=0" \
  -H "Content-Type: application/yang-data+json" \
  -d '{
    "Cisco-IOS-XE-native:VirtualPortGroup": {
      "name": 0,
      "ip": {
        "address": {
          "primary": {
            "address": "192.168.100.1",
            "mask": "255.255.255.0"
          }
        }
      }
    }
  }'
# Expected: HTTP 201
```

**Create the DHCP pool:**

```bash
curl -sk -u admin:<password> -X PATCH \
  "https://<device-ip>/restconf/data/Cisco-IOS-XE-native:native/ip" \
  -H "Content-Type: application/yang-data+json" \
  -d '{
    "Cisco-IOS-XE-native:ip": {
      "dhcp": {
        "Cisco-IOS-XE-dhcp:pool": [{
          "id": "vpg0-pool",
          "network": {
            "primary-network": {
              "number": "192.168.100.0",
              "mask": "255.255.255.0"
            }
          },
          "default-router": {
            "default-router-list": ["192.168.100.1"]
          },
          "dns-server": {
            "dns-server-list": ["8.8.8.8"]
          },
          "lease": {
            "lease-value": { "days": 0, "hours": 1 }
          }
        }]
      }
    }
  }'
# Expected: HTTP 204
```

## VK configuration

### CiscoDevice CR

```yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: ir1101
  namespace: cisco-vk
spec:
  driver: XE
  address: "10.62.8.123"        # management IP of the router
  port: 443
  username: admin
  credentialSecretRef:
    name: ir1101-creds           # Secret with key: password
  tls:
    enabled: true
    insecureSkipVerify: true     # acceptable for lab; use caFile in production
  # allowUnsignedApps: true      # uncomment when running unsigned packages
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: true             # container IPs from the DHCP pool above
          interface: "0"         # VirtualPortGroup0
          guestInterface: 0      # container-side eth index (0 = eth0)
```

### Pod manifest

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: hello-app
  namespace: cisco-vk
spec:
  nodeName: ir1101
  tolerations:
    - key: "cisco.io/device"
      operator: "Exists"
      effect: "NoSchedule"
  containers:
    - name: hello-app
      image: "bootflash:hello-app.iosxe.tar"
      resources:
        requests:
          cpu: "500m"
          memory: "128Mi"
          ephemeral-storage: "512Mi"   # fits within IR1100 available IOx disk
        limits:
          ephemeral-storage: "512Mi"
```

## Verification

### On the cluster

```bash
kubectl get ciscodevice ir1101 -n cisco-vk
# PHASE should reach Ready within ~15s

kubectl get node ir1101
# STATUS should be Ready; VERSION shows the IOS-XE release

kubectl get pod hello-app -n cisco-vk -o wide
# IP should be in the DHCP pool range (e.g. 192.168.100.2)
```

Expected node output:

```
NAME     STATUS   ROLES   AGE   VERSION   INTERNAL-IP   OS-IMAGE   KERNEL-VERSION
ir1101   Ready    <none>  27m   v1.12.0   10.62.8.123   IOS-XE     17.12.4
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

### Verify VirtualPortGroup

```bash
curl -sk -u admin:<password> -H "Accept: application/yang-data+json" \
  "https://<device-ip>/restconf/data/Cisco-IOS-XE-interfaces-oper:interfaces/interface=VirtualPortGroup0" \
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
IPv4:        192.168.100.1
```

## See also

- [Getting Started](getting-started.md) — end-to-end deployment
- [Configuration](CONFIGURATION.md) — full field reference, applies to any platform
- [YANG Version Support](yang-version-support.md) — adding support for additional IOS-XE versions
- [Troubleshooting](troubleshooting.md) — DHCP issues, IP discovery, install failures
