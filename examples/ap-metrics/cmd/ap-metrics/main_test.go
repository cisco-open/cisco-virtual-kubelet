package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseOutput(t *testing.T) {
	output := `CW9166-3-test#show processes cpu 0
CPU:   0% usr   6% sys   0% nic  84% idle   0% io   0% irq   9% sirq
CW9166-3-test#show memory
MemTotal:        1659372 kB
MemAvailable:     466992 kB
CW9166-3-test#show dot11 clients
Total dot11 clients: 3
CW9166-3-test#show controllers dot11Radio 0 frequency
RX packets:42 errors:0
TX packets:24 errors:0
Serving Frequency: 2437Mhz Channel: 6 20MHz
CW9166-3-test#show controllers dot11Radio 1 frequency
RX packets:52 errors:0
TX packets:34 errors:0
Serving Frequency: 5280Mhz Channel: 56 160MHz (DFS enabled)
CW9166-3-test#show controllers dot11Radio 2 frequency
Serving Frequency: 5975Mhz Channel: 5 40MHz Below
`
	metrics := parseOutput("CW9166-3-test", output)
	got := map[string]float64{}
	for _, metric := range metrics {
		key := metric.name
		if band := metric.labels["band"]; band != "" {
			key += "/" + band + "/" + metric.labels["counter"]
		}
		got[key] = metric.value
	}
	for key, want := range map[string]float64{
		"cisco_ap_cpu_usage_percent":              16,
		"cisco_ap_memory_usage_percent":           71.86,
		"cisco_ap_clients_associated":             3,
		"cisco_ap_radio_channel/2.4ghz/":          6,
		"cisco_ap_radio_channel/5ghz/":            56,
		"cisco_ap_radio_channel/6ghz/":            5,
		"cisco_ap_radio_rx_packets_total/2.4ghz/": 42,
		"cisco_ap_radio_tx_packets_total/5ghz/":   34,
	} {
		if value, found := got[key]; !found || abs(value-want) > 0.02 {
			t.Errorf("%s = %v (found %v), want %v", key, value, found, want)
		}
	}
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func TestWriteMetrics(t *testing.T) {
	state := &snapshot{
		metrics:    []sample{{name: "cisco_ap_cpu_usage_percent", labels: map[string]string{"ap": "ap-1"}, value: 5}},
		success:    true,
		lastScrape: time.Unix(123, 0),
	}
	var output bytes.Buffer
	writeMetrics(&output, state)
	for _, expected := range []string{
		"ap_exporter_scrape_success 1",
		"ap_exporter_last_scrape_timestamp_seconds 123",
		`cisco_ap_cpu_usage_percent{ap="ap-1"} 5`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("metrics output missing %q", expected)
		}
	}
}
