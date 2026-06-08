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

package ftd

import (
	"regexp"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

type ftdNetworkInfo struct {
	Hostname       string
	ManagementPort string
	Interfaces     []ftdInterface
}

type ftdInterface struct {
	Name      string
	IPv4      string
	State     string
	LinkState string
}

var (
	ftdHeaderRe       = regexp.MustCompile(`\[\s*([^\]]+?)\s*\]`)
	ftdModelRe        = regexp.MustCompile(`(?i)model\s*:\s*(.+?)(?:\s+Version\s+|$)`)
	ftdVersionRe      = regexp.MustCompile(`(?i)\bVersion\s+([0-9][0-9A-Za-z._-]*)(?:\s+\(Build\s+([^)]+)\))?`)
	ftdUUIDRe         = regexp.MustCompile(`(?i)^UUID\s*:\s*(\S+)`)
	ftdInterfaceHdrRe = regexp.MustCompile(`=+\[\s*([^\]]+?)\s*\]=+`)
)

func parseShowVersion(out string) *common.DeviceInfo {
	info := &common.DeviceInfo{}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if info.Hostname == "" {
			if m := ftdHeaderRe.FindStringSubmatch(trimmed); len(m) > 1 {
				info.Hostname = strings.TrimSpace(m[1])
			}
		}
		if info.ProductID == "" {
			if m := ftdModelRe.FindStringSubmatch(trimmed); len(m) > 1 {
				info.ProductID = strings.TrimSpace(m[1])
			}
		}
		if info.SoftwareVersion == "" {
			if m := ftdVersionRe.FindStringSubmatch(trimmed); len(m) > 1 {
				info.SoftwareVersion = strings.TrimSpace(m[1])
				if len(m) > 2 && strings.TrimSpace(m[2]) != "" {
					info.SoftwareVersion += "-" + strings.TrimSpace(m[2])
				}
			}
		}
		if info.SerialNumber == "" {
			if m := ftdUUIDRe.FindStringSubmatch(trimmed); len(m) > 1 {
				info.SerialNumber = strings.TrimSpace(m[1])
			}
		}
	}
	if info.ProductID == "" {
		info.ProductID = "Cisco Secure Firewall Threat Defense"
	}
	return info
}

func parseShowNetwork(out string) ftdNetworkInfo {
	info := ftdNetworkInfo{}
	var current *ftdInterface
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if m := ftdInterfaceHdrRe.FindStringSubmatch(trimmed); len(m) > 1 {
			name := strings.TrimSpace(m[1])
			if current != nil {
				info.Interfaces = append(info.Interfaces, *current)
			}
			if strings.EqualFold(name, "System Information") {
				current = nil
				continue
			}
			current = &ftdInterface{Name: name}
			continue
		}
		key, value, ok := splitColon(trimmed)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "hostname":
			info.Hostname = value
		case "management port":
			info.ManagementPort = value
		case "ipv4 address":
			if current != nil {
				current.IPv4 = value
			}
		case "state":
			if current != nil {
				current.State = value
			}
		case "link state":
			if current != nil {
				current.LinkState = value
			}
		}
	}
	if current != nil {
		info.Interfaces = append(info.Interfaces, *current)
	}
	return info
}

func splitColon(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

func ftdInterfaceStatus(intf ftdInterface) string {
	link := strings.ToLower(strings.TrimSpace(intf.LinkState))
	state := strings.ToLower(strings.TrimSpace(intf.State))
	if link == "up" && (state == "" || state == "enabled") {
		return "up"
	}
	if link == "up" {
		return "up"
	}
	return "down"
}
