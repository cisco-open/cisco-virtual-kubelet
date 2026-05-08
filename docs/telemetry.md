# IOS-XE Telemetry

## Phase 1

Phase 1 adds the `IOSXETelemetry` custom resource for MDT-over-gNMI
subscription lifecycle management.

What works today:

- `IOSXETelemetry` CRD creation, update, delete, and status reporting.
- Per-device subscriber lifecycle in the `cvk-<device>` pod.
- A dedicated gNMI `Subscribe` client connection for telemetry, separate from
  the config driver gNMI connection.
- STREAM subscriptions using `SAMPLE`, `ON_CHANGE`, or `TARGET_DEFINED` stream
  modes.
- Multiplexing compatible subscriptions into one gNMI Subscribe RPC per
  `(encoding, sampleInterval)` bucket.
- Bounded notification buffering with buffer-overflow drops counted in status.
- Structured logs for received notifications, including path and update count.
- Reconnect backoff with `spec.reconnect`.

Phase 1 does not emit OpenTelemetry metrics, logs, or traces from device
telemetry yet. Mapping, classification, filtering, and signal export are
deferred to later phases.

Example:

```yaml
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXETelemetry
metadata:
  name: c9300x-environmental
  namespace: network
spec:
  deviceRef:
    name: c9300x-01
  subscriptions:
    - name: environmental
      mode: STREAM
      streamMode: SAMPLE
      sampleInterval: 30s
      encoding: PROTO
      paths:
        - /Cisco-IOS-XE-environment-oper:environment-sensors
  output:
    signal:
      - metrics
```
