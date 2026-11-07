// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package iosxe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
)

func TestStandardOwnedPartialCopyCleanup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		clean   bool
	}{
		{"definitive400", &common.RESTCONFError{StatusCode: 400}, true},
		{"definitive404", &common.RESTCONFError{StatusCode: 404}, true},
		{"server500", &common.RESTCONFError{StatusCode: 500}, false},
		{"lostReply", common.NewRESTCONFMutationAmbiguousError(errors.New("lost reply")), false},
		{"unknownOutput", errors.New("unknown completion"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeNetworkClient{}
			var owned string
			deletes := 0
			client.postHook = func(p string, body any) error {
				if strings.HasSuffix(p, ":copy") {
					owned = pathBase(body.(map[string]any)["Cisco-IOS-XE-rpc:copy"].(map[string]string)["destination-drop-node-name"])
					client.mu.Lock()
					client.stageFiles = map[string]bool{owned: true, "preloaded.tar": true}
					client.mu.Unlock()
					return tc.failure
				}
				if strings.HasSuffix(p, ":delete") {
					deletes++
					return nil
				}
				t.Fatal("app mutation after failed copy")
				return nil
			}
			d := newTestDriver(client)
			err := d.CreateAppHostingApp(context.Background(), minimalAppConfig("https://origin/b.tar", v1.PullAlways, time.Second))
			if err == nil {
				t.Fatal("copy failure ignored")
			}
			if client.stageFiles[owned] == tc.clean || deletes != map[bool]int{true: 1, false: 0}[tc.clean] || !client.stageFiles["preloaded.tar"] {
				t.Fatalf("files=%v deletes=%d", client.stageFiles, deletes)
			}
			if err = d.cleanupStandardStages(context.Background(), "test-app"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStandardPreloadedAndExplicitDestinationRemainCallerOwned(t *testing.T) {
	for _, source := range []string{"flash:/preloaded.tar", "flash:/cvk-stage-0123456789abcdef0123456789abcdef.tar", "https://origin/a.tar"} {
		t.Run(source, func(t *testing.T) {
			client := &fakeNetworkClient{}
			deletes := 0
			client.postHook = func(p string, _ any) error {
				if strings.HasSuffix(p, ":delete") {
					deletes++
				}
				return nil
			}
			d := newTestDriver(client)
			cfg := minimalAppConfig(source, v1.PullAlways, time.Second)
			if strings.HasPrefix(source, "https:") {
				cfg.Spec.PackageDest = "flash:/caller.tar"
			}
			file, err := d.stageStandardPackage(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			d.standardStagesConsumed(cfg.AppName())
			if err = d.cleanupStandardStages(context.Background(), cfg.AppName()); err != nil {
				t.Fatal(err)
			}
			if deletes != 0 || len(d.standardStaging) != 0 {
				t.Fatalf("caller path %s acquired cleanup ownership", file)
			}
		})
	}
}

func TestStandardCleanupWaitsForConsumptionOrTeardown(t *testing.T) {
	client := &fakeNetworkClient{}
	d := newTestDriver(client)
	file, err := d.stageStandardPackage(context.Background(), minimalAppConfig("https://origin/a.tar", v1.PullAlways, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	d.standardStageInstallPending(file)
	if err = d.cleanupStandardStages(context.Background(), "test-app"); err != nil {
		t.Fatal(err)
	}
	if !client.stageFiles[pathBase(file)] {
		t.Fatal("deleted package while install pending")
	}
	d.standardStagesConsumed("other-app")
	if err = d.cleanupStandardStages(context.Background(), "test-app"); err != nil {
		t.Fatal(err)
	}
	if !client.stageFiles[pathBase(file)] {
		t.Fatal("another app authorized deletion")
	}
	d.standardStagesConsumed("test-app")
	if err = d.cleanupStandardStages(context.Background(), "test-app"); err != nil {
		t.Fatal(err)
	}
	if client.stageFiles[pathBase(file)] || len(d.standardStaging) != 0 {
		t.Fatal("consumed package retained")
	}
}

func TestStandardCleanupRefusesUnrecognizedOrNonRegularObservation(t *testing.T) {
	for _, observation := range []string{`{}`, `{"Cisco-IOS-XE-platform-software-oper:partition-content":[{"full-path":"WRONG","type":"file"}]}`, `{"Cisco-IOS-XE-platform-software-oper:partition-content":[{"full-path":"NAME","type":"directory"}]}`} {
		t.Run(observation, func(t *testing.T) {
			client := &fakeNetworkClient{}
			d := newTestDriver(client)
			file, err := d.stageStandardPackage(context.Background(), minimalAppConfig("https://origin/a.tar", v1.PullAlways, time.Second))
			if err != nil {
				t.Fatal(err)
			}
			deletes := 0
			client.postHook = func(p string, _ any) error {
				if strings.HasSuffix(p, ":delete") {
					deletes++
				}
				return nil
			}
			client.filesystemHook = func(_ string, out any) error {
				return json.Unmarshal([]byte(strings.ReplaceAll(observation, "NAME", pathBase(file))), out)
			}
			if err = d.cleanupStandardStages(context.Background(), "test-app"); err == nil || deletes != 0 {
				t.Fatalf("error=%v deletes=%d", err, deletes)
			}
		})
	}
}

func TestStandardAllocationDoesNotAdoptExistingNames(t *testing.T) {
	client := &fakeNetworkClient{}
	attempts := 0
	client.filesystemHook = func(p string, out any) error {
		if _, ok := out.(*standardFilesystemInventory); ok {
			return json.Unmarshal([]byte(`{"Cisco-IOS-XE-platform-software-oper:q-filesystem":[{"fru":"fru-rp","slot":0,"bay":0,"chassis":-1,"partitions":[{"name":"flash:","is-writable":true}]}]}`), out)
		}
		attempts++
		name := strings.Split(strings.Split(p, "/partition-content=")[1], "?")[0]
		return json.Unmarshal([]byte(fmt.Sprintf(`{"Cisco-IOS-XE-platform-software-oper:partition-content":[{"full-path":%q,"type":"file"}]}`, name)), out)
	}
	d := newTestDriver(client)
	if _, err := d.allocateStandardStage(context.Background(), "test-app"); err == nil || attempts != 3 || len(d.standardStaging) != 0 {
		t.Fatalf("attempts=%d receipts=%v error=%v", attempts, d.standardStaging, err)
	}
}

func TestStandardReceiptIsNotReconstructedByReplacementWorker(t *testing.T) {
	client := &fakeNetworkClient{}
	d := newTestDriver(client)
	file, err := d.stageStandardPackage(context.Background(), minimalAppConfig("https://origin/a.tar", v1.PullAlways, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	replacement := newTestDriver(client)
	replacement.standardStagesConsumed("test-app")
	if err = replacement.cleanupStandardStages(context.Background(), "test-app"); err != nil {
		t.Fatal(err)
	}
	if !client.stageFiles[pathBase(file)] {
		t.Fatal("replacement swept a file without ownership")
	}
	if err = d.preserveStandardPath("bootflash:/" + pathBase(file)); err != nil {
		t.Fatal(err)
	}
	if err = d.cleanupStandardStages(context.Background(), "test-app"); err != nil {
		t.Fatal(err)
	}
	if !client.stageFiles[pathBase(file)] {
		t.Fatal("explicitly adopted package removed")
	}
}

func TestStandardDeleteRequiresAcknowledgmentAndConfirmedAbsence(t *testing.T) {
	for _, tc := range []string{"missingAck", "stillPresent", "lostReply", "readFailure"} {
		t.Run(tc, func(t *testing.T) {
			client := &fakeNetworkClient{}
			d := newTestDriver(client)
			file, err := d.stageStandardPackage(context.Background(), minimalAppConfig("https://origin/a.tar", v1.PullAlways, time.Second))
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client.postWithResultHook = func(_ string, _ any, out any) error {
				calls++
				if tc == "lostReply" {
					return common.NewRESTCONFMutationAmbiguousError(errors.New("lost delete reply"))
				}
				if tc == "missingAck" {
					return nil
				}
				r := out.(*appHostingRPCOutput)
				r.Present = true
				r.Result = "RPC request successful"
				if tc == "readFailure" {
					client.filesystemHook = func(_ string, _ any) error { return errors.New("observation unavailable") }
				}
				return nil
			}
			if err = d.cleanupStandardStages(context.Background(), "test-app"); !common.IsRESTCONFMutationAmbiguous(err) {
				t.Fatalf("expected uncertain deletion: %v", err)
			}
			if !d.standardStaging[file].quarantined {
				t.Fatal("ambiguous deletion was retryable")
			}
			_ = d.cleanupStandardStages(context.Background(), "test-app")
			if calls != 1 {
				t.Fatal("ambiguous deletion replayed")
			}
		})
	}
}

func TestStandardUnsupportedFilesystemRetainsUnownedPackageWithoutExtraConfiguration(t *testing.T) {
	client := &fakeNetworkClient{filesystemHook: func(_ string, _ any) error { return &common.RESTCONFError{StatusCode: 404} }}
	d := newTestDriver(client)
	file, err := d.stageStandardPackage(context.Background(), minimalAppConfig("https://origin/a.tar", v1.PullAlways, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !standardStageName.MatchString(pathBase(file)) || len(d.standardStaging) != 0 {
		t.Fatalf("fallback claimed ownership: %s %v", file, d.standardStaging)
	}
	d.standardStagesConsumed("test-app")
	if err = d.cleanupStandardStages(context.Background(), "test-app"); err != nil {
		t.Fatal(err)
	}
	if !client.stageFiles[pathBase(file)] {
		t.Fatal("unowned fallback was removed")
	}
}
