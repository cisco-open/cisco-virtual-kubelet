// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strconv"
	"strings"
)

func lanLinkDeleteProofCommands(port string) []string {
	return []string{"show version", "show running-config interface " + port}
}

// Bounded IOS-XE deconfiguration profile. An explicitly unaddressed interface
// and a complete exact interface block are mandatory. Unknown commands/layouts
// hold rather than infer cleanup from missing lines or an empty neighbor table.
// Hardware output qualification is separate from this strict parser contract.
func verifyLANLinkDeleteCLI(endpoint, _ lanLinkEndpoint, outputs []string) error {
	if len(outputs) != 2 {
		return errInvalidResponse
	}
	if err := verifyLANChassisSerial(endpoint.device.Serial, outputs[0]); err != nil {
		return err
	}
	inside, ended := false, false
	interfaces, unaddressed, ends, headers := 0, 0, 0, 0
	for _, raw := range strings.Split(outputs[1], "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if ended {
			return errInvalidResponse
		}
		if strings.HasPrefix(line, "interface ") {
			interfaces++
			if inside || interfaces != 1 || line != "interface "+endpoint.port.PortName {
				return errConfigurationStale
			}
			inside = true
			continue
		}
		if line == "end" {
			if !inside {
				return errInvalidResponse
			}
			ends++
			inside = false
			ended = true
			continue
		}
		if !inside {
			if line == "Building configuration..." || line == "!" {
				continue
			}
			if strings.HasPrefix(line, "Current configuration : ") {
				f := strings.Fields(line)
				if len(f) != 5 || strings.Join(f[:3], " ") != "Current configuration :" || f[4] != "bytes" {
					return errInvalidResponse
				}
				size, err := strconv.Atoi(f[3])
				if err != nil || size <= 0 || size > 65536 {
					return errInvalidResponse
				}
				headers++
				if headers != 1 {
					return errInvalidResponse
				}
				continue
			}
			return errInvalidResponse
		}
		switch line {
		case "no ip address":
			unaddressed++
		case "!", "shutdown", "no shutdown", "no switchport", "switchport", "negotiation auto", "duplex auto", "speed auto", "no ip router isis", "no ipv6 router isis", "no ipv6 address":
		default:
			if strings.HasPrefix(line, "description ") && len(line) <= 256 {
				continue
			}
			return errConfigurationBusy
		}
	}
	if interfaces != 1 || unaddressed != 1 || ends != 1 {
		return errConfigurationBusy
	}
	return nil
}
