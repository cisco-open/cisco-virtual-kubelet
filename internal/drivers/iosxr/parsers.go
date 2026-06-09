// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package iosxr

import (
	"regexp"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

func parseShowVersion(out string) *common.DeviceInfo {
	info := &common.DeviceInfo{}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if info.SoftwareVersion == "" {
			if m := regexp.MustCompile(`(?i)version\s+([0-9][^\s,]+)`).FindStringSubmatch(trimmed); len(m) > 1 {
				info.SoftwareVersion = m[1]
			}
		}
		if info.ProductID == "" && strings.Contains(lower, "cisco") && strings.Contains(lower, "processor") {
			info.ProductID = trimmed
		}
		if info.SerialNumber == "" {
			if m := regexp.MustCompile(`(?i)(?:processor board id|serial(?: number)?)\s*:?\s*([A-Za-z0-9_-]+)`).FindStringSubmatch(trimmed); len(m) > 1 {
				info.SerialNumber = m[1]
			}
		}
	}
	info.Hostname = parseHostname(out)
	return info
}

func parseHostname(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "hostname ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "hostname "))
		}
		if m := regexp.MustCompile(`(?m)(?:RP/\d+/RP\d+/CPU\d+:)?([A-Za-z0-9_.:-]+)(?:\([^)]+\))?[#>]`).FindStringSubmatch(line); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

func enrichPlatform(info *common.DeviceInfo, out string) {
	if info.ProductID != "" {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.HasPrefix(fields[0], "0/") {
			info.ProductID = strings.Join(fields[2:], " ")
			return
		}
	}
}

func parseSourceTable(out string) map[string]bool {
	sources := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		if fields[0] == "Sno" || strings.HasPrefix(fields[0], "-") {
			continue
		}
		if regexp.MustCompile(`^\d+$`).MatchString(fields[0]) {
			sources[fields[1]] = true
		}
	}
	return sources
}

func parseApplicationTable(out string) []iosxrApp {
	var apps []iosxrApp
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "Name ") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 3 || fields[1] != "Docker" {
			continue
		}
		app := iosxrApp{
			ID:          fields[0],
			Type:        fields[1],
			ConfigState: fields[2],
		}
		if len(fields) > 3 {
			app.Status = strings.Join(fields[3:], " ")
		}
		apps = append(apps, app)
	}
	return apps
}

func parseApplicationDetail(out string) iosxrApp {
	app := iosxrApp{}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		key, value, ok := splitColon(trimmed)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "application":
			app.ID = value
		case "type":
			app.Type = value
		case "source":
			app.Source = value
		case "config state":
			app.ConfigState = value
		case "container id":
			app.ContainerID = value
		case "container name":
			app.ContainerName = value
		case "image":
			app.Image = value
		case "status":
			app.Status = value
		case "networks":
			app.Network = value
		case "labels":
			app.RunOpts = append(app.RunOpts, labelsToRunOpts(value)...)
		}
	}
	if app.Image == "" && app.Source != "" {
		app.Image = app.Source
	}
	return app
}

func splitColon(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx == -1 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

func labelsToRunOpts(labels string) []string {
	if labels == "" {
		return nil
	}
	var opts []string
	for _, label := range strings.Split(labels, ",") {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		opts = append(opts, "--label "+label)
	}
	return opts
}

func parseXRInterfaceIPs(out string) []common.InterfaceIP {
	var ifaces []common.InterfaceIP
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 4 || fields[0] == "Interface" || strings.HasPrefix(fields[0], "-") {
			continue
		}
		status := strings.ToLower(fields[2] + "/" + fields[3])
		ip := fields[1]
		if strings.EqualFold(ip, "unassigned") {
			ip = ""
		}
		ifaces = append(ifaces, common.InterfaceIP{
			Interface: fields[0],
			IPv4:      ip,
			Status:    status,
		})
	}
	return ifaces
}
