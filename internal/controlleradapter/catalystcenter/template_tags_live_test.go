// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestLiveCatalystCenterTags(t *testing.T) {
	if os.Getenv("CATC_TAG_EXECUTION") != "isolated-tag" {
		t.Skip("isolated tag qualification not enabled")
	}
	positive := os.Getenv("CATC_TAG_DYNAMIC_EXECUTION") == "isolated-matching-rule"
	dynamic := positive || os.Getenv("CATC_TAG_DYNAMIC_EXECUTION") == "isolated-empty-rule"
	ruleForm := os.Getenv("CATC_TAG_RULE_FORM")
	if ruleForm == "" {
		ruleForm = "AND"
	}
	if ruleForm != "AND" && ruleForm != "EQ" && ruleForm != "UNARY" && ruleForm != "OR" && ruleForm != "LIKE" && ruleForm != "ILIKE" && ruleForm != "IN" {
		t.Fatal("unsupported isolated rule qualification form")
	}
	clearCycle := dynamic && os.Getenv("CATC_TAG_RULE_TRANSITIONS") == "empty-rule-cycle"
	matchingClear := positive && os.Getenv("CATC_TAG_RULE_TRANSITIONS") == "matching-rule-clear-cycle"

	section := "templates"
	if os.Getenv("CATC_TAG_SECTION") == "inventory" {
		section = "inventory"
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	name := "CVK-Q-Tag-" + time.Now().UTC().Format("20060102-150405") + fmt.Sprintf("-%x", nonce)
	id := ""
	settled := true
	initial, err := c.listTags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := initial[name]; exists {
		t.Fatal("tag fixture exists")
	}
	expectedMembers := map[string]bool{}
	var targets []Device
	if positive {
		devices, err := c.configurationDevices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, device := range devices {
			if device.Family == "Switches and Hubs" && device.Serial != "" && device.Hostname != "" {
				targets = append(targets, device)
			}
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].Serial < targets[j].Serial })
		if len(targets) < 2 {
			t.Skip("two wired inventory identities are required for positive tag qualification")
		}
		targets = targets[:2]
	}
	checkTargets := func(checkCtx context.Context) {
		t.Helper()
		if !positive {
			return
		}
		current, err := c.configurationDevices(checkCtx)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range targets {
			// LIKE/ILIKE use a full-hostname substring below. Refuse pattern
			// metacharacters and any inventory collision before creating a rule;
			// generated qualification must not select a third device.
			if strings.ContainsAny(target.Hostname, "%_*?[]\\") {
				t.Fatal("hostname contains pattern metacharacters; positive qualification refused")
			}
			for _, device := range current {
				if device.ID != target.ID && (strings.Contains(strings.ToLower(device.Hostname), strings.ToLower(target.Hostname)) || strings.EqualFold(device.Hostname, target.Hostname+"-other")) {
					t.Fatal("positive rule would select another inventory identity; retain")
				}
			}
			found := 0
			for _, device := range current {
				if strings.EqualFold(target.Hostname, device.Hostname) {
					found++
					if target.ID != device.ID || target.Serial != device.Serial || target.Hostname != device.Hostname || target.ManagementIP != device.ManagementIP || target.PlatformID != device.PlatformID || target.Family != device.Family {
						t.Fatal("positive tag target identity changed; retain")
					}
				}
			}
			if found != 1 {
				t.Fatal("positive tag hostname is missing or ambiguous; retain")
			}
		}
	}
	check := func(checkCtx context.Context) {
		t.Helper()
		checkTargets(checkCtx)
		current, err := c.listTags(checkCtx)
		if err != nil {
			t.Fatal(err)
		}
		if tag, exists := current[name]; exists {
			if id == "" || tag.ID != id || tag.SystemTag == nil || *tag.SystemTag {
				t.Fatal("unowned or changed tag fixture; retain")
			}
			for _, memberType := range []string{"networkdevice", "template"} {
				members, err := c.tagMembers(checkCtx, id, memberType)
				want := map[string]bool{}
				if positive && memberType == "networkdevice" {
					want = expectedMembers
				}
				if err != nil || !reflect.DeepEqual(members, want) {
					t.Fatal("tag fixture has changed members or unreadable scope; retain", err)
				}
			}
			if positive {
				for _, association := range []string{"DYNAMIC", "STATIC", "MIXED"} {
					members, err := c.tagMembersByAssociation(checkCtx, id, "networkdevice", association)
					want := map[string]bool{}
					if association == "DYNAMIC" {
						want = expectedMembers
					}
					if err != nil || !reflect.DeepEqual(members, want) {
						t.Fatal("tag association provenance changed; retain", association, err)
					}
				}
			}
			delete(current, name)
		} else if id != "" {
			t.Fatal("owned tag fixture disappeared; retain")
		}
		if !reflect.DeepEqual(initial, current) {
			t.Fatal("unrelated tag baseline changed; retain")
		}
	}
	wait := func(ctx context.Context, task Task, submitted time.Time) error {
		for {
			done, err := c.configurationTaskComplete(ctx, task.ID, submitted, time.Now())
			if err != nil || done {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	t.Cleanup(func() {
		if !settled || id == "" {
			t.Log("retained uncertain tag", name, id)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		tags, err := c.listTags(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		if dynamic {
			check(cleanup)
		}
		tag, exists := tags[name]
		if !exists {
			return
		}
		if tag.ID != id || tag.SystemTag == nil || *tag.SystemTag {
			t.Error("cleanup refused changed/protected tag")
			return
		}
		submitted := time.Now()
		raw, err := c.mutate(cleanup, http.MethodDelete, tagsPath+"/"+id, nil)
		if err != nil {
			t.Error(err)
			return
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Error("cleanup receipt missing")
			return
		}
		if err = wait(cleanup, receipt.Response, submitted); err != nil {
			t.Error(err)
			return
		}
		tags, err = c.listTags(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		if _, exists := tags[name]; exists {
			t.Error("tag remains")
			return
		}
		if dynamic && !reflect.DeepEqual(initial, tags) {
			t.Error("tag baseline not restored")
			return
		}
		t.Log("tag cleanup verified", id)
	})
	compile := func(raw []byte) (configurationExecutor, error) {
		if os.Getenv("CATC_COMBINED_TAG_APPLY") == "true" {
			plan, err := c.compileConfigurationApply(raw, []string{"inventory", "templates"}, nil)
			if err != nil {
				return nil, err
			}
			return plan.executor, nil
		}
		return c.compileConfigurationExecutor(raw, section)
	}
	descriptions := []string{"CVK isolated tag", "CVK isolated tag updated"}
	if clearCycle {
		descriptions = append(descriptions, "CVK isolated tag rules cleared", "CVK isolated tag rules restored")
	}
	if positive {
		descriptions = []string{"CVK dynamic first target", "CVK dynamic second target", "CVK dynamic no targets", "CVK dynamic cleared"}
		if matchingClear {
			descriptions = []string{"CVK dynamic first target", "CVK dynamic second target", "CVK dynamic matching rules cleared", "CVK dynamic first target restored", "CVK dynamic no targets", "CVK dynamic cleared"}
		}
	}
	for index, description := range descriptions {
		matchingTarget := -1
		if positive && index < 2 {
			matchingTarget = index
		}
		if matchingClear && index == 3 {
			matchingTarget = 0
		}
		rulesCleared := clearCycle && index == 2 || positive && index == len(descriptions)-1 || matchingClear && index == 2
		entry := map[string]any{"name": name, "description": description, "system_tag": false}
		if dynamic {
			check(ctx)
			value := fmt.Sprintf("%s-never-match-%d", name, index)
			if matchingTarget >= 0 {
				value = targets[matchingTarget].Hostname
			}
			devices, err := c.configurationDevices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, device := range devices {
				if matchingTarget < 0 && strings.Contains(strings.ToLower(device.Hostname), strings.ToLower(value)) {
					t.Fatal("isolated dynamic rule would select an existing device")
				}
			}
			rule := map[string]any{"member_type": "networkdevice", "operation": "AND", "items": []any{map[string]any{"operation": "EQ", "name": "hostname", "value": value}, map[string]any{"operation": "EQ", "name": "family", "value": "Switches and Hubs"}}}
			switch ruleForm {
			case "OR":
				rule["operation"] = "OR"
				rule["items"] = []any{map[string]any{"operation": "EQ", "name": "hostname", "value": value}, map[string]any{"operation": "EQ", "name": "hostname", "value": value + "-other"}}
			case "LIKE", "ILIKE":
				rule["items"].([]any)[0] = map[string]any{"operation": ruleForm, "name": "hostname", "value": "%" + value + "%"}
			case "UNARY":
				rule["items"] = rule["items"].([]any)[:1]
				if index%2 == 1 {
					rule["operation"] = "OR"
				}
			case "EQ":
				rule = map[string]any{"member_type": "networkdevice", "operation": "EQ", "name": "hostname", "value": value}
			case "IN":
				rule = map[string]any{"member_type": "networkdevice", "operation": "IN", "name": "hostname", "values": []any{value, value + "-other"}}
			}
			entry["dynamic_rules"] = []any{rule}
			if rulesCleared {
				entry["dynamic_rules"] = []any{}
			}
		}
		root := map[string]any{section: map[string]any{"tags": []any{entry}}}
		if os.Getenv("CATC_COMBINED_TAG_APPLY") == "true" {
			root = map[string]any{"inventory": root[section], "templates": map[string]any{"tags": []any{map[string]any{"name": name, "description": "shadowed template description"}}}}
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": root})
		executor, err := compile(raw)
		if err != nil {
			t.Fatal(err)
		}
		write, err := executor.next(ctx)
		if err != nil || write == nil {
			t.Fatal("tag plan missing", err)
		}
		settled = false
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("tag operation", index, write.action, task.ID)
		if err = wait(ctx, task, submitted); err != nil {
			t.Fatal(err)
		}
		executor, err = compile(raw)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := executor.verify(ctx, &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, TaskID: task.ID, SubmittedAt: &metav1.Time{Time: submitted}}); err != nil || !ok {
			t.Fatal("tag read-back failed", err)
		}
		tags, err := c.listTags(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if id != "" && id != tags[name].ID {
			t.Fatal("tag UUID changed during update; retain")
		}
		id = tags[name].ID
		if clearCycle || positive {
			if (len(tags[name].DynamicRules) == 0) != rulesCleared {
				t.Fatal("native rule clearing/restoration not independently observed; retain")
			}
			t.Log("native rule readback", index, len(tags[name].DynamicRules), "nilList", tags[name].DynamicRules == nil)
		}
		if positive {
			wantMembers := map[string]bool{}
			if matchingTarget >= 0 {
				wantMembers[targets[matchingTarget].ID] = true
			}
			deadline := time.Now().Add(45 * time.Second)
			for {
				checkTargets(ctx)
				all, err := c.tagMembers(ctx, id, "networkdevice")
				if err != nil {
					t.Fatal(err)
				}
				evaluated, err := c.tagMembersByAssociation(ctx, id, "networkdevice", "DYNAMIC")
				if err != nil {
					t.Fatal(err)
				}
				for member := range all {
					if member != targets[0].ID && member != targets[1].ID {
						t.Fatal("foreign dynamic member; retain")
					}
				}
				if reflect.DeepEqual(all, wantMembers) && reflect.DeepEqual(evaluated, wantMembers) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("evaluated membership did not converge; retain", index, len(all), len(evaluated), len(wantMembers))
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Second):
				}
			}
			expectedMembers = wantMembers
			t.Log("evaluated dynamic membership", index, len(expectedMembers))
		}
		settled = true
		if dynamic {
			check(ctx)
		}
		if next, err := executor.next(ctx); err != nil || next != nil {
			t.Fatal("tag did not converge", err)
		}
		if positive && index == 0 && os.Getenv("CATC_TAG_EXPLICIT_ASSOCIATION") == "isolated-mixed" {
			qualifyLiveMixedTagMembership(t, c, ctx, name, id, targets[0], entry, &settled, wait)
			check(ctx)
		}
	}
	// Inventory wins over a conflicting template declaration of the same tag.
	combined, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{
		"templates": map[string]any{"tags": []any{map[string]any{"name": name, "description": "shadowed template description"}}},
		"inventory": map[string]any{"tags": []any{map[string]any{"name": name, "description": descriptions[len(descriptions)-1], "system_tag": false}}},
	}})
	plans, err := c.compileConfigReports(combined, []string{"templates", "inventory"})
	if err != nil {
		t.Fatal("combined tag report compilation", err)
	}
	count := 0
	for _, p := range plans {
		count += p.count
		drift, err := p.observe(ctx)
		if err != nil || len(drift) != 0 {
			t.Fatal("combined tag precedence did not converge", err)
		}
	}
	if count != 1 {
		t.Fatal("combined tag counted twice")
	}
	t.Log("combined inventory/template tag precedence verified against one live tag")
	if !dynamic && os.Getenv("CATC_DEVICE_TAG_EXECUTION") == "isolated-association" {
		qualifyLiveDeviceMembership(t, c, ctx, name, id, &settled, wait)
	}
	if positive {
		t.Log("positive dynamic evaluation, provenance and cleanup preparation verified", "matchingRuleClearCycle", matchingClear)
	} else if clearCycle {
		t.Log("native dynamic tag create/update/clear/restore, same UUID, no-op and empty membership verified")
	} else if dynamic {
		t.Log("native dynamic tag", ruleForm, "create, same-UUID rule update and empty membership verified")
	} else {
		t.Log("native static tag create/update verified")
	}
}
