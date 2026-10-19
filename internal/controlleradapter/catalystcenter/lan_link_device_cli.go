// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"strconv"
	"strings"
)

func lanLinkProofCommands(port string) []string {
	return []string{"show version", "show running-config interface " + port, "show isis neighbors"}
}

// This is a deliberately bounded IOS-XE CLI profile. Unknown output is not
// convergence. DeviceOperation owns execution, redaction and output limits.
func verifyLANLinkCLI(endpoint, peer lanLinkEndpoint, outputs []string) error {
	if len(outputs) != 3 {
		return errInvalidResponse
	}
	if err := verifyLANChassisSerial(endpoint.device.Serial, outputs[0]); err != nil {
		return err
	}

	interfaces, addresses, routing, ends := 0, 0, 0, 0
	inside := false
	for _, line := range strings.Split(outputs[1], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "interface ") {
			interfaces++
			if line != "interface "+endpoint.port.PortName || interfaces != 1 || ends != 0 {
				return errConfigurationStale
			}
			inside = true
			continue
		}
		if line == "end" {
			ends++
			inside = false
			continue
		}
		if line == "shutdown" || strings.HasPrefix(line, "%") {
			return errConfigurationBusy
		}
		if strings.HasPrefix(line, "ip address ") {
			if !inside || line != fmt.Sprintf("ip address %s %s", endpoint.port.IPv4, endpoint.port.Mask) {
				return errConfigurationStale
			}
			addresses++
		}
		if strings.HasPrefix(line, "ip router isis") {
			fields := strings.Fields(line)
			if !inside || len(fields) < 3 || len(fields) > 4 || strings.Join(fields[:3], " ") != "ip router isis" {
				return errInvalidResponse
			}
			routing++
		}
	}
	if interfaces != 1 || addresses != 1 || routing != 1 || ends != 1 {
		return errConfigurationBusy
	}

	// Cisco's IOS-XE table identifies the local interface and neighbor IP. Both
	// endpoints are checked reciprocally against the independently read addresses.
	header := false
	levels := map[string]bool{}
	for _, line := range strings.Split(outputs[2], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Join(strings.Fields(line), " ") == "System Id Type Interface IP Address State Holdtime Circuit Id" {
			if header {
				return errInvalidResponse
			}
			header = true
			continue
		}
		fields := strings.Fields(line)
		if !header || len(fields) != 7 {
			return errInvalidResponse
		}
		if !lanCLIInterfaceMatches(endpoint.port.PortName, fields[2]) {
			continue
		}
		if (fields[1] != "L1" && fields[1] != "L2" && fields[1] != "L1L2") || fields[3] != peer.port.IPv4 || fields[4] != "UP" || levels[fields[1]] {
			return errConfigurationStale
		}
		hold, err := strconv.Atoi(fields[5])
		if err != nil || hold <= 0 || hold > 65535 {
			return errInvalidResponse
		}
		levels[fields[1]] = true
	}
	if !header || len(levels) == 0 {
		return errConfigurationBusy
	}
	return nil
}

func lanCLIInterfaceMatches(full, observed string) bool {
	if full == observed {
		return true
	}
	// Exact platform spellings only; generic prefix matching would confuse ports.
	for name, abbreviation := range map[string]string{"GigabitEthernet": "Gi", "TenGigabitEthernet": "Te", "TwentyFiveGigE": "Twe", "FortyGigabitEthernet": "Fo", "HundredGigE": "Hu"} {
		if strings.HasPrefix(full, name) && observed == abbreviation+strings.TrimPrefix(full, name) {
			return true
		}
	}
	return false
}

// Retain the LAN helper name while sharing chassis evidence across CC actions.
func verifyLANChassisSerial(serial, output string) error {
	return verifyConfigurationChassisSerial(serial, output)
}
