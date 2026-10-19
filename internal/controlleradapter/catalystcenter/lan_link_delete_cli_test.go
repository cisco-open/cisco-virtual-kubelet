// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestLANLinkDeleteCLIRequiresCompleteDeconfiguration(t *testing.T) {
	endpoint := lanLinkEndpoint{device: Device{Serial: "FOC12345678"}, port: lanLinkInterface{PortName: "TenGigabitEthernet1/0/1"}}
	for _, mode := range []string{"valid", "up-port", "description", "empty", "missing-no-address", "remaining-address", "remaining-isis", "remaining-ipv6-isis", "other-interface", "multiple-interfaces", "missing-end", "extra-end", "after-end", "unsupported", "error", "duplicate-no-address", "wrong-serial", "duplicate-serial", "bad-header", "no-interface", "outside-no-address"} {
		t.Run(mode, func(t *testing.T) {
			outputs := lanDeleteCLIOutputs(endpoint)
			switch mode {
			case "up-port":
				outputs[1] = strings.ReplaceAll(outputs[1], " shutdown\n", " no shutdown\n")
			case "description":
				outputs[1] = strings.ReplaceAll(outputs[1], " no switchport\n", " description former automated link\n no switchport\n")
			case "empty":
				outputs[1] = ""
			case "missing-no-address":
				outputs[1] = strings.ReplaceAll(outputs[1], " no ip address\n", "")
			case "remaining-address":
				outputs[1] = strings.ReplaceAll(outputs[1], " no ip address", " ip address 192.0.2.130 255.255.255.254")
			case "remaining-isis":
				outputs[1] = strings.ReplaceAll(outputs[1], "end\n", " ip router isis UNDERLAY\nend\n")
			case "remaining-ipv6-isis":
				outputs[1] = strings.ReplaceAll(outputs[1], "end\n", " ipv6 router isis UNDERLAY\nend\n")
			case "other-interface":
				outputs[1] = strings.ReplaceAll(outputs[1], endpoint.port.PortName, "TenGigabitEthernet1/0/2")
			case "multiple-interfaces":
				outputs[1] = strings.ReplaceAll(outputs[1], "end\n", "interface TenGigabitEthernet1/0/2\nend\n")
			case "missing-end":
				outputs[1] = strings.TrimSuffix(outputs[1], "end\n")
			case "extra-end":
				outputs[1] += "end\n"
			case "after-end":
				outputs[1] += " ip address 192.0.2.130 255.255.255.254\n"
			case "unsupported":
				outputs[1] = strings.ReplaceAll(outputs[1], "end\n", " ip unnumbered Loopback0\nend\n")
			case "error":
				outputs[1] = "% Invalid input detected\n"
			case "duplicate-no-address":
				outputs[1] = strings.ReplaceAll(outputs[1], " no ip address\n", " no ip address\n no ip address\n")
			case "wrong-serial":
				outputs[0] = "Processor board ID FOC99999999\n"
			case "duplicate-serial":
				outputs[0] += outputs[0]
			case "bad-header":
				outputs[1] = strings.ReplaceAll(outputs[1], "100 bytes", "N/A bytes")
			case "no-interface":
				outputs[1] = "Building configuration...\n!\n no ip address\nend\n"
			case "outside-no-address":
				outputs[1] = "no ip address\n" + outputs[1]
			}
			err := verifyLANLinkDeleteCLI(endpoint, lanLinkEndpoint{}, outputs)
			success := mode == "valid" || mode == "up-port" || mode == "description"
			if (err == nil) != success {
				t.Fatal("unexpected deconfiguration proof", err)
			}
		})
	}
}
