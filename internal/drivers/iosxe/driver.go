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
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/openconfig/ygot/ygot"
	"github.com/openconfig/ygot/ytypes"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/record"
)

// UnmarshalFunc defines a function signature for unmarshalling data
type UnmarshalFunc func([]byte, any) error

// XEDriver implements the device driver for Cisco IOS-XE AppHosting
type XEDriver struct {
	config       *v1alpha1.DeviceSpec
	client       common.NetworkClient
	marshaller   func(any) ([]byte, error)
	unmarshaller UnmarshalFunc
	deviceInfo   *common.DeviceInfo

	secretLister    corev1listers.SecretNamespaceLister
	configMapLister corev1listers.ConfigMapNamespaceLister
	recoveryMu      sync.RWMutex
	recoveringPods  map[string]bool // keyed by pod UID

	installMu       sync.Mutex
	installInFlight map[string]bool // keyed by appID; prevents duplicate background recovery installs

	eventRecorder record.EventRecorder
}

// NewAppHostingDriver creates a new IOS-XE AppHosting driver instance
func NewAppHostingDriver(ctx context.Context, spec *v1alpha1.DeviceSpec) (*XEDriver, error) {
	u := &url.URL{
		Host: fmt.Sprintf("%s:%d", spec.Address, spec.Port),
	}

	if spec.TLS != nil && spec.TLS.Enabled {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
	}

	if spec.TLS != nil {
		tlsConfig.InsecureSkipVerify = spec.TLS.InsecureSkipVerify

		if spec.TLS.CertFile != "" && spec.TLS.KeyFile != "" {
			cert, err := tls.LoadX509KeyPair(spec.TLS.CertFile, spec.TLS.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load client certificate: %v", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}

		if spec.TLS.CAFile != "" {
			caCert, err := os.ReadFile(spec.TLS.CAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA certificate: %v", err)
			}
			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse CA certificate")
			}
			tlsConfig.RootCAs = caCertPool
		}
	}

	BaseUrl := u.String()
	Timeout := 10 * time.Minute
	Client, err := common.NewNetworkClient(
		BaseUrl,
		&common.ClientAuth{
			Method:   "BasicAuth",
			Username: spec.Username,
			Password: spec.Password,
		},
		tlsConfig,
		Timeout,
	)

	d := &XEDriver{
		config:          spec,
		client:          Client,
		recoveringPods:  make(map[string]bool),
		installInFlight: make(map[string]bool),
	}

	protocol := "restconf"
	if protocol == "restconf" {
		d.marshaller = d.getRestconfMarshaller()
		d.unmarshaller = d.getRestconfUnmarshaller()
	}

	err = d.CheckConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to validate device connection: %v", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"url":      BaseUrl,
		"platform": "IOS-XE",
	}).Info("Connected to IOSXE device")

	if spec.AllowUnsignedApps {
		if err := d.ConfigureSignVerification(ctx, false); err != nil {
			log.G(ctx).WithError(err).Warn("allowUnsignedApps=true but failed to disable sign-verification on device; unsigned installs may be blocked")
		}
	}

	return d, nil
}

// gethostMetaUnmarshaller returns an unmarshaller for host-meta XML responses
func (d *XEDriver) gethostMetaUnmarshaller() UnmarshalFunc {
	return func(data []byte, v any) error {
		decoder := xml.NewDecoder(bytes.NewReader(data))
		decoder.Strict = false
		return decoder.Decode(v)
	}
}

// getRestconfMarshaller returns a marshaller for RESTCONF JSON payloads using ygot
func (d *XEDriver) getRestconfMarshaller() func(any) ([]byte, error) {
	return func(v any) ([]byte, error) {
		gs, ok := v.(ygot.GoStruct)
		if !ok {
			return nil, fmt.Errorf("value is not a ygot.GoStruct")
		}
		jsonStr, err := ygot.EmitJSON(gs, &ygot.EmitJSONConfig{
			Format: ygot.RFC7951,
			RFC7951Config: &ygot.RFC7951JSONConfig{
				AppendModuleName: true,
			},
			SkipValidation: true,
		})
		return []byte(jsonStr), err
	}
}

// getRestconfUnmarshaller returns an unmarshaller for RESTCONF JSON responses using ygot
func (d *XEDriver) getRestconfUnmarshaller() UnmarshalFunc {
	return func(data []byte, v any) error {
		// An empty or whitespace-only body is valid for RESTCONF — it means
		// the resource exists but has no data (e.g. no app configs when the
		// only app is in DEPLOYED state).  Return nil so the caller sees a
		// zero-value struct and can handle it normally.
		if len(bytes.TrimSpace(data)) == 0 {
			return nil
		}

		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return fmt.Errorf("failed to parse JSON wrapper: %w", err)
		}

		var innerData []byte
		if len(wrapper) == 1 {
			for _, val := range wrapper {
				innerData = val
			}
		} else {
			innerData = data
		}

		gs, ok := v.(ygot.GoStruct)
		if !ok {
			return fmt.Errorf("target is not a ygot.GoStruct")
		}

		// IOS XE oper models can gain additional leaves between software
		// releases. Keep typed fields we know about and ignore unknown extras
		// so node/pod status remains usable after an image upgrade.
		return Unmarshal(innerData, gs, &ytypes.IgnoreExtraFields{})
	}
}

// markPodRecovering marks a pod as currently in copy-recovery mode.
func (d *XEDriver) markPodRecovering(podUID string) {
	d.recoveryMu.Lock()
	defer d.recoveryMu.Unlock()
	d.recoveringPods[podUID] = true
}

// clearPodRecovering removes a pod from recovery mode.
func (d *XEDriver) clearPodRecovering(podUID string) {
	d.recoveryMu.Lock()
	defer d.recoveryMu.Unlock()
	delete(d.recoveringPods, podUID)
}

// isPodRecovering returns true while a pod is in copy-recovery mode.
func (d *XEDriver) isPodRecovering(podUID string) bool {
	d.recoveryMu.RLock()
	defer d.recoveryMu.RUnlock()
	return d.recoveringPods[podUID]
}

// tryMarkInstallInFlight atomically reserves the right to drive a recovery
// install for the given appID. Returns true if the caller has now claimed
// the slot and must release it via clearInstallInFlight when done; returns
// false if another goroutine already holds it. Used by GetPodStatus to
// dedupe per-app recovery attempts across status cycles.
func (d *XEDriver) tryMarkInstallInFlight(appID string) bool {
	d.installMu.Lock()
	defer d.installMu.Unlock()
	if d.installInFlight[appID] {
		return false
	}
	d.installInFlight[appID] = true
	return true
}

// clearInstallInFlight releases an in-flight install slot.
func (d *XEDriver) clearInstallInFlight(appID string) {
	d.installMu.Lock()
	defer d.installMu.Unlock()
	delete(d.installInFlight, appID)
}

// SetEventRecorder wires a Kubernetes event recorder into the driver so it can
// emit pod-scoped events visible via kubectl describe pod.
func (d *XEDriver) SetEventRecorder(r record.EventRecorder) {
	d.eventRecorder = r
}
