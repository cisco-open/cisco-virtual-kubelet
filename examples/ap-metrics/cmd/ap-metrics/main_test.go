package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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
	metrics, err := parseOutput("CW9166-3-test", output)
	if err != nil {
		t.Fatal(err)
	}
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

const coreOutput = "CPU: 84% idle\nMemTotal: 100 kB\nMemAvailable: 20 kB\nTotal dot11 clients: 0\n"

func TestParseOutputRejectsIncompleteOrInvalidCoreMetrics(t *testing.T) {
	for name, output := range map[string]string{
		"empty":               "",
		"command rejected":    "% Invalid input detected at '^' marker.\nAP#",
		"no available memory": strings.ReplaceAll(coreOutput, "MemAvailable: 20 kB\n", ""),
		"invalid memory":      strings.ReplaceAll(coreOutput, "MemAvailable: 20", "MemAvailable: 200"),
		"invalid CPU":         strings.ReplaceAll(coreOutput, "84% idle", "184% idle"),
		"no clients":          strings.ReplaceAll(coreOutput, "Total dot11 clients: 0\n", ""),
	} {
		t.Run(name, func(t *testing.T) {
			if metrics, err := parseOutput("test", output); err == nil || len(metrics) != 0 {
				t.Fatalf("parseOutput = %v, %v; want no metrics and an error", metrics, err)
			}
		})
	}
}

func TestParseOutputFlexibleRadiosCRLFAndCommandEcho(t *testing.T) {
	output := coreOutput + `show controllers dot11Radio 2 frequency
AP#show controllers dot11Radio 2 frequency
RX packets: 1234567890123
Serving Frequency: 5180Mhz Channel: 36 80MHz
show controllers dot11Radio 0 frequency
AP#show controllers dot11Radio 0 frequency
Serving Frequency: 5975Mhz Channel: 5 40MHz
show controllers dot11Radio 1 frequency
% Invalid interface
`
	metrics, err := parseOutput("test", strings.ReplaceAll(output, "\n", "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, metric := range metrics {
		if metric.name == "cisco_ap_radio_frequency_mhz" {
			got[metric.labels["slot"]] = metric.labels["band"]
		}
		if metric.name == "cisco_ap_radio_rx_packets_total" && (metric.labels["slot"] != "2" || metric.value != 1234567890123) {
			t.Fatalf("counter attributed to wrong radio: %+v", metric)
		}
	}
	if len(got) != 2 || got["0"] != "6ghz" || got["2"] != "5ghz" {
		t.Fatalf("radio bands = %v", got)
	}
}

func TestSnapshotFailureClearsStaleMetrics(t *testing.T) {
	state := &snapshot{}
	state.update([]sample{{name: "cisco_ap_cpu_usage_percent", value: 5}}, nil, time.Unix(100, 0))
	state.update(nil, errors.New("remote error containing sensitive details"), time.Unix(200, 0))
	var output bytes.Buffer
	writeMetrics(&output, state)
	text := output.String()
	for _, want := range []string{"ap_exporter_scrape_success 0", "ap_exporter_last_scrape_timestamp_seconds 200", "ap_exporter_last_success_timestamp_seconds 100"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	for _, unwanted := range []string{"cisco_ap_cpu", "sensitive", "collection_error_info"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("unexpected %q in %s", unwanted, text)
		}
	}
}

func TestMetricsPreserveCounterPrecisionAndEscapeLabels(t *testing.T) {
	state := &snapshot{metrics: []sample{{name: "cisco_ap_radio_rx_packets_total", labels: map[string]string{"ap": "AP\twith\"quotes\\and\nnewline"}, value: 1234567890123}}}
	var output bytes.Buffer
	writeMetrics(&output, state)
	want := "cisco_ap_radio_rx_packets_total{ap=\"AP\twith\\\"quotes\\\\and\\nnewline\"} 1.234567890123e+12"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("missing exact sample %q in %s", want, output.String())
	}
}

func TestReadCLI(t *testing.T) {
	for _, tt := range []struct {
		name, input             string
		password, allowPassword bool
		wantErr                 bool
	}{
		{name: "prompt without newline", input: "result\r\nAP-1#"},
		{name: "user prompt", input: "AP-1>"},
		{name: "password", input: "enable\r\nPassword:", allowPassword: true, password: true},
		{name: "lowercase password", input: "password:", allowPassword: true, password: true},
		{name: "truncated", input: "result without prompt", wantErr: true},
		{name: "bounded response", input: strings.Repeat("x", maxCLIOutput) + "\nAP#", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, password, err := readCLI(context.Background(), bufio.NewReader(strings.NewReader(tt.input)), tt.allowPassword)
			if (err != nil) != tt.wantErr || password != tt.password {
				t.Fatalf("password=%v error=%v", password, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readUntilPrompt(ctx, bufio.NewReader(strings.NewReader("AP#"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %v", err)
	}
}

func TestHostKeyPin(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	if err := pinHostKey(ssh.FingerprintSHA256(key))("ap", nil, key); err != nil {
		t.Fatalf("pinned key rejected: %v", err)
	}
	if err := pinHostKey("SHA256:wrong")("ap", nil, key); err == nil {
		t.Fatal("wrong host key accepted")
	}
}

func TestLoadConfigValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.ini")
	t.Setenv("CAF_APP_CONFIG_FILE", path)
	valid := "[ap]\naddress=192.0.2.1\nusername=user\npassword=secret\nenable_secret=secret\nap_name=test\nhost_key_sha256=SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"
	for _, tt := range []struct {
		name, content string
		wantErr       bool
	}{
		{"defaults", valid, false},
		{"invalid poll", valid + "poll_period=soon\n", true},
		{"short poll", valid + "poll_period=1s\n", true},
		{"missing credential", strings.ReplaceAll(valid, "password=secret\n", ""), true},
		{"invalid pin", strings.ReplaceAll(valid, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "insecure"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfig()
			if (err != nil) != tt.wantErr {
				t.Fatalf("loadConfig error = %v", err)
			}
			if !tt.wantErr && (cfg.PollPeriod != defaultPollPeriod || cfg.ListenAddress != defaultListenAddr) {
				t.Fatalf("defaults = %+v", cfg)
			}
		})
	}
}

func TestHTTPReadinessAndLiveness(t *testing.T) {
	state := &snapshot{}
	handler := metricsHandler(state)
	for _, tt := range []struct {
		path   string
		status int
	}{{"/healthz", http.StatusOK}, {"/readyz", http.StatusServiceUnavailable}, {"/metrics", http.StatusOK}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if response.Code != tt.status {
			t.Errorf("%s status = %d", tt.path, response.Code)
		}
	}
	state.update(nil, nil, time.Now())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Errorf("ready status = %d", response.Code)
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
