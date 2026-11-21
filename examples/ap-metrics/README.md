# Catalyst AP metrics exporter

This lightweight ARM64 IOx app polls the Catalyst AP itself over its local SSH
CLI and exports the results as Prometheus text on port `9100`. It uses the
AP’s internal IOx host address (typically `192.168.11.1`), not a WLC connection.
The collector enters AP enable mode, runs read-only `show` commands, and never
requests client detail output or exports client identifiers.

## Metrics

- `cisco_ap_cpu_usage_percent` from the AP's local process CPU summary.
- `cisco_ap_memory_usage_percent`, `cisco_ap_memory_total_bytes`, and
  `cisco_ap_memory_available_bytes` from the AP's local memory summary.
- `cisco_ap_clients_associated` from `show dot11 clients`.
- Per-radio serving frequency, channel, channel width, and RX/TX packet
  counters from `show controllers dot11Radio <slot> frequency`.
- `ap_exporter_scrape_success` and
  `ap_exporter_last_scrape_timestamp_seconds` for collector health.

The AP CLI exposes aggregate client count directly. Per-client MAC, RSSI, and
SNR are deliberately not queried or included in labels. Channel utilization,
noise/interference, and client RSSI/SNR distributions remain WLC-sourced on
this AP/firmware combination and are not claimed by this direct collector.
Counters are Prometheus counters; current state and resource levels are gauges.
The default polling interval is 60 seconds to limit work on the resource-
constrained AP. Prometheus can continue scraping the exporter every 15 seconds;
each HTTP request serves the latest cached AP sample.

## Build for Catalyst 9100 APs

The CW9166 target is `aarch64`, not amd64. From this directory:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' \
  -o ap-metrics ./cmd/ap-metrics
docker buildx build --platform linux/arm64 --load -t cisco-ap-metrics:dev .
```

Create a protected `conf/package_config.ini` from the example, then package the
Docker image with Linux `ioxclient` (the AP builder requires ext2 support):

```sh
cp conf/package_config.ini.example conf/package_config.ini
ioxclient docker package -p ext2 -r 5 cisco-ap-metrics:dev ./conf
```

The Alpine rootfs includes the `/bin/sh` required by AP CAF. Keep the resulting
`conf/package.tar` below 20 MB; Cisco's total app plus extracted rootfs limit is
80 MB. The app uses the documented `c1.tiny` profile (32 MB RAM).

## AP access and provisioning

The AP must have direct SSH enabled and an enable secret configured. The
exporter runs inside the AP's IOx network and connects to `192.168.11.1:22` by
default. The AP's AP-join profile also needs to permit SSH access; no WLC CLI
credentials or WLC-side statistics configuration is required by the exporter.

Configure `package_config.ini` through CAF/ioxclient app configuration or a
protected deployment mechanism. Do not commit a populated config file or
publish credentials in the package artifact server. Pin the AP SSH host-key
fingerprint in `host_key_sha256`. Both the SSH login and enable secret are
required by the AP CLI collector.

The collector's local AP commands are:

```text
show processes cpu 0
show memory
show dot11 clients
show controllers dot11Radio 0 frequency
show controllers dot11Radio 1 frequency
show controllers dot11Radio 2 frequency
```

After activation, scrape `http://<AP-management-address>:9100/metrics`.
