# API Reference

This document details the RESTCONF APIs used by the Cisco Virtual Kubelet Provider.

## RESTCONF Endpoints

### Base URL

```bash
https://<device-ip>/restconf
```

### Authentication
HTTP Basic Authentication with device credentials.

## App-Hosting Configuration

### Create Application Configuration

**Endpoint**: `POST /restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps`

**Payload**:
```json
{
  "Cisco-IOS-XE-app-hosting-cfg:app": {
    "application-name": "vk_default_nginx_1234567890",
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

### Get Application State

**Endpoint**: `GET /restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app={app-id}`

**Response**:
```json
{
  "Cisco-IOS-XE-app-hosting-oper:app": {
    "application-name": "vk_default_nginx_1234567890",
    "application-state": "RUNNING"
  }
}
```

## App-Hosting RPCs

### Install Application

**Endpoint**: `POST /restconf/operations/Cisco-IOS-XE-rpc:app-hosting`

**Payload**:
```json
{
  "Cisco-IOS-XE-rpc:input": {
    "install": {
      "appid": "vk_default_nginx_1234567890",
      "package": "flash:/nginx.tar"
    }
  }
}
```

### Activate Application

**Endpoint**: `POST /restconf/operations/Cisco-IOS-XE-rpc:app-hosting`

**Payload**:
```json
{
  "Cisco-IOS-XE-rpc:input": {
    "activate": {
      "appid": "vk_default_nginx_1234567890"
    }
  }
}
```

### Start Application

**Endpoint**: `POST /restconf/operations/Cisco-IOS-XE-rpc:app-hosting`

**Payload**:
```json
{
  "Cisco-IOS-XE-rpc:input": {
    "start": {
      "appid": "vk_default_nginx_1234567890"
    }
  }
}
```

### Stop Application

**Endpoint**: `POST /restconf/operations/Cisco-IOS-XE-rpc:app-hosting`

**Payload**:
```json
{
  "Cisco-IOS-XE-rpc:input": {
    "stop": {
      "appid": "vk_default_nginx_1234567890"
    }
  }
}
```

### Deactivate Application

**Endpoint**: `POST /restconf/operations/Cisco-IOS-XE-rpc:app-hosting`

**Payload**:
```json
{
  "Cisco-IOS-XE-rpc:input": {
    "deactivate": {
      "appid": "vk_default_nginx_1234567890"
    }
  }
}
```

### Uninstall Application

**Endpoint**: `POST /restconf/operations/Cisco-IOS-XE-rpc:app-hosting`

**Payload**:
```json
{
  "Cisco-IOS-XE-rpc:input": {
    "uninstall": {
      "appid": "vk_default_nginx_1234567890"
    }
  }
}
```

## Application States

| State | Description |
|-------|-------------|
| `DEPLOYED` | Package installed, ready for activation |
| `ACTIVATED` | Application activated, ready to start |
| `RUNNING` | Application is running |
| `STOPPED` | Application stopped |
| `UNINSTALLED` | Application removed |

## Error Responses

### 400 Bad Request
Invalid request payload or parameters.

### 401 Unauthorized
Authentication failed.

### 404 Not Found
Resource (application) not found.

### 409 Conflict
Operation cannot be performed in current state.

### 500 Internal Server Error
Device-side error.

## YANG Models

The provider uses these YANG models:

- `Cisco-IOS-XE-app-hosting-cfg.yang` - Configuration
- `Cisco-IOS-XE-app-hosting-oper.yang` - Operational state
- `Cisco-IOS-XE-rpc.yang` - RPC operations

## Testing APIs

Use curl to test RESTCONF endpoints:

```bash
# Get hostname
curl -k -u admin:password \
  https://192.168.1.100/restconf/data/Cisco-IOS-XE-native:native/hostname

# List applications
curl -k -u admin:password \
  https://192.168.1.100/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data

# Install application
curl -k -u admin:password \
  -X POST \
  -H "Content-Type: application/yang-data+json" \
  -d '{"Cisco-IOS-XE-rpc:input":{"install":{"appid":"test","package":"flash:/test.tar"}}}' \
  https://192.168.1.100/restconf/operations/Cisco-IOS-XE-rpc:app-hosting
```
