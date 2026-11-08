// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package iosxe

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
	commonpb "github.com/openconfig/gnoi/types"
	"github.com/openconfig/ygot/ygot"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type VerifiedFiles interface {
	Put(context.Context, string, io.Reader, gnoi.PutOpts) error
	Get(context.Context, string, io.Writer) (*commonpb.HashType, error)
}

// VerifiedRuntime is wired only with authenticated TLS and durable, protected
// storage. Standard never creates this runtime or accesses its journal.
type VerifiedRuntime struct {
	Resolver *appartifact.Resolver
	Journal  appartifact.Journal
	Files    func(context.Context) (VerifiedFiles, error)
	Delivery VerifiedPackageDelivery
	mu       sync.Mutex
}

func VerifiedJournalName(device string) string {
	sum := sha256.Sum256([]byte(device))
	return "cvk-artifacts-" + hex.EncodeToString(sum[:8])
}
func (d *XEDriver) SetVerifiedRuntime(r *VerifiedRuntime) { d.verified = r }
func (d *XEDriver) VerifiedAppArtifactsEnabled() bool {
	return d.verified != nil && d.verified.Resolver != nil && (d.verified.Files != nil || d.verified.Delivery != nil) && d.verified.Journal.Maps != nil
}
func (d *XEDriver) verifiedMode() bool {
	return d.config != nil && string(d.config.AppArtifacts.EffectiveMode()) == "Verified"
}

func (d *XEDriver) deployVerified(ctx context.Context, pod *v1.Pod, secrets corev1listers.SecretNamespaceLister, configs corev1listers.ConfigMapNamespaceLister) (outcome error) {
	deviceWorkStarted := false
	defer func() {
		if outcome != nil && !deviceWorkStarted {
			outcome = devicecoordination.NoDeviceMutation(outcome)
		}
	}()
	if !d.VerifiedAppArtifactsEnabled() {
		return common.ErrVerifiedArtifactsUnsupported
	}
	if pod == nil || pod.UID == "" || len(pod.Spec.InitContainers) != 0 || len(pod.Spec.EphemeralContainers) != 0 {
		return fmt.Errorf("Verified requires Pod UID and regular containers only")
	}
	if pod.DeletionTimestamp != nil {
		return fmt.Errorf("terminating Pod cannot install artifacts")
	}
	r := d.verified
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	apps, artifacts, err := d.prepareVerifiedAppsLocked(ctx, pod, secrets, configs)
	if err != nil {
		return err
	}
	// Reject changed desired configuration before classifying the operation as
	// a possible device write. A rejected update must not strand a write fence.
	for i := range apps {
		identity, _, e := d.verifiedIdentity(&apps[i], artifacts[apps[i].ContainerName()])
		if e != nil {
			return e
		}
		record, _, e := r.Journal.Load(ctx, "app-"+appartifact.Digest([]byte(apps[i].AppName()))[7:])
		if e != nil {
			return e
		}
		if record.Identity != "" && record.Identity != identity {
			return fmt.Errorf("installation identity changed; replace the Pod")
		}
	}

	for i := range apps {
		// From this point, preserve the conservative fence on every error:
		// staging or lifecycle work may have reached the device.
		deviceWorkStarted = true
		app := &apps[i]
		a := artifacts[app.ContainerName()]
		if err = d.installVerified(ctx, app, a); err != nil {
			return fmt.Errorf("Verified app %s: %w", app.AppName(), err)
		}
	}
	return nil
}

// prepareVerifiedAppsLocked is read-only. Callers hold the runtime lock because
// conversion uses the driver's namespace-scoped resource listers.
func (d *XEDriver) prepareVerifiedAppsLocked(ctx context.Context, pod *v1.Pod, secrets corev1listers.SecretNamespaceLister, configs corev1listers.ConfigMapNamespaceLister) ([]AppHostingConfig, map[string]*appartifact.VerifiedArtifact, error) {
	if pod == nil || pod.UID == "" || len(pod.Spec.InitContainers) != 0 || len(pod.Spec.EphemeralContainers) != 0 {
		return nil, nil, fmt.Errorf("Verified requires Pod UID and regular containers only")
	}
	if pod.DeletionTimestamp != nil {
		return nil, nil, fmt.Errorf("terminating Pod cannot install artifacts")
	}
	if secrets == nil && d.verifiedSecretLister != nil {
		secrets = d.verifiedSecretLister.Secrets(pod.Namespace)
	}
	if configs == nil && d.verifiedConfigMapLister != nil {
		configs = d.verifiedConfigMapLister.ConfigMaps(pod.Namespace)
	}
	d.secretLister = secrets
	d.configMapLister = configs
	names := []string{}
	for _, c := range pod.Spec.Containers {
		if len(c.EnvFrom) != 0 {
			return nil, nil, fmt.Errorf("Verified requires explicit env entries; envFrom is unsupported")
		}
		if c.ImagePullPolicy == v1.PullNever {
			return nil, nil, fmt.Errorf("Verified remote artifacts do not support imagePullPolicy Never")
		}
		names = append(names, c.Name)
	}
	bindings, err := appartifact.ParseBindings(pod.Annotations[appartifact.BindingAnnotation], names)
	if err != nil {
		return nil, nil, err
	}
	apps, err := d.ConvertPodToAppConfigs(pod)
	if err != nil {
		return nil, nil, err
	}
	artifacts := map[string]*appartifact.VerifiedArtifact{}
	// Resolve every container before the first device mutation.
	for _, c := range pod.Spec.Containers {
		a, e := d.verified.Resolver.Resolve(ctx, bindings[c.Name].DescriptorDigest, c.Image)
		if e != nil {
			return nil, nil, e
		}
		if a.Descriptor.Package.Format != "docker-save-v1" {
			return nil, nil, fmt.Errorf("unsupported Verified IOS-XE package format")
		}
		artifacts[c.Name] = a
	}
	for i := range apps {
		for _, app := range apps[i].Spec.DeviceConfig.App {
			app.Start = ygot.Bool(false)
		}
	}
	return apps, artifacts, nil
}

func (d *XEDriver) verifiedIdentity(app *AppHostingConfig, a *appartifact.VerifiedArtifact) (string, []byte, error) {
	cfg, err := json.Marshal(app.Spec.DeviceConfig)
	if err != nil {
		return "", nil, err
	}
	return appartifact.Digest([]byte(d.config.Address + "\x00" + app.PodUID() + "\x00" + app.ContainerName() + "\x00" + a.DescriptorDigest + "\x00" + appartifact.Digest(cfg))), cfg, nil
}

// observeVerifiedRunning never takes a device write lease, updates a journal,
// stages bytes or sends a lifecycle RPC. A cancelled status request therefore
// cannot strand a write fence after a completed installation. Pending records
// and stopped applications still require guarded recovery, never blind replay.
func (d *XEDriver) observeVerifiedRunning(ctx context.Context, pod *v1.Pod) (bool, error) {
	if !d.VerifiedAppArtifactsEnabled() {
		return false, common.ErrVerifiedArtifactsUnsupported
	}
	r := d.verified
	if !r.mu.TryLock() {
		return false, fmt.Errorf("Verified installation or recovery is in progress")
	}
	defer r.mu.Unlock()
	apps, artifacts, err := d.prepareVerifiedAppsLocked(ctx, pod, nil, nil)
	if err != nil {
		return false, err
	}
	for i := range apps {
		app := &apps[i]
		identity, _, err := d.verifiedIdentity(app, artifacts[app.ContainerName()])
		if err != nil {
			return false, err
		}
		record, _, err := r.Journal.Load(ctx, "app-"+appartifact.Digest([]byte(app.AppName()))[7:])
		if err != nil {
			return false, err
		}
		if record.Identity != "" && record.Identity != identity {
			return false, fmt.Errorf("installation identity changed; replace the Pod")
		}
		if record.Phase == "deleted" {
			return false, fmt.Errorf("installation is blocked by journal phase deleted")
		}
		if record.Phase != "running" {
			return false, nil
		}
	}
	oper, err := d.GetAppOperationalData(ctx)
	if err != nil {
		return false, err
	}
	for i := range apps {
		app := oper[apps[i].AppName()]
		if app == nil || app.Details == nil || app.Details.State == nil {
			return false, fmt.Errorf("recorded application is absent; replace the Pod for recovery")
		}
		switch *app.Details.State {
		case "RUNNING":
		case "STOPPED", "ACTIVATED", "DEPLOYED":
			return false, nil
		default:
			return false, fmt.Errorf("recorded application state requires Pod replacement")
		}
	}
	return true, nil
}

func (d *XEDriver) installVerified(ctx context.Context, app *AppHostingConfig, a *appartifact.VerifiedArtifact) error {
	r := d.verified
	identity, cfg, err := d.verifiedIdentity(app, a)
	if err != nil {
		return err
	}
	key := "app-" + appartifact.Digest([]byte(app.AppName()))[7:]
	path := "flash:cvk-" + appartifact.Digest([]byte(app.PodUID()))[7:23] + "-" + a.Descriptor.Package.Digest[7:] + ".tar"
	record, version, err := r.Journal.Load(ctx, key)
	if err != nil {
		return err
	}
	if record.Identity != "" && record.Identity != identity {
		return fmt.Errorf("installation identity changed; replace the Pod")
	}
	switch record.Phase {
	case "", "staging", "configuring", "verified", "installPending", "installed", "activatePending", "activated", "startPending", "running":
	default:
		return fmt.Errorf("installation is blocked by journal phase %q", record.Phase)
	}
	save := func(phase string) error {
		record = appartifact.Installation{SourceImage: a.Descriptor.Source.Registry + "/" + a.Descriptor.Source.Repository + "@" + a.Descriptor.Source.ManifestDigest, Identity: identity, Phase: phase, Device: d.config.Address, PodUID: app.PodUID(), AppID: app.AppName(), DescriptorDigest: a.DescriptorDigest, PackageDigest: a.Descriptor.Package.Digest, StagedPath: path, ConfigDigest: appartifact.Digest(cfg)}
		if e := r.Journal.Save(ctx, key, version, record); e != nil {
			return e
		}
		var e error
		record, version, e = r.Journal.Load(ctx, key)
		return e
	}
	observe := func() (string, error) {
		o := d.getAppObservation(ctx, app.AppName())
		if !o.Valid {
			return "", fmt.Errorf("device observation unavailable")
		}
		return o.State, nil
	}
	if record.Phase == "" {
		state, e := observe()
		if e != nil {
			return e
		}
		existing, e := d.ListAppHostingApps(ctx)
		if e != nil {
			return e
		}
		for _, x := range existing {
			if x.ApplicationName != nil && *x.ApplicationName == app.AppName() {
				return fmt.Errorf("existing configuration has no installation record")
			}
		}
		if state != "" {
			return fmt.Errorf("existing application has no installation record")
		}
		if err = save("staging"); err != nil {
			return err
		}
	}
	if record.Phase == "running" {
		state, e := observe()
		if e != nil {
			return e
		}
		switch state {
		case "RUNNING":
			return nil
		case "STOPPED", "ACTIVATED":
			if err = save("activated"); err != nil {
				return err
			}
		case "DEPLOYED":
			if err = save("installed"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("recorded application is %q; replace Pod for recovery", state)
		}
	}
	var files VerifiedFiles
	if r.Delivery == nil {
		if r.Files == nil {
			return fmt.Errorf("Verified delivery is unavailable")
		}
		files, err = r.Files(ctx)
		if err != nil {
			return err
		}
	}
	expected := VerifiedPackageChecksums{Package: a.Descriptor.Package}
	var local *os.File
	// Copy's native SHA512 is derived only from SHA256-validated package bytes.
	// Recompute after worker replacement rather than trusting a cached filename.
	// A healthy running record above needs no package download/checksum query.
	if record.Phase == "staging" || r.Delivery != nil {
		local, err = r.Resolver.DownloadPackage(ctx, a)
		if err != nil {
			return err
		}
		defer os.Remove(local.Name())
		defer local.Close()
		if r.Delivery != nil {
			h := sha512.New()
			if _, err = io.Copy(h, local); err != nil {
				return err
			}
			expected.SHA512 = hex.EncodeToString(h.Sum(nil))
			if _, err = local.Seek(0, 0); err != nil {
				return err
			}
		}
	}
	verify := func() error {
		if r.Delivery != nil {
			return r.Delivery.Verify(ctx, path, expected)
		}
		return verifyStagedPackage(ctx, files, path, a.Descriptor.Package)
	}

	if record.Phase == "staging" {

		if r.Delivery != nil {
			err = r.Delivery.Stage(ctx, path, a.PackageURL, local)
		} else {
			err = files.Put(ctx, path, local, gnoi.PutOpts{Permissions: 0600})
		}
		if err != nil {
			return err
		}
		if err = verify(); err != nil {
			return err
		}

		if err = save("configuring"); err != nil {
			return err
		}
	}
	if record.Phase == "configuring" {
		if err = verify(); err != nil {
			return err
		}
		err = d.client.Post(ctx, "/restconf/data/Cisco-IOS-XE-app-hosting-cfg:app-hosting-cfg-data/apps", app.Spec.DeviceConfig, d.marshaller)
		if err != nil && !isRESTCONFDataExists(err) {
			return err
		}
		if err = save("verified"); err != nil {
			return err
		}
	}

	// Persist intent before every RPC. Pending operations are observed after a
	// restart, never replayed on the strength of a filename or RUNNING alone.
	for _, step := range []struct{ before, pending, after, operation, target string }{
		{"verified", "installPending", "installed", "install", "DEPLOYED"},
		{"installed", "activatePending", "activated", "activate", "ACTIVATED"},
		{"activated", "startPending", "running", "start", "RUNNING"},
	} {
		if record.Phase == step.before {
			if err = verify(); err != nil {
				return err
			}
			state, e := observe()
			if e != nil {
				return e
			}
			want := ""
			if step.operation == "activate" {
				want = "DEPLOYED"
			}
			if step.operation == "start" {
				want = "ACTIVATED"
			}
			if state != want && !(step.operation == "start" && state == "STOPPED") {
				return fmt.Errorf("unexpected state %q before %s", state, step.operation)
			}
			if err = save(step.pending); err != nil {
				return err
			}
			params := map[string]string{"appid": app.AppName()}
			if step.operation == "install" {
				params["package"] = path
			}
			result, e := d.postAppHostingRPC(ctx, map[string]any{step.operation: params})
			if e != nil {
				return e
			}
			if !appHostingLifecycleResultMatches(step.operation, app.AppName(), params["package"], result) {
				return fmt.Errorf("%s completion uncertain; durable intent retained", step.operation)
			}
		}
		if record.Phase == step.pending {
			if err = verify(); err != nil {
				return err
			}
			targets := []string{step.target}
			if step.operation == "activate" {
				targets = append(targets, "STOPPED")
			}
			if err = d.waitForAppStatuses(ctx, app.AppName(), defaultPackageTimeout, targets...); err != nil {
				return fmt.Errorf("pending %s: %w", step.operation, err)
			}
			if err = save(step.after); err != nil {
				return err
			}
		}
	}
	if record.Phase != "running" {
		return fmt.Errorf("unknown installation journal phase %q", record.Phase)
	}
	state, err := observe()
	if err != nil {
		return err
	}
	if state != "RUNNING" {
		return fmt.Errorf("recorded application is %q; replace Pod for recovery", state)
	}
	return nil
}

type packageHashWriter struct {
	remaining int64
	n         int64
	target    io.Writer
}

func (w *packageHashWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("device package exceeds declared size")
	}
	n, e := w.target.Write(p)
	w.remaining -= int64(n)
	w.n += int64(n)
	return n, e
}
func verifyStagedPackage(ctx context.Context, files VerifiedFiles, path string, p appartifact.Package) error {
	h := sha256.New()
	w := &packageHashWriter{remaining: p.SizeBytes, target: h}
	reported, err := files.Get(ctx, path, w)
	if err != nil {
		return err
	}
	got := h.Sum(nil)
	if w.n != p.SizeBytes || "sha256:"+hex.EncodeToString(got) != p.Digest || reported.GetMethod() != commonpb.HashType_SHA256 || hex.EncodeToString(reported.GetHash()) != hex.EncodeToString(got) {
		return fmt.Errorf("on-device package size or SHA256 mismatch")
	}
	return nil
}

func (d *XEDriver) verifiedPodStatus(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	result := pod.DeepCopy()
	running, err := d.observeVerifiedRunning(ctx, pod)
	if err == nil && !running {
		err = d.withMaintenanceMutation(ctx, func(writeCtx context.Context) error { return d.deployVerified(writeCtx, pod, nil, nil) })
	}

	result.Status.ContainerStatuses = nil
	result.Status.Phase = v1.PodRunning
	// These fields describe this observation, not an earlier provider failure.
	result.Status.Reason = ""
	result.Status.Message = ""
	ready := v1.ConditionTrue
	if err != nil {
		result.Status.Phase = v1.PodPending
		result.Status.Reason = "VerifiedInstallationBlocked"
		result.Status.Message = err.Error()
		ready = v1.ConditionFalse
	}
	result.Status.Conditions = []v1.PodCondition{{Type: v1.PodReady, Status: ready, LastTransitionTime: metav1.Now()}}
	result.Status.HostIP = d.config.Address
	if err == nil {
		// Preserve ordinary app-hosting network discovery for Services and
		// independent application probes; RUNNING alone does not provide an IP.
		if oper, e := d.GetAppOperationalData(ctx); e == nil {
			if ip := d.discoverPodIP(ctx, common.GenerateContainerAppIDs(pod), oper); isValidPodIP(ip) {
				result.Status.PodIP = ip
				result.Status.PodIPs = []v1.PodIP{{IP: ip}}
			}
		}
	}
	for _, c := range pod.Spec.Containers {
		status := v1.ContainerStatus{Name: c.Name, Image: c.Image, Ready: err == nil, ContainerID: common.GenerateContainerAppIDs(pod)[c.Name]}
		if err == nil {
			key := "app-" + appartifact.Digest([]byte(status.ContainerID))[7:]
			if record, _, e := d.verified.Journal.Load(ctx, key); e == nil && record.Phase == "running" {
				status.ImageID = record.SourceImage
			}
		}
		if err != nil {
			status.State.Waiting = &v1.ContainerStateWaiting{Reason: "VerifiedInstallationBlocked", Message: err.Error()}
		} else {
			status.State.Running = &v1.ContainerStateRunning{}
		}
		result.Status.ContainerStatuses = append(result.Status.ContainerStatuses, status)
	}
	return result, nil
}

// SetPodResourceListers supplies namespace-scoped configuration sources during
// recovery after process restart, before any DeployPod callback has run.
func (d *XEDriver) SetPodResourceListers(secrets corev1listers.SecretLister, configs corev1listers.ConfigMapLister) {
	d.verifiedSecretLister = secrets
	d.verifiedConfigMapLister = configs
}
