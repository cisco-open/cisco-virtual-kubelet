// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestExtranetGroupDispatch(t *testing.T) {
	for _, action := range []string{"create", "update"} {
		for _, mode := range []string{"accepted", "denied", "missing-permit", "lost", "revoked", "stale-native"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				posts := 0
				var afterPost func()
				nativeMode := "valid"
				if action == "update" {
					nativeMode = "expansion"
				}
				a, config, want, _, late := extranetManagedFixture(t, nativeMode, func(w http.ResponseWriter, r *http.Request) bool {
					if r.Method == "GET" {
						return false
					}
					expected := "POST"
					if action == "update" {
						expected = "PUT"
					}
					if r.Method != expected || r.URL.Path != fabricExtranetPath {
						t.Error("unexpected mutation", r.Method, r.URL.Path)
						w.WriteHeader(400)
						return true
					}
					posts++
					var body []fabricExtranetPolicy
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0].Name != "Shared" || body[0].Provider != "Services" || len(body[0].Subscribers) != 2 || len(body[0].Fabrics) != 1 || body[0].Fabrics[0] != "fabric" {
						t.Error("invalid native extranet body")
						w.WriteHeader(400)
						return true
					}
					if action == "create" && body[0].ID != "" || action == "update" && body[0].ID != "policy" {
						t.Error("wrong native policy identity")
					}
					if afterPost != nil {
						afterPost()
					}
					if mode == "lost" {
						w.WriteHeader(500)
						return true
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return true
				})
				config.Spec.Source.Inline.Raw = []byte(extranetModel)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				source, err := loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				if action == "update" {
					retainExtranetCreationFixture(t, a, config, want)
				}
				op, err := a.prepareExtranetPolicy(ctx, config, source, "Shared")
				if err != nil || op == nil {
					t.Fatal("prepare", err)
				}
				var members []configurationDispatchFixtureMember
				for _, target := range op.PhysicalGroup.Targets {
					var device cvk.CiscoDevice
					var node core.Node
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: target.Device.Name}, &device); err != nil {
						t.Fatal(err)
					}
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: target.Node.Name}, &node); err != nil {
						t.Fatal(err)
					}
					members = append(members, configurationDispatchFixtureMember{&device, &node})
				}
				permit, revoke := configurationGroupDispatchFixture(t, a, members, true)
				if mode == "denied" {
					revoke()
				}
				if mode == "revoked" {
					afterPost = revoke
				}
				if mode == "missing-permit" {
					permit = nil
				}
				if mode == "stale-native" {
					late.Store(true)
				}
				task, err := a.dispatchExtranetPolicy(ctx, config, source, "Shared", permit)
				if (err == nil) != (mode == "accepted") {
					t.Fatal("dispatch outcome", err)
				}
				retained := journalState(t, a).Operation
				switch mode {
				case "accepted":
					if retained.Phase != "Accepted" || retained.TaskID != "task" || task.ID != "task" || posts != 1 {
						t.Fatal("missing accepted receipt", retained, task, posts)
					}
				case "denied", "missing-permit", "stale-native":
					if retained.Phase != "Prepared" || retained.SubmittedAt != nil || posts != 0 {
						t.Fatal("unadmitted submission", retained, posts)
					}
				default:
					if retained.Phase != "Submitting" || retained.SubmittedAt == nil || posts != 1 {
						t.Fatal("uncertainty not retained", retained, posts)
					}
					if mode == "revoked" && task.ID != "task" {
						t.Fatal("known task lost")
					}
				}
				before := posts
				if _, err := a.dispatchExtranetPolicy(ctx, config, source, "Shared", permit); err == nil || posts != before {
					t.Fatal("operation replayed", err, posts)
				}
			})
		}
	}
}
