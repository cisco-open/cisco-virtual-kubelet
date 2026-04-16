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
	"strings"
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
//	"INSTALLING"    → no-op (wait) or fail-fast   → Converging / Error
//	"STOPPED"       → start RPC                  → Converging
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
	obs := d.getAppObservation(ctx, appID)
	state := obs.State
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

		case "STOPPED":
			// STOPPED → start (can restart directly without re-activate)
			appConfig.Status.Phase = AppPhaseConverging
			appConfig.Status.Message = "Restarting stopped app"
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

		case "INSTALLING":
			// Install is in progress on the device — wait for it to finish.
			// Re-issuing the install RPC would restart the tar extraction
			// and prevent the install from ever completing on slow devices.

			// Detect signature-validation failures: when the device requires
			// signed packages but the tar is unsigned, the install gets stuck
			// in INSTALLING with pkg-policy "invalid". Fail fast instead of
			// waiting forever.
			if obs.PkgPolicy == Cisco_IOS_XEAppHostingOper_IoxPkgPolicy_iox_pkg_policy_invalid {
				msg := "app package policy is invalid (possible unsigned package on a device requiring signed packages)"
				if notif := d.getAppInstallNotification(ctx, appID); notif != "" {
					msg = strings.TrimSpace(notif)
				}
				log.G(ctx).Errorf("ReconcileApp %s: install blocked: %s", appID, msg)
				appConfig.Status.Phase = AppPhaseError
				appConfig.Status.Message = fmt.Sprintf("install blocked: %s", msg)
				return
			}

			appConfig.Status.Phase = AppPhaseConverging
			appConfig.Status.Message = "Install in progress, waiting"
			log.G(ctx).Infof("ReconcileApp %s: install in progress, waiting for DEPLOYED", appID)
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
				// A 404 means the config entry doesn't exist (e.g. the app
				// was only visible via oper data and had no config).  Since
				// the app is already absent from both oper and config, treat
				// this as a successful deletion.
				if strings.Contains(err.Error(), "404") {
					log.G(ctx).Infof("ReconcileApp %s: config already absent (404), treating as deleted", appID)
					appConfig.Status.Phase = AppPhaseDeleted
					appConfig.Status.Message = "App fully removed (config was absent)"
					return
				}
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

// appObservation holds the observed state and metadata for an app, collected
// from device operational data during a reconciliation step.
type appObservation struct {
	State     string
	PkgPolicy E_Cisco_IOS_XEAppHostingOper_IoxPkgPolicy
}

// getAppObservation returns the current operational state and package policy
// for appID.  If the app has no oper data the returned State is "".
func (d *XEDriver) getAppObservation(ctx context.Context, appID string) appObservation {
	if d.client == nil {
		return appObservation{}
	}
	allOper, err := d.GetAppOperationalData(ctx)
	if err != nil {
		log.G(ctx).Warnf("Could not fetch oper data to check state of app %s: %v", appID, err)
		return appObservation{}
	}
	operData, ok := allOper[appID]
	if !ok || operData == nil {
		return appObservation{}
	}
	obs := appObservation{PkgPolicy: operData.PkgPolicy}
	if operData.Details != nil && operData.Details.State != nil {
		obs.State = *operData.Details.State
	}
	return obs
}

// getAppInstallNotification returns the most recent install notification
// message for appID, or "" if none found.
func (d *XEDriver) getAppInstallNotification(ctx context.Context, appID string) string {
	if d.client == nil {
		return ""
	}
	path := "/restconf/data/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data?fields=app-notifications"
	root := &Cisco_IOS_XEAppHostingOper_AppHostingOperData{}
	if err := d.client.Get(ctx, path, root, d.getRestconfUnmarshaller()); err != nil {
		log.G(ctx).Debugf("Could not fetch app notifications: %v", err)
		return ""
	}
	for _, n := range root.AppNotifications {
		if n.AppId != nil && *n.AppId == appID && n.Message != nil {
			return *n.Message
		}
	}
	return ""
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
