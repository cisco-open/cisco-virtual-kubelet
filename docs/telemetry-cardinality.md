<!--
Copyright 2026 Cisco Systems Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# IOS-XE Telemetry Cardinality

`IOSXETelemetry.spec.cardinalityLimits.maxSeriesPerSubscription` caps the
number of distinct mapped series a single subscription can emit. Tune it per
CR, starting from the expected path fan-out:

```yaml
spec:
  cardinalityLimits:
    maxSeriesPerSubscription: 10000
    onExceeded: dropNewSeries
```

Use a higher cap for broad interface or protocol subscriptions and a lower cap
for small state-only subscriptions. A C9300X interface counter subscription is
typically around 10K series in the lab, so `10000` is a sensible starting point
for access-switch interface counters. Add headroom when subscribing to
interface counters plus queue, policy, optical, or per-protocol leaves in the
same CR.

## `dropNewSeries` Behavior

`dropNewSeries` preserves already-seen series and suppresses only new series
after the cap is reached. In practice:

- Existing interface counters continue to emit.
- Newly discovered interfaces, queues, neighbors, or keyed leaves are dropped.
- `status.observedSubscriptionState[].droppedEvents.cardinality_limit`
  increments for the affected subscription.
- Recovery requires either raising the cap or narrowing the subscribed paths;
  deleting/recreating the CR also resets the in-memory series cache.

## Monitoring

Monitor the drop counter by reason:

```promql
increase(cisco_vk_telemetry_notifications_dropped_total{reason="cardinality_limit"}[15m])
```

Alerting guidance:

- Warning: any cardinality drops for two consecutive 15-minute windows.
- Critical: drops continue for 30 minutes, or exceed 1% of
  `cisco_vk_telemetry_metric_points_emitted_total` over the same window.
- Page immediately when a production CR starts dropping after a device software
  upgrade or template change; that usually indicates a new keyed subtree.

Also watch reconnect pressure while investigating drops:

```promql
increase(cisco_vk_telemetry_stream_reconnects_total[15m])
```

The lab Bug A retest observed 5+ reconnects per device during recovery
validation. Treat more than 5 reconnects per device in 15 minutes outside a
maintenance or test window as an investigation threshold, and page when the
same device exceeds 10 reconnects in 15 minutes or stays nonzero for three
consecutive windows.

## Tuning Checklist

1. Start with the narrowest viable gNMI paths.
2. Set `maxSeriesPerSubscription` near the expected steady-state series count
   plus 20-30% headroom.
3. Split very broad telemetry into separate CRs so one noisy subtree cannot
   starve unrelated signals.
4. Confirm `cisco_vk_telemetry_notifications_dropped_total{reason="cardinality_limit"}`
   remains zero after deploy and after planned topology churn.
5. Revisit the cap after switch software upgrades, line-card changes, or new
   queue/QoS features that add keyed telemetry leaves.
