// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

func TestLiveCatalystCenterNetworkEmptyProbe(t *testing.T) {
	if os.Getenv("CATC_NETWORK_EMPTY_PROBE") != "isolated-server-reset" {
		t.Skip("isolated server reset qualification disabled")
	}
	qualifyLiveSiteExecution(t, false)
}

func qualifyLiveNetworkEmptyProbe(t *testing.T, c *client, ctx context.Context, root string, waitTask func(context.Context, Task, time.Time) error, settled *bool) {
	t.Helper()
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, site := range sites {
		if site.NameHierarchy == root && site.Type == "area" {
			target = site.ID
		}
	}
	if !validAPIID(target) {
		t.Fatal("empty generated area missing")
	}
	for _, kind := range []string{"ntp", "dhcp"} {
		for index, servers := range []any{[]string{"192.0.2.21", "192.0.2.22"}, []string{}, nil} {
			before, err := c.readNetworkSetting(ctx, target, kind)
			if err != nil {
				t.Fatal("seed observation unavailable", err)
			}
			body, err := json.Marshal(map[string]any{kind: map[string]any{"servers": servers}})
			if err != nil {
				t.Fatal(err)
			}
			*settled = false
			submitted := time.Now()
			raw, err := c.mutate(ctx, http.MethodPut, sitesPath+"/"+target+"/"+kind+"Settings", body)
			if err != nil {
				var rejection *transport.RESTError
				if index == 0 || !errors.As(err, &rejection) || rejection.StatusCode != http.StatusBadRequest {
					t.Fatal("unexpected/uncertain server-reset outcome", err)
				}
				// A known synchronous validation rejection is not task completion. Only
				// unchanged generated native state permits the next independent probe.
				for range 2 {
					fresh, readErr := c.readNetworkSetting(ctx, target, kind)
					if readErr != nil || !reflect.DeepEqual(before, fresh) {
						t.Fatal("rejected request native outcome unproven", readErr)
					}
				}
				*settled = true
				t.Logf("isolated %s encoding=%d HTTP400 rejected; no task accepted; native state unchanged", kind, index)
				continue
			}
			var ack struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
				t.Fatal("original task acknowledgement unavailable")
			}
			if err := waitTask(ctx, ack.Response, submitted); err != nil {
				t.Fatal(err)
			}
			raw, err = c.get(ctx, sitesPath+"/"+target+"/"+kind+"Settings", nil, 200)
			if err != nil {
				t.Fatal(err)
			}
			var env struct {
				Response map[string]json.RawMessage `json:"response"`
			}
			if json.Unmarshal(raw, &env) != nil {
				t.Fatal("invalid native envelope")
			}
			group, ok := env.Response[kind]
			if !ok {
				t.Fatal("native setting group missing")
			}
			shape := "inherited"
			if string(group) != "null" {
				var fields map[string]json.RawMessage
				if json.Unmarshal(group, &fields) != nil || fields == nil {
					t.Fatal("native group invalid")
				}
				value, present := fields["servers"]
				switch {
				case !present:
					shape = "absent"
				case string(value) == "null":
					shape = "null"
				default:
					var list []string
					if json.Unmarshal(value, &list) != nil {
						t.Fatal("native resolver type invalid")
					}
					shape = "array"
					if index > 0 && len(list) != 0 {
						t.Fatal("reset did not clear list")
					}
				}
			}
			*settled = true
			t.Logf("isolated %s encoding=%d original task=%s native server shape=%s", kind, index, ack.Response.ID, shape)
		}
	}
}
