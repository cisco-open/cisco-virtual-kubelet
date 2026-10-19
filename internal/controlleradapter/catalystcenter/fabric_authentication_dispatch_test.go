// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"testing"
)

func TestFabricAuthenticationRetainedGroupDispatch(t *testing.T) {
	for _, mode := range []string{"accepted", "denied", "missing-permit", "lost-response", "revoked-after-write", "baseline-changed", "member-changed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, f, members := fabricAuthManagedFixture(t)
			if _, err := a.prepareFabricAuthentication(ctx, config, source, "Global/Lab"); err != nil {
				t.Fatal(err)
			}
			permit, revoke := configurationGroupDispatchFixture(t, a, members, true)
			switch mode {
			case "denied":
				revoke()
			case "missing-permit":
				permit = nil
			case "lost-response":
				f.lostAck = true
			case "revoked-after-write":
				f.afterWrite = revoke
			case "baseline-changed":
				v := 20
				f.profile.Timeout = &v
			case "member-changed":
				f.memberships["zone-id"] = []fabricDeviceMembership{}
			}
			task, err := a.dispatchFabricAuthentication(ctx, config, source, "Global/Lab", permit)
			state := journalState(t, a).Operation
			if mode == "accepted" {
				if err != nil || task.ID != "fabric-auth-task" || state.Phase != "Accepted" || state.TaskID != task.ID || f.writes != 1 {
					t.Fatal("accepted task not retained", err, task, state.Phase)
				}
			} else if mode == "lost-response" || mode == "revoked-after-write" {
				if err == nil || f.writes != 1 || state.Phase != "Submitting" || state.TaskID != "" {
					t.Fatal("uncertain dispatch lost its fence", err, state.Phase, f.writes)
				}
				if mode == "revoked-after-write" && task.ID != "fabric-auth-task" {
					t.Fatal("known task discarded after receipt failure")
				}
			} else if err == nil || f.writes != 0 || state.Phase != "Prepared" || state.SubmittedAt != nil {
				t.Fatal("unadmitted or stale dispatch submitted", err, state.Phase, f.writes)
			}
			writes := f.writes
			restarted := *a
			if _, err := restarted.dispatchFabricAuthentication(ctx, config, source, "Global/Lab", permit); err == nil || f.writes != writes {
				t.Fatal("restarted dispatch replayed or ignored denial")
			}
		})
	}
}
