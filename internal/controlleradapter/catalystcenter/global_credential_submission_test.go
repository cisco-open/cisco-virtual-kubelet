// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestGlobalCredentialSubmissionRechecksSelectedDefinitions(t *testing.T) {
	for _, mode := range []string{"create-race", "later-selection-race", "replace", "metadata-drift", "disappear", "late-race", "unrelated", "stable"} {
		t.Run(mode, func(t *testing.T) {
			want, snapshot := globalCredentialFixture(t, "cli_credentials")
			later := want
			later.metadata.Name = "later"
			update := mode == "replace" || mode == "metadata-drift" || mode == "disappear"
			var current []globalCredentialMetadata
			var receipts []ciskov1.NetworkControllerConfigurationCredentialApplication
			if update {
				got := want.metadata
				got.ID, got.Username = "owned-id", "old-username"
				current = append(current, got)
				_, versions, err := want.payload(snapshot, "owner", got.ID)
				if err != nil {
					t.Fatal(err)
				}
				receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: "network_settings", Resource: want.resource(), ConfigUID: "owner", TargetID: got.ID, OperationID: "0123456789abcdef0123456789abcdef", Versions: versions}}
			}
			reads, writes := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != globalCredentialsPath {
					t.Error("unexpected endpoint")
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodGet {
					reads++
					if mode == "late-race" && reads == 3 {
						got := want.metadata
						got.ID = "foreign-id"
						current = append(current, got)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": current}})
					return
				}
				writes++
				fmt.Fprint(w, `{"response":{"taskId":"credential-task"}}`)
			})
			e := &globalCredentialExecutor{client: c, desired: []globalCredentialIntent{want, later}, secrets: snapshot, owner: "owner", receipts: receipts}
			write, err := e.next(context.Background())
			if err != nil || write == nil {
				t.Fatal("planning failed", err)
			}
			switch mode {
			case "create-race", "later-selection-race", "unrelated":
				got := want.metadata
				got.ID = "foreign-id"
				if mode == "later-selection-race" {
					got.Name = "later"
				} else if mode == "unrelated" {
					got.Name = "unrelated"
				}
				current = append(current, got)
			case "replace":
				current[0].ID = "replacement-id"
			case "metadata-drift":
				current[0].Username = "changed-after-planning"
			case "disappear":
				current = nil
			}
			_, err = write.submit(context.Background())
			allowed := mode == "unrelated" || mode == "stable"
			if allowed && (err != nil || writes != 1 || reads != 3) {
				t.Fatal("stable selection failed", err, writes, reads)
			}
			if !allowed && (err == nil || writes != 0) {
				t.Fatal("changed selection was submitted", writes)
			}
			beforeReads, beforeWrites := reads, writes
			if _, err = write.submit(context.Background()); err == nil || reads != beforeReads || writes != beforeWrites {
				t.Fatal("submission closure was reused")
			}
		})
	}
}

func TestGlobalCredentialSubmissionDoesNotReplayLostAcknowledgement(t *testing.T) {
	want, snapshot := globalCredentialFixture(t, "cli_credentials")
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"response":{}}`)
			return
		}
		writes++
		// The appliance may have accepted the operation, but its acknowledgement
		// is unusable. The same closure must never issue the request again.
		fmt.Fprint(w, `{"response":{}}`)
	})
	write, err := c.planGlobalCredentialCreate(context.Background(), want, snapshot, "owner")
	if err != nil || write == nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := write.submit(context.Background()); err == nil {
			t.Fatal("missing task acknowledgement accepted")
		}
	}
	if writes != 1 {
		t.Fatal("uncertain write replayed", writes)
	}
}
