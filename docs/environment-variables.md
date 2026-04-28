# Environment Variables Support

The Cisco Virtual Kubelet provider supports Kubernetes environment variables in container specifications, allowing applications to receive configuration data from multiple sources. This document describes the supported environment variable types, limitations, and best practices.

## Overview

Environment variables are passed to containers running on IOS-XE devices via Docker `-e` flags in the RunOpts configuration. The provider supports all major Kubernetes environment variable sources while respecting IOS-XE platform constraints.

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
- **Maximum Characters Per Line**: 235 characters
- **Total Capacity**: ~7,050 characters

The provider automatically distributes environment variables across multiple RunOpts lines when needed.

### Security Features

- **Shell Injection Prevention**: All values are properly escaped
- **Special Character Handling**: Quotes, dollar signs, backticks, and backslashes are safely escaped
- **Value Wrapping**: All values are wrapped in double quotes for safe shell parsing

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

1. **Minimize total environment variable size**: Stay well under the 7,050 character limit
2. **Use short variable names**: Reduces RunOpts overhead
3. **Consolidate configuration**: Group related settings in ConfigMaps

### Organization

1. **Use descriptive Secret/ConfigMap names**: `database-credentials`, `app-config`
2. **Use consistent naming conventions**: `DB_PASSWORD`, `API_BASE_URL`
3. **Document required vs optional variables**: In pod annotations or README

## Examples

### Complete Pod with Mixed Sources

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: web-app
  namespace: production
spec:
  containers:
  - name: web-server
    image: nginx:latest
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
  
  # Required resources must exist
  imagePullSecrets:
  - name: registry-credentials
---
apiVersion: v1
kind: Secret
metadata:
  name: database-credentials
  namespace: production
type: Opaque
data:
  password: c3VwZXItc2VjcmV0LXBhc3N3b3Jk  # base64 encoded
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

The above pod specification would generate RunOpts similar to:

```bash
docker run \
  --label pod=web-app \
  --label namespace=production \
  --label container=web-server \
  -e NODE_ENV="production" \
  -e PORT="8080" \
  -e DB_HOST="db.production.example.com" \
  -e DB_PASSWORD="super-secret-password" \
  -e API_BASE_URL="https://api.example.com/v1" \
  -e CACHE_TTL="300" \
  nginx:latest
```

Note: `ANALYTICS_API_KEY` is omitted because the optional secret doesn't exist.

## Troubleshooting

### Pod Deployment Fails

1. **Check Secret/ConfigMap existence**: `kubectl get secret,configmap -n <namespace>`
2. **Verify key names**: `kubectl describe secret <name>` or `kubectl describe configmap <name>`
3. **Check namespaces**: Resources must be in the same namespace as the pod
4. **Review error messages**: Pod events contain specific error details

### Environment Variables Missing in Container

1. **Verify resolution worked**: Check pod events for resolution errors
2. **Check optional vs required**: Optional missing resources are silently omitted
3. **Validate character limits**: Excessive environment variables may be truncated
4. **Test with direct values**: Isolate whether the issue is with resolution or container setup

### Character Limit Exceeded

```bash
# Check total environment variable size
kubectl get pod <pod-name> -o yaml | grep -A 20 "env:" | wc -c
```

If approaching limits:
1. **Use shorter variable names**
2. **Consolidate related configuration into fewer variables**
3. **Use ConfigMap volume mounts instead** for large configuration files
4. **Split functionality across multiple containers**

## Integration with Cisco IOS-XE

### Generated Configuration

Environment variables are integrated into IOS-XE App-Hosting configuration:

```
app-hosting appid web-app
 app-vnic management guest-interface 0
 app-default-gateway 192.168.1.1 guest-interface 0
 app-resource docker
  run-opts 1 "--label pod=web-app -e NODE_ENV=\"production\""
  run-opts 2 "-e DB_PASSWORD=\"super-secret-password\""
```

### Monitoring

Check app status and environment variable propagation:

```bash
# On IOS-XE device
show app-hosting list
show app-hosting detail appid web-app
show app-hosting utilization appid web-app
```

Environment variables appear in the container's runtime environment and can be verified through application logs or debugging commands if container access is available.