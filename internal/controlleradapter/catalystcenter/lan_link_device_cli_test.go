// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"strings"
	"testing"
)

func lanCLIOutputs(endpoint, peer lanLinkEndpoint) []string {
	return []string{
		"Cisco IOS XE Software\nProcessor board ID " + endpoint.device.Serial + "\n",
		fmt.Sprintf("Building configuration...\ninterface %s\n no switchport\n ip address %s %s\n ip router isis underlay\nend\n", endpoint.port.PortName, endpoint.port.IPv4, endpoint.port.Mask),
		fmt.Sprintf("System Id Type Interface IP Address State Holdtime Circuit Id\npeer L2 %s %s UP 20 peer.01\n", endpoint.port.PortName, peer.port.IPv4),
	}
}

func TestLANLinkDeviceCLIProof(t *testing.T) {
	endpoint := lanLinkEndpoint{device: Device{Serial: "FOC12345678"}, port: lanLinkInterface{PortName: "TenGigabitEthernet1/0/1", IPv4: "192.0.2.130", Mask: "255.255.255.254"}}
	peer := lanLinkEndpoint{device: Device{Serial: "FOC12345679"}, port: lanLinkInterface{PortName: "TenGigabitEthernet1/0/2", IPv4: "192.0.2.131", Mask: "255.255.255.254"}}
	for _, mode := range []string{"valid", "abbreviated", "l1-and-l2", "wrong-serial", "ambiguous-serial", "wrong-address", "secondary-address", "wrong-interface", "shutdown", "missing-isis", "duplicate-isis", "missing-end", "wrong-peer", "neighbor-down", "wrong-neighbor-port", "duplicate-neighbor", "missing-header", "unknown-format", "command-error"} {
		t.Run(mode, func(t *testing.T) {
			outputs := lanCLIOutputs(endpoint, peer)
			switch mode {
			case "abbreviated":
				outputs[2] = strings.ReplaceAll(outputs[2], endpoint.port.PortName, "Te1/0/1")
			case "l1-and-l2":
				outputs[2] += strings.Replace(strings.Split(outputs[2], "\n")[1], " L2 ", " L1 ", 1) + "\n"
			case "wrong-serial":
				outputs[0] = strings.ReplaceAll(outputs[0], endpoint.device.Serial, peer.device.Serial)
			case "ambiguous-serial":
				outputs[0] += "Processor board ID " + endpoint.device.Serial + "\n"
			case "wrong-address":
				outputs[1] = strings.ReplaceAll(outputs[1], endpoint.port.IPv4, "192.0.2.132")
			case "secondary-address":
				outputs[1] = strings.ReplaceAll(outputs[1], "end\n", " ip address 192.0.2.132 255.255.255.254 secondary\nend\n")
			case "wrong-interface":
				outputs[1] = strings.ReplaceAll(outputs[1], endpoint.port.PortName, peer.port.PortName)
			case "shutdown":
				outputs[1] = strings.ReplaceAll(outputs[1], "end\n", " shutdown\nend\n")
			case "missing-isis":
				outputs[1] = strings.ReplaceAll(outputs[1], " ip router isis underlay\n", "")
			case "duplicate-isis":
				outputs[1] = strings.ReplaceAll(outputs[1], "end\n", " ip router isis other\nend\n")
			case "missing-end":
				outputs[1] = strings.ReplaceAll(outputs[1], "end\n", "")
			case "wrong-peer":
				outputs[2] = strings.ReplaceAll(outputs[2], peer.port.IPv4, "192.0.2.133")
			case "neighbor-down":
				outputs[2] = strings.ReplaceAll(outputs[2], " UP ", " INIT ")
			case "wrong-neighbor-port":
				outputs[2] = strings.ReplaceAll(outputs[2], endpoint.port.PortName, "Te1/0/11")
			case "duplicate-neighbor":
				outputs[2] += strings.Split(outputs[2], "\n")[1] + "\n"
			case "missing-header":
				outputs[2] = strings.Split(outputs[2], "\n")[1]
			case "unknown-format":
				outputs[2] += "Unrecognized output\n"
			case "command-error":
				outputs[1] += "% Invalid input detected\n"
			}
			err := verifyLANLinkCLI(endpoint, peer, outputs)
			if (err == nil) != (mode == "valid" || mode == "abbreviated" || mode == "l1-and-l2") {
				t.Fatal("unexpected independent CLI result", err)
			}
		})
	}
}
