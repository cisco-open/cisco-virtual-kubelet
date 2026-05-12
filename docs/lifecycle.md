# CVK Lifecycle And MDT State

CVK now treats MDT as a push signal for lifecycle freshness, not as the only source of truth. Device state flows through `internal/telemetry/state`, then wakes the Virtual Kubelet `PodNotifier` bridge. The provider still calls the IOS-XE driver for authoritative Pod status before notifying Kubernetes.

## Flow

1. IOS-XE emits MDT app-hosting updates over gNMI.
2. The telemetry mapper produces `MappedEvent` records.
3. `state.Cache` extracts app-hosting state by app ID and stores a short-lived record.
4. `AppHostingProvider.ObserveAppEvent` queues the event without blocking telemetry.
5. The PodNotifier worker resolves app ID to Pod UID, refreshes status from the driver, then invokes the upstream callback.

The queue is intentionally lossy under pressure. Drops increment:

```text
cisco_vk_telemetry_notifier_dropped_total{reason="consumer_backpressure"}
```

Polling remains the safety net. A dropped notification delays freshness, but does not make Kubernetes state authoritative over device state.

## Trace Correlation

During `CreatePod`, CVK stores the active VK span context in a bounded cache keyed by `(device, appID)`. When a later MDT app event arrives, the telemetry subscriber uses that cache to parent recovery spans under the original Pod admission trace while the entry is fresh.

The cache uses the W3C `traceparent` model from `internal/telemetry/correlation` and defaults to a 15 minute TTL with a 4096 item cap.
