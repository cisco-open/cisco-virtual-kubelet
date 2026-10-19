// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestWiredBulkNetworkCLIRequiresExactScopedIntent(t *testing.T) {
	for _, mode := range []string{"match", "split-dns", "domain-alias", "canonical-ipv6", "ntp-hostname", "wrong-ntp", "extra-ntp", "duplicate-ntp", "ntp-options", "wrong-dns", "dns-order", "extra-dns", "duplicate-dns", "dns-vrf", "wrong-domain", "extra-domain", "dns-option", "cli-error", "missing-output", "unsupported-kind", "duplicate-kind"} {
		t.Run(mode, func(t *testing.T) {
			wants := []networkSetting{{kind: "ntp", servers: []string{"192.0.2.1"}}, {kind: "dns", domain: "example.test", servers: []string{"192.0.2.53", "192.0.2.54"}}}
			outputs := []string{"Processor board ID SERIAL\n", "ntp server 192.0.2.1\n", "ip name-server 192.0.2.53 192.0.2.54\n", "ip domain name example.test\n"}
			switch mode {
			case "split-dns":
				outputs[2] = "ip name-server 192.0.2.53\nip name-server 192.0.2.54\n"
			case "domain-alias":
				outputs[3] = "ip domain-name example.test\n"
			case "canonical-ipv6":
				wants[0].servers = []string{"2001:db8::1"}
				outputs[1] = "ntp server 2001:0db8:0000::1\n"
			case "ntp-hostname":
				wants[0].servers = []string{"ntp.example.test"}
				outputs[1] = "ntp server NTP.EXAMPLE.TEST.\n"
			case "wrong-ntp":
				outputs[1] = "ntp server 192.0.2.2\n"
			case "extra-ntp":
				outputs[1] += "ntp server 192.0.2.2\n"
			case "duplicate-ntp":
				outputs[1] += outputs[1]
			case "ntp-options":
				outputs[1] = "ntp server 192.0.2.1 prefer\n"
			case "wrong-dns":
				outputs[2] = "ip name-server 192.0.2.55 192.0.2.54\n"
			case "dns-order":
				outputs[2] = "ip name-server 192.0.2.54 192.0.2.53\n"
			case "extra-dns":
				outputs[2] += "ip name-server 192.0.2.55\n"
			case "duplicate-dns":
				outputs[2] += "ip name-server 192.0.2.53\n"
			case "dns-vrf":
				outputs[2] = "ip name-server vrf Mgmt 192.0.2.53 192.0.2.54\n"
			case "wrong-domain":
				outputs[3] = "ip domain name other.test\n"
			case "extra-domain":
				outputs[3] += outputs[3]
			case "dns-option":
				outputs[3] += "ip domain lookup source-interface Loopback0\n"
			case "cli-error":
				outputs[1] = "% Invalid input detected\n"
			case "missing-output":
				outputs = outputs[:3]
			case "unsupported-kind":
				wants = append(wants, networkSetting{kind: "timezone", timezone: "Europe/Berlin"})
			case "duplicate-kind":
				wants = append(wants, wants[0])
			}
			err := verifyWiredBulkNetworkCLI(wants, outputs)
			valid := mode == "match" || mode == "split-dns" || mode == "domain-alias" || mode == "canonical-ipv6" || mode == "ntp-hostname"
			if (err == nil) != valid {
				t.Fatal("device observation silently widened or lost intent", err)
			}
			if valid {
				commands, err := wiredBulkNetworkCommands(wants)
				if err != nil || len(commands) != len(outputs) || commands[0] != "show version" {
					t.Fatal("invalid bounded worker queries", err)
				}
				for _, command := range commands {
					if !strings.HasPrefix(command, "show ") {
						t.Fatal("non-read query")
					}
				}
			}
		})
	}
}
