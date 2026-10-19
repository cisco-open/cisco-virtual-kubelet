// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"testing"
)

func TestAuthExecution(t *testing.T) {
	for _, mode := range []string{"update", "missing", "replaced", "lost-receipt"} {
		t.Run(mode, func(t *testing.T) {
			desired, err := parseAuthTemplates([]byte(authModel))
			if err != nil {
				t.Fatal(err)
			}
			actual := desired[0]
			actual.ID = "profile"
			hosts := "Unlimited"
			actual.Hosts = &hosts
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != authProfilesPath {
					t.Error("unexpected API")
					w.WriteHeader(404)
					return
				}
				if r.Method == http.MethodGet {
					profiles := []authProfile{actual}
					if mode == "missing" {
						profiles = []authProfile{}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": profiles})
					return
				}
				if r.Method != http.MethodPut {
					t.Error("expected PUT")
				}
				var payload []map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if len(payload) != 1 {
					t.Error("expected single profile")
					w.WriteHeader(400)
					return
				}
				if _, ok := payload[0]["fabricId"]; ok {
					t.Error("global update includes fabric")
				}
				if _, ok := payload[0]["preAuthAcl"]; ok {
					t.Error("irrelevant ACL sent")
				}
				raw, _ := json.Marshal(payload[0])
				if err := json.Unmarshal(raw, &actual); err != nil {
					t.Error(err)
				}
				if actual.ID != "profile" {
					t.Error("UUID not bound")
				}
				writes++
				if mode == "lost-receipt" {
					fmt.Fprint(w, `{}`)
					return
				}
				fmt.Fprint(w, `{"response":{"taskId":"auth-task"}}`)
			})
			executor, err := c.compileConfigurationExecutor([]byte(authModel), "authentication_templates")
			if err != nil {
				t.Fatal(err)
			}
			write, err := executor.next(context.Background())
			if mode == "missing" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("missing profile accepted")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("missing plan", err)
			}
			_, err = write.submit(context.Background())
			if mode == "lost-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("ambiguous submission replayed or accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "replaced" {
				actual.ID = "replacement"
			}
			// Reconstruct the executor as a worker restart would.
			executor, err = c.compileConfigurationExecutor([]byte(authModel), "authentication_templates")
			if err != nil {
				t.Fatal(err)
			}
			ok, err := executor.verify(context.Background(), &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target})
			if mode == "replaced" {
				if err == nil || ok {
					t.Fatal("replaced UUID accepted")
				}
				return
			}
			if err != nil || !ok {
				t.Fatal("read-back failed", err)
			}
			if next, err := executor.next(context.Background()); err != nil || next != nil {
				t.Fatal("did not converge", err)
			}
			if writes != 1 {
				t.Fatal("write replayed")
			}
		})
	}
}
