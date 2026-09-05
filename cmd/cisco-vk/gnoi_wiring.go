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

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/virtual-kubelet/virtual-kubelet/log"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/devicegrpc"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoiruntime"
	"github.com/cisco/virtual-kubelet-cisco/internal/tlsutil"
)

// gNOIDisabledEnv lets operators force-disable the gNOI pillar even
// when the binary has it linked in. Useful for incremental rollout —
// matches the CISCO_VK_TELEMETRY_INSECURE escape-hatch pattern.
const gNOIDisabledEnv = "CISCO_VK_GNOI_DISABLED"

// gNOIPortEnv overrides the device-side gNOI listener port. Defaults
// follow the same secure/insecure heuristic the gNMI transport uses.
const gNOIPortEnv = "CISCO_VK_GNOI_PORT"

// gNOIInsecureEnv forces legacy configurations to use the insecure gNXI
// listener (typically port 50052), bypassing spec.tls.enabled inference. It
// cannot override an explicit gnoi.transportSecurity=tls contract.
const gNOIInsecureEnv = "CISCO_VK_GNOI_INSECURE"

const (
	gNOIProvisioningMountPath     = "/var/run/secrets/cisco-vk/gnoi-provisioning"
	gNOIProvisioningCertFile      = "tls.crt"
	gNOIProvisioningCAKeyFile     = "ca.key"
	gNOIProvisioningCAFile        = "ca.crt"
	gNOIProvisioningBootstrapFile = "bootstrap.crt"
)

// setupGNOI builds the per-device gRPC runtime. A successfully configured base
// provider has no certificate-install authority; that authority is returned
// separately and only when both the write-class gate and local signer material
// are present.
//
// Returns (nil, nil, nil, nil) when:
//   - The device spec is missing the address (defensive — usually
//     caught earlier in startup).
//   - Operators have set CISCO_VK_GNOI_DISABLED=1 to opt out.
//
// A nil gnoi.Provider signals to the reconcilers that the gNOI
// dispatch path is unavailable; they fail fast with reason
// GNOIUnsupported on any CR they receive.
func setupGNOI(ctx context.Context, opts configReconcilerOptions) (gnoi.Provider, *gnoiruntime.Provisioner, func(), error) {
	return setupGNOIWithProvisioningDirectory(ctx, opts, gNOIProvisioningMountPath)
}

// setupGNOIWithProvisioningDirectory is the testable composition boundary for
// the projected provisioning Secret. Production always passes the fixed,
// read-only mount path above.
func setupGNOIWithProvisioningDirectory(
	ctx context.Context,
	opts configReconcilerOptions,
	provisioningDirectory string,
) (gnoi.Provider, *gnoiruntime.Provisioner, func(), error) {
	if v := os.Getenv(gNOIDisabledEnv); v == "1" || strings.EqualFold(v, "true") {
		log.G(ctx).Info("gNOI pillar disabled by CISCO_VK_GNOI_DISABLED")
		return nil, nil, nil, nil
	}
	if opts.Spec == nil || opts.Spec.Address == "" {
		return nil, nil, nil, nil
	}

	forceInsecure := false
	if v := os.Getenv(gNOIInsecureEnv); v == "1" || strings.EqualFold(v, "true") {
		forceInsecure = true
	}

	port, tlsEnabled, err := gnoiTransportForSpec(opts.Spec, forceInsecure)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("gNOI: invalid transport config: %w", err)
	}

	dialCfg, err := gnoiDialConfig(opts.Spec, opts.Password, tlsEnabled)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("gNOI: TLS from spec: %w", err)
	}
	provisioningBundle, err := loadGNOIProvisioningBundle(
		opts.Spec,
		tlsEnabled,
		dialCfg.TLSConfig,
		provisioningDirectory,
		opts.EnableWriteClassGNOI,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("gNOI: certificate provisioning: %w", err)
	}
	var signer gnoi.CertificateSigner
	var signerErr error
	if provisioningBundle != nil && opts.EnableWriteClassGNOI {
		signer, signerErr = loadGNOILocalCertificateSigner(provisioningBundle, provisioningDirectory)
	}

	pool := devicegrpc.New(dialCfg, nil)
	key := devicegrpc.DeviceKey{Address: opts.Spec.Address, Port: port}
	provider, err := gnoiruntime.NewProvider(pool, key, dialCfg.AuthContext())
	if err != nil {
		_ = pool.Close()
		return nil, nil, nil, err
	}

	var provisioner *gnoiruntime.Provisioner
	if signerErr != nil {
		log.G(ctx).WithError(signerErr).Warn(
			"gNOI ProvisionCertificate is unavailable because the local ca.key signer could not be loaded; base gNOI remains enabled",
		)
	} else if provisioningBundle != nil && signer != nil && opts.EnableWriteClassGNOI {
		provisioner, err = gnoiruntime.NewProvisioner(provider, provisioningBundle, signer)
		if err != nil {
			provider.Close()
			return nil, nil, nil, err
		}
	} else if provisioningBundle != nil && opts.EnableWriteClassGNOI {
		log.G(ctx).Warnf("gNOI certificate provisioning is unavailable: %s is not mounted", gNOIProvisioningCAKeyFile)
	}

	log.G(ctx).Infof("gNOI: pillar enabled (%s:%d, tls=%v, lazy_bulk=true)", opts.Spec.Address, port, dialCfg.TLSConfig != nil)
	return provider, provisioner, provider.Close, nil
}

func loadGNOIProvisioningBundle(
	spec *ciskov1.DeviceSpec,
	tlsEnabled bool,
	tlsCfg *tls.Config,
	directory string,
	provisioningWritesEnabled bool,
) (*gnoi.ProvisioningBundle, error) {
	if spec == nil || spec.XE == nil || spec.XE.GNOI == nil || spec.XE.GNOI.CertificateProvisioning == nil {
		return nil, nil
	}
	if !tlsEnabled || tlsCfg == nil {
		return nil, fmt.Errorf("TLS transport is required")
	}
	if tlsCfg.InsecureSkipVerify {
		return nil, fmt.Errorf("verified TLS is required; insecureSkipVerify cannot be used with certificate provisioning")
	}
	provisioning := spec.XE.GNOI.CertificateProvisioning
	if !provisioning.ReplaceTargetCABundle {
		return nil, fmt.Errorf("replaceTargetCABundle must be true before replacing the shared gNXI/gNMI CA bundle")
	}
	leafPEM, err := os.ReadFile(filepath.Join(directory, gNOIProvisioningCertFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gNOIProvisioningCertFile, err)
	}
	caBundlePEM, err := os.ReadFile(filepath.Join(directory, gNOIProvisioningCAFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gNOIProvisioningCAFile, err)
	}
	var bootstrapPEM []byte
	if provisioningWritesEnabled {
		bootstrapPEM, err = readOptionalFile(filepath.Join(directory, gNOIProvisioningBootstrapFile))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", gNOIProvisioningBootstrapFile, err)
		}
	}
	bundle, err := gnoi.NewProvisioningBundle(
		provisioning.CertificateID,
		spec.Address,
		leafPEM,
		caBundlePEM,
	)
	if err != nil {
		return nil, err
	}

	if err := bundle.ConfigureClientTLS(tlsCfg, bootstrapPEM); err != nil {
		return nil, err
	}
	return bundle, nil
}

// loadGNOILocalCertificateSigner loads the optional private signing material
// independently from the public provisioning profile. A caller can therefore
// retain verified gNOI TLS and read-only operations when local signing is
// unavailable. The source bytes are cleared after the key has been parsed.
func loadGNOILocalCertificateSigner(bundle *gnoi.ProvisioningBundle, directory string) (gnoi.CertificateSigner, error) {
	if bundle == nil {
		return nil, nil
	}
	caKeyPEM, err := readOptionalFile(filepath.Join(directory, gNOIProvisioningCAKeyFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gNOIProvisioningCAKeyFile, err)
	}
	defer clear(caKeyPEM)
	if len(caKeyPEM) == 0 {
		return nil, nil
	}
	signer, err := gnoi.NewLocalCertificateSigner(bundle, caKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("load local signer from %s: %w", gNOIProvisioningCAKeyFile, err)
	}
	return signer, nil
}

func readOptionalFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func gnoiDialConfig(spec *ciskov1.DeviceSpec, password string, tlsEnabled bool) (devicegrpc.DialConfig, error) {
	dialCfg := devicegrpc.DialConfig{}
	if !explicitSecureGNOI(spec) {
		dialCfg.Username = spec.Username
		dialCfg.Password = password
	}
	if !tlsEnabled {
		return dialCfg, nil
	}

	// Shared device-client helper: honours spec.tls.caFile (RootCAs)
	// and the certFile/keyFile client pair in addition to
	// InsecureSkipVerify, matching the apphosting driver.
	tlsCfg, err := tlsutil.ClientTLSFromDeviceTLS(spec.TLS)
	if err != nil {
		return devicegrpc.DialConfig{}, err
	}
	dialCfg.TLSConfig = tlsCfg
	if explicitSecureGNOI(spec) {
		if tlsCfg.InsecureSkipVerify {
			return devicegrpc.DialConfig{}, fmt.Errorf("verified TLS is required for explicit secure gNOI; insecureSkipVerify is not permitted")
		}
		if (spec.Username == "") != (password == "") {
			return devicegrpc.DialConfig{}, fmt.Errorf("explicit secure gNOI password authentication requires both username and password, or neither when another authentication method is configured")
		}
		if spec.Username != "" {
			dialCfg.RPCCredentials = devicegrpc.NewIOSXEPasswordCredentials(spec.Username, password)
		}
	}
	return dialCfg, nil
}

func explicitSecureGNOI(spec *ciskov1.DeviceSpec) bool {
	return spec != nil && spec.GNOI != nil && spec.GNOI.TransportSecurity == ciskov1.GNOITransportSecurityTLS
}

type unavailableGNOIProvider struct {
	cause error
}

func (p unavailableGNOIProvider) GNOIClient(context.Context) (*gnoi.Client, error) {
	return nil, fmt.Errorf("gNOI unavailable: %w", p.cause)
}

// gnoiTransportForSpec resolves the effective gNOI port and transport security.
// A missing or zero-valued per-device block uses the historical resolver.
// Explicit fields affect only the gNOI connection.
func gnoiTransportForSpec(spec *ciskov1.DeviceSpec, forceInsecure bool) (int, bool, error) {
	if spec == nil {
		return 0, false, fmt.Errorf("nil DeviceSpec")
	}

	if err := spec.GNOI.Validate(); err != nil {
		return 0, false, err
	}
	if spec.XE != nil {
		if err := spec.XE.GNOI.Validate(spec.GNOI); err != nil {
			return 0, false, fmt.Errorf("invalid XE gNOI config: %w", err)
		}
	}
	if forceInsecure && explicitSecureGNOI(spec) {
		return 0, false, fmt.Errorf("CISCO_VK_GNOI_INSECURE cannot override explicit gnoi.transportSecurity=tls")
	}
	sharedTLS := spec.TLS != nil && spec.TLS.Enabled
	if port, ok := gnoiPortEnvOverride(); ok {
		return port, !forceInsecure && effectiveGNOITLS(spec.GNOI, sharedTLS), nil
	}
	if forceInsecure {
		// The legacy override selects the legacy listener as well as plaintext.
		// A custom plaintext port remains available through CISCO_VK_GNOI_PORT.
		return inferredGNOIPort(spec.Port, false), false, nil
	}
	if spec.GNOI == nil || (spec.GNOI.Port == 0 && (spec.GNOI.TransportSecurity == "" || spec.GNOI.TransportSecurity == ciskov1.GNOITransportSecurityAuto)) {
		return inferredGNOIPort(spec.Port, sharedTLS), sharedTLS, nil
	}

	tlsEnabled := effectiveGNOITLS(spec.GNOI, sharedTLS)
	if spec.GNOI.Port > 0 {
		return spec.GNOI.Port, tlsEnabled, nil
	}
	if tlsEnabled {
		return 9339, true, nil
	}
	return 50052, false, nil
}

func effectiveGNOITLS(config *ciskov1.GNOIConfig, sharedTLS bool) bool {
	return sharedTLS || (config != nil && config.TransportSecurity == ciskov1.GNOITransportSecurityTLS)
}

func inferredGNOIPort(port int, tlsEnabled bool) int {
	if port == 0 || port == 80 || port == 443 {
		if tlsEnabled {
			return 9339
		}
		return 50052
	}
	return port
}

func gnoiPortEnvOverride() (int, bool) {
	v := os.Getenv(gNOIPortEnv)
	if v == "" {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}
