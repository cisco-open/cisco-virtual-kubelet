// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package iosxe

import (
	"context"
	"fmt"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
)

// ReconcileApp performs a single reconciliation step, driving the app one state
// closer to its desired state and updating appConfig.Status in place.
//
// Forward (DesiredState = Running):
//
//	"" (no config)  → POST config + install RPC  → Converging
//	"" (config, no oper) → re-issue install RPC   → Converging
//	"DEPLOYED"      → activate RPC               → Converging
//	"ACTIVATED"     → start RPC                  → Converging
//	"RUNNING"       → no-op                      → Ready
//
// Reverse (DesiredState = Deleted):
//
//	"RUNNING"       → stop RPC                   → Deleting
//	"ACTIVATED"/"STOPPED" → deactivate RPC       → Deleting
//	"DEPLOYED"      → uninstall RPC              → Deleting
//	"" (no oper)    → delete config              → Deleted
func (d *XEDriver) ReconcileApp(ctx context.Context, appConfig *AppHostingConfig) {
	appID := appConfig.AppName()
	desired := appConfig.Spec.DesiredState

	// 1. Observe current device state.
	state := d.getAppState(ctx, appID)
	appConfig.Status.ObservedState = state
	appConfig.Status.LastTransition = time.Now()

	log.G(ctx).Infof("ReconcileApp %s: observed=%q desired=%s phase=%s",
		appID, state, desired, appConfig.Status.Phase)

	// ── Forward path: drive toward RUNNING ────────────────────────────
	if desired == AppDesiredStateRunning {
		switch state {
		case "RUNNING":
			appConfig.Status.Phase = AppPhaseReady
			appConfig.Status.Message = "App is running"
			return

		case "ACTIVATED":
			// ACTIVATED → start
			appConfig.Status.Phase = AppPhaseConverging
			appConfig.Status.Message = "Starting app"
			if err := d.StartApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: start failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("start failed: %v", err)
			}
			return

		case "DEPLOYED":
			// DEPLOYED → activate
			appConfig.Status.Phase = AppPhaseConverging
			appConfig.Status.Message = "Activating app"
			if err := d.ActivateApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: activate failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("activate failed: %v", err)
			}
			return

		default:
			// No oper data (or unexpected state) — the install likely hasn't
			// happened or failed silently. Re-issue install if we have an image.
			imagePath := appConfig.ImagePath()
			if imagePath == "" {
				log.G(ctx).Warnf("ReconcileApp %s: no oper data and no image path; cannot install", appID)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = "no image path available for install"
				return
			}
			appConfig.Status.Phase = AppPhaseConverging
			appConfig.Status.Message = "Re-issuing install"
			log.G(ctx).Warnf("ReconcileApp %s: no oper data; re-issuing install (image: %s)", appID, imagePath)
			if err := d.InstallApp(ctx, appID, imagePath); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: install failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("install failed: %v", err)
			}
			return
		}
	}

	// ── Reverse path: drive toward deletion ───────────────────────────
	if desired == AppDesiredStateDeleted {
		switch state {
		case "RUNNING":
			appConfig.Status.Phase = AppPhaseDeleting
			appConfig.Status.Message = "Stopping app"
			if err := d.StopApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: stop failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("stop failed: %v", err)
			}
			return

		case "ACTIVATED", "STOPPED":
			appConfig.Status.Phase = AppPhaseDeleting
			appConfig.Status.Message = "Deactivating app"
			if err := d.DeactivateApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: deactivate failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("deactivate failed: %v", err)
			}
			return

		case "DEPLOYED":
			appConfig.Status.Phase = AppPhaseDeleting
			appConfig.Status.Message = "Uninstalling app"
			if err := d.UninstallApp(ctx, appID); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: uninstall failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("uninstall failed: %v", err)
			}
			return

		default:
			// No operational data — safe to remove config.
			appConfig.Status.Phase = AppPhaseDeleting
			appConfig.Status.Message = "Removing config"
			path := fmt.Sprintf("/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps/app=%s", appID)
			if err := d.client.Delete(ctx, path); err != nil {
				log.G(ctx).Warnf("ReconcileApp %s: config delete failed: %v", appID, err)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("config delete failed: %v", err)
				return
			}
			appConfig.Status.Phase = AppPhaseDeleted
			appConfig.Status.Message = "App fully removed"
			log.G(ctx).Infof("ReconcileApp %s: fully deleted", appID)
			return
		}
	}
}

// getAppState returns the current operational state string for appID, or ""
// if the app has no oper data or the state cannot be determined.
func (d *XEDriver) getAppState(ctx context.Context, appID string) string {
	if d.client == nil {
		return ""
	}
	allOper, err := d.GetAppOperationalData(ctx)
	if err != nil {
		log.G(ctx).Warnf("Could not fetch oper data to check state of app %s: %v", appID, err)
		return ""
	}
	operData, ok := allOper[appID]
	if !ok || operData == nil || operData.Details == nil || operData.Details.State == nil {
		return ""
	}
	return *operData.Details.State
}

// DeleteApp orchestrates a reconciler-driven teardown of the app lifecycle.
//
// It creates a transient AppHostingConfig with DesiredState=Deleted and
// repeatedly invokes ReconcileApp until the app reaches the Deleted phase
// or a timeout is exceeded.
//
//	RUNNING  → stop → ACTIVATED → deactivate → DEPLOYED → uninstall → (absent) → config delete
func (d *XEDriver) DeleteApp(ctx context.Context, appID string) error {
	appConfig := &AppHostingConfig{
		Metadata: AppHostingMetadata{AppName: appID},
		Spec:     AppHostingSpec{DesiredState: AppDesiredStateDeleted},
		Status:   AppHostingStatus{Phase: AppPhaseDeleting},
	}

	const maxAttempts = 15
	const reconcileInterval = 4 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		d.ReconcileApp(ctx, appConfig)

		if appConfig.Status.Phase == AppPhaseDeleted {
			log.G(ctx).Infof("Successfully deleted app %s after %d reconcile pass(es)", appID, attempt)
			return nil
		}

		log.G(ctx).Debugf("DeleteApp %s: attempt %d/%d, phase=%s observed=%q msg=%s",
			appID, attempt, maxAttempts, appConfig.Status.Phase, appConfig.Status.ObservedState, appConfig.Status.Message)

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while deleting app %s", appID)
		case <-time.After(reconcileInterval):
		}
	}

	return fmt.Errorf("app %s not fully deleted after %d reconcile attempts (last phase: %s, observed: %q)",
		appID, maxAttempts, appConfig.Status.Phase, appConfig.Status.ObservedState)
}

// containerImagePath returns the image path for a named container in a pod spec.
func containerImagePath(pod *v1.Pod, containerName string) string {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == containerName {
			return pod.Spec.Containers[i].Image
		}
	}
	return ""
}
