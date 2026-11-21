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
- `ap_exporter_scrape_success`, `ap_exporter_last_scrape_timestamp_seconds`,
  and `ap_exporter_last_success_timestamp_seconds` for collector health.

Only the aggregate client count is parsed from the client command output.
Per-client identifiers, RSSI, and SNR are not exported or included in labels.
Channel utilization, noise/interference, and client RSSI/SNR distributions
are outside this collector's supported metrics.
Counters are Prometheus counters; current state and resource levels are gauges.
The default polling interval is 60 seconds to limit work on the resource-
constrained AP. Prometheus can continue scraping the exporter every 15 seconds;
each HTTP request serves the latest cached AP sample.

A collection succeeds only when CPU, total/available memory, and associated
client count all parse correctly. Missing or disabled radios are omitted;
radio bands come from the reported serving frequency, so a slot operating in
a different band is labelled correctly. Packet counters without a serving
frequency use `band="unknown"`. A failed collection clears cached AP samples
and sets `ap_exporter_scrape_success` to zero; the last successful timestamp
remains available. Detailed collection errors appear in application logs,
not metric labels. `/healthz` reports process liveness; `/readyz` returns HTTP
503 until a collection succeeds, and after a failed collection.

This is a CLI-format-specific example, with parser fixtures for CW9166 output.
Validate command output and metrics on the target AP model and firmware before
using it for monitoring. The metrics endpoint uses unauthenticated HTTP;
restrict network access to trusted scrapers. It has no TLS or authentication
configuration of its own.

## Build for Catalyst 9100 APs

The CW9166 target is `aarch64`, not amd64. From this directory:

```sh
sh build.sh
docker buildx build --platform linux/arm64 --load -t cisco-ap-metrics:dev .
```

Use the module's Go 1.26.11 toolchain or newer. `build.sh` builds the executable
and copies the repository Apache license, Go toolchain notices, and the
`x/crypto` and `x/sys` license/patent texts into `licenses/`; the Docker image
includes these at `/licenses`. Reassess that dependency notice list whenever
the module gains dependencies, and preserve it in redistributed IOx packages.

Package the Docker image with Linux `ioxclient` (the AP builder requires ext2
support):

```sh
ioxclient docker package -p ext2 -r 5 cisco-ap-metrics:dev ./conf
```

The Alpine rootfs includes the `/bin/sh` required by AP CAF. Keep the resulting
`conf/package.tar` below 20 MB; Cisco's total app plus extracted rootfs limit is
80 MB. The app uses the documented `c1.tiny` profile (32 MB RAM).

## AP access and provisioning

The AP must have direct SSH enabled and an enable secret configured. The
example configuration connects to `192.168.11.1:22` inside the AP's IOx network.
The AP's AP-join profile also needs to permit SSH access; no WLC CLI
credentials or WLC-side statistics configuration is required by the exporter.

Configure `package_config.ini` through CAF/ioxclient app configuration or a
protected deployment mechanism. Do not commit a populated config file or
publish credentials in the package artifact server. Pin the AP SSH host-key
fingerprint in `host_key_sha256`. Both the SSH login and enable secret are
required by the AP CLI collector. Configuration is loaded from
`CAF_APP_CONFIG_FILE`, or `/data/package_config.ini` when that variable is
unset. Invalid or missing configuration exits immediately. `poll_period`
defaults to `60s` and must be at least `30s` when set. The protected config,
package archives, and compiled binary are ignored by Git; Docker's build
context excludes configuration files. Keep credentials out of any distributable
package, and provision the configuration through an authenticated device
configuration channel before starting the application.

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

## Validation

This example is a separate Go module. Run its checks from this directory:

```sh
go test -race ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o /tmp/ap-metrics ./cmd/ap-metrics
```

The repository's opt-in `TestLiveAPMetricsExporter` deploys a package on real
hardware. Run it only against an AP reserved for the test. Replacing an
existing app requires both `CVK_AP_METRICS_REPLACE=1` and the exact
`CVK_AP_METRICS_REPLACE_APP_ID`; selecting an app by name or mapped port is
insufficient. A replacement removes that app, so first preserve its package
and configuration. `CVK_AP_METRICS_KEEP=1` leaves the test app installed for
inspection. Hardware validation remains necessary for package size, startup
within the configured memory profile, port mapping, and target CLI support.
