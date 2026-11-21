// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0.

package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultConfigPath = "/data/package_config.ini"
	defaultListenAddr = ":9100"
	defaultPollPeriod = 60 * time.Second
)

var (
	promptLine   = regexp.MustCompile(`^[[:alnum:]_.-]+[>#]$`)
	cpuIdle      = regexp.MustCompile(`(?i)\b([0-9]+)%\s+idle\b`)
	memoryField  = regexp.MustCompile(`(?m)^\s*(MemTotal|MemAvailable):\s*([0-9]+)\s+kB\s*$`)
	clientTotal  = regexp.MustCompile(`(?im)^\s*Total dot11 clients:\s*([0-9]+)\s*$`)
	servingRadio = regexp.MustCompile(`(?im)^Serving Frequency:\s*([0-9]+)\s*Mhz\s+Channel:\s*([0-9]+)\s+([0-9]+)\s*MHz`)
	rxPackets    = regexp.MustCompile(`(?i)RX packets:\s*([0-9]+)`)
	txPackets    = regexp.MustCompile(`(?i)TX packets:\s*([0-9]+)`)
)

var metricMetadataMap = map[string][2]string{
	"cisco_ap_cpu_usage_percent":       {"Current AP CPU usage percent from the AP local CLI.", "gauge"},
	"cisco_ap_memory_usage_percent":    {"AP memory used percent, calculated from MemTotal and MemAvailable.", "gauge"},
	"cisco_ap_memory_total_bytes":      {"Total AP memory in bytes.", "gauge"},
	"cisco_ap_memory_available_bytes":  {"Available AP memory in bytes.", "gauge"},
	"cisco_ap_clients_associated":      {"Number of clients associated with the AP, reported by its local CLI.", "gauge"},
	"cisco_ap_radio_channel":           {"Serving channel reported by the AP local radio CLI.", "gauge"},
	"cisco_ap_radio_channel_width_mhz": {"Serving radio channel width in MHz.", "gauge"},
	"cisco_ap_radio_frequency_mhz":     {"Serving radio frequency in MHz.", "gauge"},
	"cisco_ap_radio_rx_packets_total":  {"Cumulative AP radio receive packets.", "counter"},
	"cisco_ap_radio_tx_packets_total":  {"Cumulative AP radio transmit packets.", "counter"},
}

type config struct {
	APAddress       string
	APUsername      string
	APPassword      string
	APEnableSecret  string
	APHostKeySHA256 string
	APName          string
	ListenAddress   string
	PollPeriod      time.Duration
}

type sample struct {
	name   string
	labels map[string]string
	value  float64
}

type snapshot struct {
	mu         sync.RWMutex
	metrics    []sample
	success    bool
	lastScrape time.Time
	lastError  string
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Printf("configuration: %v", err)
	}
	state := &snapshot{}
	go pollForever(cfg, state)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writeMetrics(w, state)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	addr := cfg.ListenAddress
	if addr == "" {
		addr = defaultListenAddr
	}
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("HTTP server: %v", err)
	}
}

func loadConfig() (config, error) {
	path := os.Getenv("CAF_APP_CONFIG_FILE")
	if path == "" {
		path = defaultConfigPath
	}
	file, err := os.Open(path)
	if err != nil {
		return config{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	var cfg config
	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section != "ap" {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "address":
			cfg.APAddress = value
		case "username":
			cfg.APUsername = value
		case "password":
			cfg.APPassword = value
		case "enable_secret":
			cfg.APEnableSecret = value
		case "host_key_sha256":
			cfg.APHostKeySHA256 = value
		case "ap_name":
			cfg.APName = value
		case "listen_address":
			cfg.ListenAddress = value
		case "poll_period":
			duration, parseErr := time.ParseDuration(value)
			if parseErr == nil && duration >= 30*time.Second {
				cfg.PollPeriod = duration
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if cfg.PollPeriod == 0 {
		cfg.PollPeriod = defaultPollPeriod
	}
	return cfg, nil
}

func pollForever(cfg config, state *snapshot) {
	period := cfg.PollPeriod
	if period == 0 {
		period = defaultPollPeriod
	}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		metrics, err := collect(ctx, cfg)
		cancel()
		state.mu.Lock()
		state.success = err == nil
		state.lastScrape = time.Now()
		if err == nil {
			state.metrics = metrics
			state.lastError = ""
		} else {
			state.lastError = err.Error()
			log.Printf("AP collection failed: %v", err)
		}
		state.mu.Unlock()
		time.Sleep(period)
	}
}

func collect(ctx context.Context, cfg config) ([]sample, error) {
	if cfg.APAddress == "" || cfg.APUsername == "" || cfg.APPassword == "" || cfg.APEnableSecret == "" || cfg.APName == "" {
		return nil, fmt.Errorf("ap.address, ap.username, ap.password, ap.enable_secret, and ap.ap_name are required")
	}
	if cfg.APHostKeySHA256 == "" {
		return nil, fmt.Errorf("ap.host_key_sha256 is required; pin the AP SSH host key")
	}
	address := cfg.APAddress
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, "22")
	}
	clientConfig := &ssh.ClientConfig{
		User:            cfg.APUsername,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.APPassword)},
		Timeout:         8 * time.Second,
		HostKeyCallback: pinHostKey(cfg.APHostKeySHA256),
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("connect to AP: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	sshConn, channels, requests, err := ssh.NewClientConn(connection, address, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH to AP: %w", err)
	}
	client := ssh.NewClient(sshConn, channels, requests)
	defer client.Close()
	output, err := runAPCLI(ctx, client, cfg.APEnableSecret, []string{
		"show processes cpu 0",
		"show memory",
		"show dot11 clients",
		"show controllers dot11Radio 0 frequency",
		"show controllers dot11Radio 1 frequency",
		"show controllers dot11Radio 2 frequency",
	})
	if err != nil {
		return nil, err
	}
	return parseOutput(cfg.APName, output), nil
}

func pinHostKey(expected string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(key)
		if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			return fmt.Errorf("AP SSH host key fingerprint mismatch (received %s)", actual)
		}
		return nil
	}
}

func runAPCLI(ctx context.Context, client *ssh.Client, enableSecret string, commands []string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("open AP CLI session: %w", err)
	}
	defer session.Close()
	if err := session.RequestPty("vt100", 1000, 200, nil); err != nil {
		return "", fmt.Errorf("request AP terminal: %w", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("open AP CLI input: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("open AP CLI output: %w", err)
	}
	if err := session.Shell(); err != nil {
		return "", fmt.Errorf("start AP CLI: %w", err)
	}
	reader := bufio.NewReader(stdout)
	if _, err := readUntilPrompt(ctx, reader); err != nil {
		return "", fmt.Errorf("read AP login prompt: %w", err)
	}
	if _, err := io.WriteString(stdin, "enable\n"); err != nil {
		return "", fmt.Errorf("request AP enable mode: %w", err)
	}
	passwordPrompt, err := readUntilPasswordOrPrompt(ctx, reader)
	if err != nil {
		return "", fmt.Errorf("read AP enable prompt: %w", err)
	}
	if !passwordPrompt {
		return "", fmt.Errorf("AP CLI did not request the enable secret")
	}
	if _, err := io.WriteString(stdin, enableSecret+"\n"); err != nil {
		return "", fmt.Errorf("send AP enable secret: %w", err)
	}
	if _, err := readUntilPrompt(ctx, reader); err != nil {
		return "", fmt.Errorf("enter AP enable mode: %w", err)
	}
	if _, err := io.WriteString(stdin, "terminal length 0\n"); err != nil {
		return "", fmt.Errorf("disable AP CLI paging: %w", err)
	}
	if _, err := readUntilPrompt(ctx, reader); err != nil {
		return "", fmt.Errorf("configure AP terminal: %w", err)
	}
	var output strings.Builder
	for _, command := range commands {
		if _, err := io.WriteString(stdin, command+"\n"); err != nil {
			return "", fmt.Errorf("write AP CLI command: %w", err)
		}
		result, err := readUntilPrompt(ctx, reader)
		if err != nil {
			return "", fmt.Errorf("read AP command %q: %w", command, err)
		}
		output.WriteString(result)
		output.WriteByte('\n')
	}
	return output.String(), nil
}

func readUntilPasswordOrPrompt(ctx context.Context, reader *bufio.Reader) (bool, error) {
	var output strings.Builder
	for {
		if strings.HasSuffix(output.String(), "Password:") {
			return true, nil
		}
		if isPromptLine(lastLine(output.String())) {
			return false, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		value, err := reader.ReadByte()
		if err != nil {
			return false, err
		}
		output.WriteByte(value)
	}
}

func readUntilPrompt(ctx context.Context, reader *bufio.Reader) (string, error) {
	var output strings.Builder
	for {
		if output.Len() > 0 && isPromptLine(lastLine(output.String())) {
			return output.String(), nil
		}
		if err := ctx.Err(); err != nil {
			return output.String(), err
		}
		value, err := reader.ReadByte()
		if err != nil {
			return output.String(), err
		}
		output.WriteByte(value)
	}
}

func lastLine(output string) string {
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func isPromptLine(line string) bool { return promptLine.MatchString(line) }

func parseOutput(apName, output string) []sample {
	labels := map[string]string{"ap": apName}
	var metrics []sample
	if match := cpuIdle.FindStringSubmatch(output); len(match) == 2 {
		if idle, err := strconv.ParseFloat(match[1], 64); err == nil {
			metrics = append(metrics, sample{name: "cisco_ap_cpu_usage_percent", labels: labels, value: 100 - idle})
		}
	}
	var total, available float64
	for _, match := range memoryField.FindAllStringSubmatch(output, -1) {
		value, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			continue
		}
		switch match[1] {
		case "MemTotal":
			total = value
		case "MemAvailable":
			available = value
		}
	}
	if total > 0 {
		metrics = append(metrics, sample{name: "cisco_ap_memory_total_bytes", labels: labels, value: total * 1024})
		metrics = append(metrics, sample{name: "cisco_ap_memory_usage_percent", labels: labels, value: (total - available) / total * 100})
		metrics = append(metrics, sample{name: "cisco_ap_memory_available_bytes", labels: labels, value: available * 1024})
	}
	if match := clientTotal.FindStringSubmatch(output); len(match) == 2 {
		if count, err := strconv.ParseFloat(match[1], 64); err == nil {
			metrics = append(metrics, sample{name: "cisco_ap_clients_associated", labels: labels, value: count})
		}
	}
	for slot, band := range []string{"2.4ghz", "5ghz", "6ghz"} {
		section := radioSection(output, slot)
		radioLabels := map[string]string{"ap": apName, "band": band, "slot": strconv.Itoa(slot)}
		if match := servingRadio.FindStringSubmatch(section); len(match) == 4 {
			frequency, _ := strconv.ParseFloat(match[1], 64)
			channel, _ := strconv.ParseFloat(match[2], 64)
			width, _ := strconv.ParseFloat(match[3], 64)
			metrics = append(metrics,
				sample{name: "cisco_ap_radio_frequency_mhz", labels: radioLabels, value: frequency},
				sample{name: "cisco_ap_radio_channel", labels: radioLabels, value: channel},
				sample{name: "cisco_ap_radio_channel_width_mhz", labels: radioLabels, value: width},
			)
		}
		if match := rxPackets.FindStringSubmatch(section); len(match) == 2 {
			if value, err := strconv.ParseFloat(match[1], 64); err == nil {
				metrics = append(metrics, sample{name: "cisco_ap_radio_rx_packets_total", labels: radioLabels, value: value})
			}
		}
		if match := txPackets.FindStringSubmatch(section); len(match) == 2 {
			if value, err := strconv.ParseFloat(match[1], 64); err == nil {
				metrics = append(metrics, sample{name: "cisco_ap_radio_tx_packets_total", labels: radioLabels, value: value})
			}
		}
	}
	return metrics
}

func radioSection(output string, slot int) string {
	start := strings.Index(output, fmt.Sprintf("show controllers dot11Radio %d frequency", slot))
	if start < 0 {
		return ""
	}
	end := len(output)
	for next := slot + 1; next < 3; next++ {
		if index := strings.Index(output[start+1:], fmt.Sprintf("show controllers dot11Radio %d frequency", next)); index >= 0 {
			end = start + 1 + index
			break
		}
	}
	return output[start:end]
}

func writeMetrics(w io.Writer, state *snapshot) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	fmt.Fprintln(w, "# HELP ap_exporter_scrape_success Whether the most recent AP local CLI scrape succeeded.")
	fmt.Fprintln(w, "# TYPE ap_exporter_scrape_success gauge")
	if state.success {
		fmt.Fprintln(w, "ap_exporter_scrape_success 1")
	} else {
		fmt.Fprintln(w, "ap_exporter_scrape_success 0")
	}
	fmt.Fprintln(w, "# HELP ap_exporter_last_scrape_timestamp_seconds Unix time of the most recent AP local CLI scrape.")
	fmt.Fprintln(w, "# TYPE ap_exporter_last_scrape_timestamp_seconds gauge")
	if !state.lastScrape.IsZero() {
		fmt.Fprintf(w, "ap_exporter_last_scrape_timestamp_seconds %.0f\n", float64(state.lastScrape.Unix()))
	}
	if state.lastError != "" {
		fmt.Fprintln(w, "# HELP ap_exporter_collection_error_info Current collection error, if any.")
		fmt.Fprintln(w, "# TYPE ap_exporter_collection_error_info gauge")
		fmt.Fprintf(w, "ap_exporter_collection_error_info{reason=%q} 1\n", state.lastError)
	}
	grouped := make(map[string][]sample)
	for _, metric := range state.metrics {
		grouped[metric.name] = append(grouped[metric.name], metric)
	}
	names := make([]string, 0, len(grouped))
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		help, kind := metricMetadataMap[name][0], metricMetadataMap[name][1]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
		for _, metric := range grouped[name] {
			fmt.Fprintf(w, "%s%s %.6g\n", metric.name, formatLabels(metric.labels), metric.value)
		}
	}
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteByte('{')
	first := true
	for _, key := range []string{"ap", "band", "slot"} {
		value, ok := labels[key]
		if !ok {
			continue
		}
		if !first {
			builder.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&builder, `%s=%q`, key, value)
	}
	builder.WriteByte('}')
	return builder.String()
}
