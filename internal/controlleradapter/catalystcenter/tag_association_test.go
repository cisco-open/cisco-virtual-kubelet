// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestTagAssociationQueriesRemainScopedWhilePaging(t *testing.T) {
	for _, memberType := range []string{"networkdevice", "template"} {
		for _, association := range []string{"", "DYNAMIC", "STATIC", "MIXED"} {
			t.Run(memberType+"/"+association, func(t *testing.T) {
				reads := 0
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					reads++
					query := r.URL.Query()
					if r.Method != "GET" || r.URL.Path != tagsPath+"/tag/member" || query.Get("memberType") != memberType || query.Get("memberAssociationType") != association || query.Get("limit") != "500" {
						t.Error("association filter or tag/type boundary changed")
					}
					if association == "" && query.Has("memberAssociationType") {
						t.Error("unfiltered legacy query was changed")
					}
					rows := []map[string]string{}
					if query.Get("offset") == "1" {
						for i := 0; i < 500; i++ {
							rows = append(rows, map[string]string{"instanceUuid": fmt.Sprintf("member-%d", i)})
						}
					} else if query.Get("offset") == "501" {
						rows = append(rows, map[string]string{"instanceUuid": "last"})
					} else {
						t.Error("unexpected page offset")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				})
				members, err := c.tagMembersByAssociation(context.Background(), "tag", memberType, association)
				if err != nil || len(members) != 501 || !members["last"] || reads != 2 {
					t.Fatal("filtered pagination incomplete", err)
				}
			})
		}
	}
}

func TestTemplateTagsRequireExplicitAssociation(t *testing.T) {
	for _, association := range []string{"DYNAMIC", "STATIC", "MIXED"} {
		t.Run(association, func(t *testing.T) {
			intent, err := parseTemplateIntent([]byte(strings.Replace(templateModel, "- name: base", "- name: base\n            tags: [tag]", 1)), map[string]string{"base.j2": "body"})
			if err != nil {
				t.Fatal(err)
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				switch r.URL.Path {
				case tagsPath:
					fmt.Fprint(w, `{"response":[{"id":"tag-id","name":"tag","systemTag":false}]}`)
				case templateProjectsPath:
					fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
				case templatesPath:
					fmt.Fprint(w, `[{"templateId":"template","projectId":"project","name":"base"}]`)
				case tagsPath + "/tag-id/member":
					if r.Method == http.MethodGet {
						filter := r.URL.Query().Get("memberAssociationType")
						if r.URL.Query().Get("memberType") != "template" || filter != "STATIC" && filter != "MIXED" {
							t.Error("explicit template intent used unscoped presence")
						}
						rows := []map[string]string{}
						if filter == association || writes > 0 && filter == "MIXED" {
							rows = append(rows, map[string]string{"instanceUuid": "template"})
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
						return
					}
					if r.Method != http.MethodPost || association != "DYNAMIC" {
						t.Error("unexpected manual template mutation")
					}
					var payload map[string][]string
					if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 || len(payload["template"]) != 1 || payload["template"][0] != "template" {
						t.Error("wrong template association body")
					}
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				default:
					t.Error("unexpected endpoint")
					http.NotFound(w, r)
				}
			})
			e := &templateMembershipExecutor{client: c, intent: intent}
			write, err := e.next(context.Background())
			if err != nil || (write != nil) != (association == "DYNAMIC") {
				t.Fatal("dynamic-only presence satisfied explicit template intent", err)
			}
			if write != nil {
				op := &cvk.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, TargetID: write.identity.target, ParentID: write.identity.parent}
				if ok, err := e.verify(context.Background(), op); err != nil || ok {
					t.Fatal("rule-only membership verified as manual before submission", err)
				}
				if _, err := write.submit(context.Background()); err != nil {
					t.Fatal(err)
				}
				if ok, err := e.verify(context.Background(), op); err != nil || !ok {
					t.Fatal("mixed template membership failed verification", err)
				}
			}
			if next, err := e.next(context.Background()); err != nil || next != nil {
				t.Fatal("manual template association replayed", err)
			}
		})
	}
}

func TestDeviceTagsRequireExplicitAssociation(t *testing.T) {
	for _, association := range []string{"DYNAMIC", "STATIC", "MIXED"} {
		t.Run(association, func(t *testing.T) {
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				switch r.URL.Path {
				case tagsPath:
					fmt.Fprint(w, `{"response":[{"id":"tag","name":"tag","systemTag":false}]}`)
				case devicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "device", Hostname: "switch", Serial: "serial"}}})
				case tagsPath + "/tag/member":
					if r.Method == "GET" {
						filter := r.URL.Query().Get("memberAssociationType")
						if filter == "" {
							t.Error("explicit intent used unfiltered dynamic presence")
						}
						rows := []map[string]string{}
						if filter == association || writes > 0 && filter == "MIXED" {
							rows = append(rows, map[string]string{"instanceUuid": "device"})
						}
						json.NewEncoder(w).Encode(map[string]any{"response": rows})
						return
					}
					if r.Method != "POST" || association != "DYNAMIC" {
						t.Error("unexpected manual membership write")
					}
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				default:
					t.Error("unexpected endpoint")
				}
			})
			e := &deviceMembershipExecutor{client: c, devices: []desiredDeviceMembership{{device: configurationDeviceReference{Name: "switch", Serial: "serial"}, tags: []string{"tag"}}}}
			write, err := e.next(context.Background())
			if err != nil || (write != nil) != (association == "DYNAMIC") {
				t.Fatal("dynamic presence substituted for explicit intent", err)
			}
			if write != nil {
				if _, err := write.submit(context.Background()); err != nil {
					t.Fatal(err)
				}
				operation := &cvk.NetworkControllerConfigurationOperation{Action: "Update", Resource: write.resource, TargetID: "device", ParentID: "tag"}
				if complete, err := e.verify(context.Background(), operation); err != nil || !complete {
					t.Fatal("mixed association did not verify explicit intent", err)
				}
			}
			if next, err := e.next(context.Background()); err != nil || next != nil {
				t.Fatal("explicit association replayed", err)
			}
		})
	}
}

func TestTagAssociationReadRefusesInvalidScopeAndFallback(t *testing.T) {
	reads := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		reads++
		if r.URL.Query().Get("memberAssociationType") != "STATIC" {
			t.Error("rejected provenance query fell back to unfiltered presence")
		}
		w.WriteHeader(400)
	})
	for _, scope := range [][3]string{{"tag", "networkdevice", "ALL"}, {"../tag", "networkdevice", "STATIC"}, {"tag", "unsupported", "STATIC"}} {
		if members, err := c.tagMembersByAssociation(context.Background(), scope[0], scope[1], scope[2]); err == nil || members != nil || reads != 0 {
			t.Fatal("invalid association scope reached native API")
		}
	}
	if members, err := c.tagExplicitDeviceMembers(context.Background(), "tag"); err == nil || members != nil || reads != 1 {
		t.Fatal("failed explicit query supplied fallback evidence")
	}
	if members, err := c.tagExplicitTemplateMembers(context.Background(), "tag"); err == nil || members != nil || reads != 2 {
		t.Fatal("failed template query supplied fallback evidence")
	}
}

func TestTagExplicitAssociationRejectsPartialClassEvidence(t *testing.T) {
	for _, memberType := range []string{"template", "networkdevice"} {
		t.Run(memberType, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if r.URL.Query().Get("memberType") != memberType {
					t.Error("member scope changed")
				}
				switch r.URL.Query().Get("memberAssociationType") {
				case "STATIC":
					fmt.Fprint(w, `{"response":[{"instanceUuid":"member"}]}`)
				case "MIXED":
					w.WriteHeader(400)
				default:
					t.Error("failed classified lookup used a fallback")
					w.WriteHeader(400)
				}
			})
			if members, err := c.tagExplicitMembers(context.Background(), "tag", memberType); err == nil || members != nil || reads != 2 {
				t.Fatal("partial class inventory supplied usable evidence", err)
			}
		})
	}
}

func TestTagAssociationMalformedMemberCannotSupplyPartialEvidence(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		fmt.Fprint(w, `{"response":[{"instanceUuid":"valid"},{"instanceUuid":"../foreign"}]}`)
	})
	members, err := c.tagMembersByAssociation(context.Background(), "tag", "networkdevice", "DYNAMIC")
	if err == nil || members != nil {
		t.Fatal("malformed member left usable partial evidence")
	}
}
