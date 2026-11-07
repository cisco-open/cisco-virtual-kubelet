# Environment Variables Support

The Cisco Virtual Kubelet provider supports Kubernetes environment variables in container specifications, allowing applications to receive configuration data from multiple sources. This document describes the supported environment variable types, limitations, and best practices.

## Overview

Environment variables are passed to containers running on IOS-XE devices via
Docker `-e` flags in the RunOpts configuration. Supported sources are explicit
`env` values, `secretKeyRef` and `configMapKeyRef`. `fieldRef` and
`resourceFieldRef` return an error; `envFrom` is not expanded. Empty resolved
values are omitted. This page describes IOS-XE; NX-OS has different
[value restrictions](security-credentials.md#hosted-application-credentials).

## Supported Environment Variable Sources

### ✅ Direct Values

Static key-value pairs defined directly in the container specification:

```yaml
env:
  - name: NODE_ENV
    value: "production"
  - name: PORT
    value: "8080"
  - name: DEBUG_MODE
    value: "false"
```

### ✅ Secret References

Values resolved from Kubernetes Secrets for sensitive data:

```yaml
env:
  - name: DB_PASSWORD
    valueFrom:
      secretKeyRef:
        name: database-credentials
        key: password
  - name: API_TOKEN
    valueFrom:
      secretKeyRef:
        name: api-secrets
        key: token
        optional: true  # Won't fail if secret/key is missing
```

### ✅ ConfigMap References

Values resolved from Kubernetes ConfigMaps for configuration data:

```yaml
env:
  - name: API_BASE_URL
    valueFrom:
      configMapKeyRef:
        name: app-config
        key: api-url
  - name: LOG_LEVEL
    valueFrom:
      configMapKeyRef:
        name: app-config
        key: log-level
        optional: true
```

## Platform Constraints

### RunOpts Character Limits

IOS-XE App-Hosting has specific limitations for Docker RunOpts configuration:

- **Maximum Lines**: 30 lines
- **Maximum Characters Per Line**: 235, counted as bytes by the current implementation
- **Gross Capacity**: 30 × 235 bytes, shared with labels, hostname, option names and quoting

The provider distributes complete options across lines; it never splits one
option. A single oversized option or a need for more than 30 lines fails Pod
configuration rather than truncating a value. The gross capacity is not a
7,050-byte allowance for environment values alone.

### Security Features

- **Portable names**: Names must match `[A-Za-z_][A-Za-z0-9_]*`.
- **Value wrapping**: Values are wrapped in single quotes; embedded single quotes are escaped by closing and reopening the quoted string.
- **Literal special characters**: Dollar signs, backticks and double quotes remain literal inside the single-quoted value.

Secret values become device app-hosting run options and can be read by device
administrators. CVK does not include resolved options in its diagnostic logs;
see [Hosted-application credentials](security-credentials.md#hosted-application-credentials).

## Error Handling

### Required Resources

If a required Secret or ConfigMap is missing, pod deployment will fail with a descriptive error:

```
failed to resolve environment variable DB_PASSWORD: failed to get secret db-credentials: secret not found
```

### Optional Resources

If an optional Secret or ConfigMap is missing (marked with `optional: true`), the environment variable is silently omitted and deployment continues.

### Missing Keys

If a required key is missing from an existing Secret/ConfigMap, deployment fails. Optional keys are silently omitted.

## Best Practices

### Security

1. **Use Secrets for sensitive data**: Passwords, API keys, tokens
2. **Use ConfigMaps for configuration**: URLs, timeouts, feature flags
3. **Mark appropriate resources as optional**: Non-critical configuration
4. **Avoid embedding secrets in direct values**: Use Secret references instead

### Performance

1. **Minimize total environment variable size**: Leave room for option syntax, labels and hostname within the RunOpts limits
2. **Use short variable names**: Reduces RunOpts overhead
3. **Consolidate configuration**: Group related settings in ConfigMaps

### Organization

1. **Use descriptive Secret/ConfigMap names**: `database-credentials`, `app-config`
2. **Use consistent naming conventions**: `DB_PASSWORD`, `API_BASE_URL`
3. **Document required vs optional variables**: In pod annotations or README

## Examples

### Complete Pod with Mixed Sources

Create the `production` namespace first and register the intended virtual Node.
This example targets `cat9000-1` and assumes a signed application package is
already at `flash:/web-app.iosxe.tar` on that device. Change those values for
your setup; a registry reference such as `nginx:latest` is not a device image.
See [Image delivery](CONFIGURATION.md#image-delivery).

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: web-app
  namespace: production
spec:
  nodeName: cat9000-1
  tolerations:
  - key: virtual-kubelet.io/provider
    operator: Exists
  containers:
  - name: web-server
    image: flash:/web-app.iosxe.tar
    env:
    # Direct configuration
    - name: NODE_ENV
      value: "production"
    - name: PORT
      value: "8080"
    
    # Database credentials from secret
    - name: DB_HOST
      valueFrom:
        configMapKeyRef:
          name: database-config
          key: host
    - name: DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: database-credentials
          key: password
    
    # Optional features
    - name: ANALYTICS_API_KEY
      valueFrom:
        secretKeyRef:
          name: analytics-keys
          key: api-key
          optional: true
    
    # Application configuration
    - name: API_BASE_URL
      valueFrom:
        configMapKeyRef:
          name: app-config
          key: api-url
    - name: CACHE_TTL
      valueFrom:
        configMapKeyRef:
          name: app-config
          key: cache-ttl
          optional: true
---
apiVersion: v1
kind: Secret
metadata:
  name: database-credentials
  namespace: production
type: Opaque
stringData:
  password: <application-password>
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
  namespace: production
data:
  api-url: "https://api.example.com/v1"
  cache-ttl: "300"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: database-config
  namespace: production
data:
  host: "db.production.example.com"
  port: "5432"
```

### Generated Docker Command

CVK configures IOS-XE RunOpts rather than invoking a local `docker run`.
The environment portion contains options like these, packed with the labels
and hostname into the available lines (the password is redacted here):

```text
-e NODE_ENV='production'
-e PORT='8080'
-e DB_HOST='db.production.example.com'
-e DB_PASSWORD='<resolved password>'
-e API_BASE_URL='https://api.example.com/v1'
-e CACHE_TTL='300'
```

Note: `ANALYTICS_API_KEY` is omitted because the optional secret doesn't exist.

## Troubleshooting

### Pod Deployment Fails

1. **Check Secret/ConfigMap existence**: `kubectl get secret,configmap -n <namespace>`
2. **Verify key names**: Describe the Secret or ConfigMap in the Pod's namespace; avoid sharing secret values in diagnostic output
3. **Check namespaces**: Resources must be in the same namespace as the pod
4. **Review error messages**: Pod events contain specific error details

### Environment Variables Missing in Container

1. **Verify resolution worked**: Check pod events for resolution errors
2. **Check optional vs required**: Optional missing resources are silently omitted
3. **Check empty values**: Empty direct or resolved values are omitted by the current implementation
4. **Test with direct values**: Isolate whether the issue is with resolution or container setup

### Character Limit Exceeded

Inspect the Pod Events and worker error for either `single RunOpts option too
long` or `too many RunOpts lines needed`. Counting YAML characters does not
measure the rendered options: referenced values, quoting, labels and hostname
all affect the result. Errors report sizes without printing resolved secrets.

If approaching limits:

1. **Use shorter variable names**
2. **Consolidate related configuration into fewer variables**
3. **Use the application's supported configuration delivery** for large files, such as packaging non-secret configuration with the app or fetching it through an authenticated application mechanism; generic ConfigMap volume projection is not provided by this RunOpts path
4. **Split functionality only when the application supports separate containers**, each with its own bounded options

## Integration with Cisco IOS-XE

### Generated Configuration

Environment variables are integrated into IOS-XE App-Hosting configuration.
The provider enables `docker-resource` mode with `prepend-pkg-opts` and sets
the container hostname to the container name. This excerpt illustrates the
shape; actual app IDs use `cvk<index>_<pod-uid>` and line packing depends on the
complete options:

```
app-hosting appid cvk00000_<pod-uid>
 app-vnic management guest-interface 0
 app-default-gateway 192.168.1.1 guest-interface 0
 app-resource docker
  prepend-pkg-opts
  run-opts 1 "--label pod=web-app --hostname=web-server -e NODE_ENV='production'"
  run-opts 2 "-e DB_PASSWORD='<resolved password>'"
 app-resource profile custom
  cpu 1000
  memory 512
```

- **`prepend-pkg-opts`**: Ensures Docker run options are prepended to the container command.
- **`--hostname=<container-name>`**: Sets the container hostname to the Kubernetes container name from the pod spec.

### Monitoring

Check app status and environment variable propagation:

```bash
# On IOS-XE device
show app-hosting list
show app-hosting detail appid <app-id>
show app-hosting utilization appid <app-id>
```

Use the application's own diagnostic or logging mechanism to verify received
settings without printing secrets. Kubernetes logs, exec, attach and
port-forward are unsupported for hosted applications; worker Pod logs show
CVK lifecycle and resolution errors. See [application diagnostics](troubleshooting.md#hosted-application-logs-exec-attach-and-port-forward).
