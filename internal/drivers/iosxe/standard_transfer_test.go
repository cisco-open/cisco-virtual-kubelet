// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package iosxe

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
)

// Model the observed firmware behavior: any install would succeed from stale
// data, including an unchecked local cache path. A failed transfer must never
// reach configuration, install, activation, or start, under either pull policy.
func TestStandardFailedDownloadCannotLaunchStalePackage(t *testing.T) {
	for _, policy := range []v1.PullPolicy{v1.PullAlways, v1.PullIfNotPresent} {
		for _, twoPhase := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/twoPhase=%v", policy, twoPhase), func(t *testing.T) {
				copies, otherWrites := 0, 0
				client := &fakeNetworkClient{postHook: func(path string, _ any) error {
					if path == "/restconf/operations/Cisco-IOS-XE-rpc:copy" {
						copies++
						return errors.New("certificate revoked")
					}
					otherWrites++
					return nil
				}, getHook: func(_ string, result any) error {
					if root, ok := result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData); ok {
						*root = *operResponse("test-app", "RUNNING")
					}
					return nil
				}}
				d := newTestDriver(client)
				cfg := minimalAppConfig("https://origin/new.tar", policy, time.Second)
				cfg.Spec.RequiresTwoPhaseStart = twoPhase
				if err := d.CreateAppHostingApp(context.Background(), cfg); err == nil {
					t.Fatal("failed copy reported success")
				}
				if copies != 1 || otherWrites != 0 {
					t.Fatalf("copies=%d app writes=%d", copies, otherWrites)
				}
			})
		}
	}
}

func TestStandardTransferPrecedesConfigurationAndInstallsOnlyLocalFile(t *testing.T) {
	order := []string{}
	state := ""
	dest := ""
	client := &fakeNetworkClient{postHook: func(path string, payload any) error {
		switch path {
		case "/restconf/operations/Cisco-IOS-XE-rpc:copy":
			order = append(order, "copy")
			input := payload.(map[string]any)["Cisco-IOS-XE-rpc:copy"].(map[string]string)
			dest = input["destination-drop-node-name"]
			if !standardStageName.MatchString(pathBase(dest)) {
				t.Fatal(input)
			}
		case "/restconf/operations/Cisco-IOS-XE-rpc:delete":
			order = append(order, "cleanup")
			if state != "RUNNING" {
				t.Fatal("cleanup before RUNNING")
			}
		case appHostingRPCPath:
			input := payload.(map[string]any)
			if v := input["install"]; v != nil {
				if v.(map[string]string)["package"] != dest {
					t.Fatalf("install used %v", v)
				}
				order = append(order, "install")
				state = "DEPLOYED"
			} else if input["activate"] != nil {
				order = append(order, "activate")
				state = "ACTIVATED"
			} else if input["start"] != nil {
				order = append(order, "start")
				state = "RUNNING"
			}
		default:
			order = append(order, "config")
		}
		return nil
	}, getHook: func(_ string, result any) error {
		if state != "" {
			*result.(*Cisco_IOS_XEAppHostingOper_AppHostingOperData) = *operResponse("test-app", state)
		}
		return nil
	}}
	d := newTestDriver(client)
	cfg := minimalDockerResourceConfig("https://origin/new.tar", v1.PullIfNotPresent, time.Second)
	if err := d.CreateAppHostingApp(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"copy", "config", "install", "activate", "start", "cleanup"}) {
		t.Fatal(order)
	}
}

func TestStandardReconciliationCannotBypassFailedDownload(t *testing.T) {
	installs := 0
	client := &fakeNetworkClient{getHook: func(_ string, _ any) error { return nil }, postHook: func(path string, _ any) error {
		if path == "/restconf/operations/Cisco-IOS-XE-rpc:copy" {
			return errors.New("404 download failed")
		}
		installs++
		return nil
	}}
	d := newTestDriver(client)
	cfg := minimalDockerResourceConfig("https://origin/missing.tar", v1.PullAlways, time.Second)
	d.ReconcileApp(context.Background(), cfg)
	if cfg.Status.Phase != AppPhaseError || installs != 0 {
		t.Fatalf("phase=%s installs=%d", cfg.Status.Phase, installs)
	}
}

func TestStandardCopyRequiresQualifiedCompletionResponse(t *testing.T) {
	for _, result := range []string{"", "%Error opening source", "Copy operation failed", "queued", "RPC request successful"} {
		t.Run(result, func(t *testing.T) {
			d := newTestDriver(&fakeNetworkClient{postWithResultHook: func(_ string, _ any, out any) error {
				response := out.(*appHostingRPCOutput)
				response.Present = result != ""
				response.Result = result
				return nil
			}})
			err := d.copyRPC(context.Background(), "https://origin/source.tar", "flash:/target.tar")
			if (err == nil) != (result == "RPC request successful") {
				t.Fatalf("result %q error %v", result, err)
			}
		})
	}
}

func TestStandardCannotPassMalformedRemoteLocationsToNativeInstall(t *testing.T) {
	for _, source := range []string{"https:missing-host", "https://origin/%bad%", "//origin/path", "unknown://origin/path"} {
		t.Run(source, func(t *testing.T) {
			writes := 0
			d := newTestDriver(&fakeNetworkClient{postHook: func(_ string, _ any) error { writes++; return nil }})
			if err := d.CreateAppHostingApp(context.Background(), minimalAppConfig(source, v1.PullAlways, time.Second)); err == nil || writes != 0 {
				t.Fatalf("error=%v writes=%d", err, writes)
			}
		})
	}
}
