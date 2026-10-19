// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestLiveCatalystCenterIPAMExecution(t *testing.T) {
	if os.Getenv("CATC_IPAM_EXECUTION") != "isolated-hierarchy" {
		t.Skip("explicit isolated IPAM qualification not enabled")
	}
	qualifyLiveSiteExecution(t, false)
}
func qualifyLiveIPAM(t *testing.T, c *client, ctx context.Context, root string, waitTask func(context.Context, Task, time.Time) error, settled *bool) {
	t.Helper()
	var model map[string]any
	json.Unmarshal([]byte(poolModel), &model)
	stripe := model["catalyst_center"].(map[string]any)
	name := lastSiteName(root)
	stripe["sites"] = map[string]any{"areas": []any{map[string]any{"name": name, "parent_name": "Global", "ip_pools_reservations": []string{name + "-subnet"}}}}
	pool := stripe["network_settings"].(map[string]any)["ip_pools"].([]any)[0].(map[string]any)
	pool["name"] = name + "-pool"
	pool["ip_pools_reservations"].([]any)[0].(map[string]any)["name"] = name + "-subnet"
	raw, _ := json.Marshal(model)
	executor, err := c.compileConfigurationExecutor(raw, "network_settings")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := c.listIPPools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range initial {
		if p.Name == name+"-pool" {
			t.Fatal("fixture pool already exists")
		}
	}
	// Preflight rejects overlaps with any existing allocation before mutation.
	ids := map[string]string{}
	t.Cleanup(func() {
		if !*settled {
			t.Log("retained uncertain IPAM fixture", name)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		for _, path := range []string{sitePoolsPath, globalPoolsPath} {
			id := ids[path]
			if id == "" {
				continue
			}
			if path == sitePoolsPath {
				rs, err := c.listIPReservations(cleanup)
				if err != nil {
					t.Error(err)
					*settled = false
					return
				}
				found := false
				for _, r := range rs {
					if r.ID == id {
						found = true
						if r.Name != name+"-subnet" {
							t.Error("reservation identity changed")
							*settled = false
							return
						}
					}
				}
				if !found {
					continue
				}
			} else {
				rs, err := c.listIPReservations(cleanup)
				if err != nil {
					t.Error(err)
					*settled = false
					return
				}
				for _, r := range rs {
					for _, s := range []*poolAddressSpace{r.V4, r.V6} {
						if s != nil && s.GlobalPoolID == id {
							t.Error("pool still has reservations")
							*settled = false
							return
						}
					}
				}
				ps, err := c.listIPPools(cleanup)
				if err != nil {
					t.Error(err)
					*settled = false
					return
				}
				found := false
				for _, p := range ps {
					if p.ID == id {
						found = true
						if p.Name != name+"-pool" {
							t.Error("pool identity changed")
							*settled = false
							return
						}
					}
				}
				if !found {
					continue
				}
			}
			*settled = false
			submitted := time.Now()
			response, err := c.mutate(cleanup, http.MethodDelete, path+"/"+id, nil)
			if err != nil {
				t.Error(err)
				return
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(response, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				t.Error("cleanup missing receipt")
				return
			}
			if err = waitTask(cleanup, receipt.Response, submitted); err != nil {
				t.Error(err)
				return
			}
			if path == sitePoolsPath {
				rs, err := c.listIPReservations(cleanup)
				if err != nil {
					t.Error(err)
					return
				}
				for _, r := range rs {
					if r.ID == id {
						t.Error("reservation remains")
						return
					}
				}
			} else {
				ps, err := c.listIPPools(cleanup)
				if err != nil {
					t.Error(err)
					return
				}
				for _, p := range ps {
					if p.ID == id {
						t.Error("pool remains")
						return
					}
				}
			}
			*settled = true
			t.Log("verified fixture removal", path, id)
		}
	})
	for step := 0; step < 2; step++ {
		write, err := executor.next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if write == nil || write.action != "Create" {
			t.Fatal("expected fixture create")
		}
		*settled = false
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("submitted %s task=%s", write.resource, task.ID)
		if err = waitTask(ctx, task, submitted); err != nil {
			t.Fatal(err)
		}
		ok, err := executor.verify(ctx, &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent})
		if err != nil || !ok {
			t.Fatalf("read-back verified=%v err=%v", ok, err)
		}
		ps, err := c.listIPPools(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if p.Name == name+"-pool" {
				ids[globalPoolsPath] = p.ID
			}
		}
		rs, err := c.listIPReservations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if r.Name == name+"-subnet" {
				ids[sitePoolsPath] = r.ID
			}
		}
		*settled = true
	}
	if next, err := executor.next(ctx); err != nil || next != nil {
		t.Fatalf("IPAM convergence failed: %v", err)
	}

	if os.Getenv("CATC_LAN_POOL_USAGE_QUALIFICATION") == "isolated-ipam" {
		reservations, err := c.listIPReservations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var bound *ipReservation
		for i := range reservations {
			if reservations[i].ID == ids[sitePoolsPath] && reservations[i].Name == name+"-subnet" {
				copy := reservations[i]
				bound = &copy
			}
		}
		if bound == nil {
			t.Fatal("owned qualification reservation missing")
		}
		usage, err := c.lanPoolUsage(ctx, *bound)
		if err != nil {
			t.Fatal("native modern LAN pool usage qualification", err)
		}
		if usage.ReservationID != bound.ID || usage.SiteID != bound.SiteID || usage.ParentID != bound.V4.GlobalPoolID {
			t.Fatal("usage lost owned reservation/site/global-parent binding")
		}
		t.Logf("qualified isolated LAN pool usage: total=%d unassignable=%d assigned=%d defaultAssigned=%d conservativeRemaining=%d", usage.Total, usage.Unassignable, usage.Assigned, usage.DefaultAssigned, usage.remaining())
	}
	t.Log("global IPv4 pool and site reservation created and read-back verified")
}
