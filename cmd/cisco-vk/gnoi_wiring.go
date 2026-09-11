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
	"crypto/x509"
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
	if envEnabled(gNOIDisabledEnv) {
		log.G(ctx).Info("gNOI pillar disabled by CISCO_VK_GNOI_DISABLED")
		return nil, nil, nil, nil
	}
	if opts.Spec == nil || opts.Spec.Address == "" {
		return nil, nil, nil, nil
	}

	forceInsecure := envEnabled(gNOIInsecureEnv)
	resolved, err := resolveGNOIConfig(
		opts.Spec,
		opts.Password,
		forceInsecure,
		provisioningDirectory,
		opts.EnableWriteClassGNOI,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("gNOI: resolve configuration: %w", err)
	}
	var signer gnoi.CertificateSigner
	var signerErr error
	if resolved.provisioningBundle != nil && opts.EnableWriteClassGNOI {
		signer, signerErr = loadGNOILocalCertificateSigner(resolved.provisioningBundle, provisioningDirectory)
	}

	pool := devicegrpc.New(resolved.dialConfig, nil)
	key := devicegrpc.DeviceKey{Address: opts.Spec.Address, Port: resolved.port}
	provider, err := gnoiruntime.NewProvider(pool, key, resolved.dialConfig.AuthContext())
	if err != nil {
		_ = pool.Close()
		return nil, nil, nil, err
	}

	var provisioner *gnoiruntime.Provisioner
	if signerErr != nil {
		log.G(ctx).WithError(signerErr).Warn(
			"gNOI ProvisionCertificate is unavailable because the local ca.key signer could not be loaded; base gNOI remains enabled",
		)
	} else if resolved.provisioningBundle != nil && signer != nil && opts.EnableWriteClassGNOI {
		provisioner, err = gnoiruntime.NewProvisioner(provider, resolved.provisioningBundle, signer)
		if err != nil {
			provider.Close()
			return nil, nil, nil, err
		}
	} else if resolved.provisioningBundle != nil && opts.EnableWriteClassGNOI {
		log.G(ctx).Warnf("gNOI certificate provisioning is unavailable: %s is not mounted", gNOIProvisioningCAKeyFile)
	}

	log.G(ctx).Infof(
		"gNOI: pillar enabled (%s:%d, tls=%v, trust_source=%s, auth_mode=%s, lazy_bulk=true)",
		opts.Spec.Address,
		resolved.port,
		resolved.dialConfig.TLSConfig != nil,
		resolved.trustSource,
		resolved.authMode(),
	)
	return provider, provisioner, provider.Close, nil
}

func envEnabled(name string) bool {
	v := os.Getenv(name)
	return v == "1" || strings.EqualFold(v, "true")
}

func loadGNOIProvisioningBundle(
	spec *ciskov1.DeviceSpec,
	tlsCfg *tls.Config,
	directory string,
	provisioningWritesEnabled bool,
) (*gnoi.ProvisioningBundle, error) {
	if spec == nil || spec.XE == nil || spec.XE.GNOI == nil || spec.XE.GNOI.CertificateProvisioning == nil {
		return nil, nil
	}
	if tlsCfg == nil {
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

type gnoiTrustSource string

const (
	gnoiTrustSourcePlaintext    gnoiTrustSource = "plaintext"
	gnoiTrustSourceLegacyShared gnoiTrustSource = "legacy-shared"
	gnoiTrustSourceSystem       gnoiTrustSource = "system"
	gnoiTrustSourceShared       gnoiTrustSource = "shared"
	gnoiTrustSourceDedicated    gnoiTrustSource = "gnoi"
	gnoiTrustSourceProvisioning gnoiTrustSource = "xe-provisioning"
)

// resolvedGNOIConfig is the single output of gNOI transport resolution. Port,
// TLS policy, authentication policy, and provisioning trust are deliberately
// resolved together so callers cannot accidentally combine decisions produced
// by independent helpers.
type resolvedGNOIConfig struct {
	port               int
	dialConfig         devicegrpc.DialConfig
	provisioningBundle *gnoi.ProvisioningBundle
	trustSource        gnoiTrustSource
}

func (c resolvedGNOIConfig) authMode() string {
	if c.dialConfig.RPCCredentials != nil {
		return "iosxe-password-metadata"
	}
	if c.dialConfig.Username != "" {
		return "legacy-basic"
	}
	return "none"
}

// resolveGNOIConfig preserves historical inference and Basic metadata only for
// configurations that have not explicitly selected secure gNOI. Explicit TLS
// always uses verified transport and IOS XE per-RPC password credentials. A
// dedicated gNOI TLS block and IOS XE certificate provisioning are alternate,
// mutually exclusive sources of gNOI-only trust.
func resolveGNOIConfig(
	spec *ciskov1.DeviceSpec,
	password string,
	forceInsecure bool,
	provisioningDirectory string,
	provisioningWritesEnabled bool,
) (resolvedGNOIConfig, error) {
	if spec == nil {
		return resolvedGNOIConfig{}, fmt.Errorf("nil DeviceSpec")
	}
	if err := spec.GNOI.Validate(); err != nil {
		return resolvedGNOIConfig{}, err
	}
	provisioning := xeGNOICertificateProvisioning(spec)
	if provisioning != nil && spec.Driver != ciskov1.DeviceDriverXE {
		return resolvedGNOIConfig{}, fmt.Errorf("gNOI certificate provisioning is supported only for driver XE; driver %q requires its own provisioning adapter", spec.Driver)
	}
	if spec.XE != nil {
		if err := spec.XE.GNOI.Validate(spec.GNOI); err != nil {
			return resolvedGNOIConfig{}, fmt.Errorf("invalid XE gNOI config: %w", err)
		}
	}

	explicitTLS := spec.GNOI != nil && spec.GNOI.TransportSecurity == ciskov1.GNOITransportSecurityTLS
	dedicatedTLS := spec.GNOI != nil && spec.GNOI.TLS != nil
	if dedicatedTLS && provisioning != nil {
		return resolvedGNOIConfig{}, fmt.Errorf("spec.gnoi.tls and spec.xe.gnoi.certificateProvisioning are mutually exclusive trust sources")
	}
	if forceInsecure && explicitTLS {
		return resolvedGNOIConfig{}, fmt.Errorf("%s cannot override explicit spec.gnoi.transportSecurity=tls", gNOIInsecureEnv)
	}

	sharedTLS := spec.TLS != nil && spec.TLS.Enabled
	tlsEnabled := explicitTLS || (!forceInsecure && sharedTLS)
	port, err := resolveGNOIPort(spec, tlsEnabled, forceInsecure)
	if err != nil {
		return resolvedGNOIConfig{}, err
	}
	resolved := resolvedGNOIConfig{
		port:        port,
		trustSource: gnoiTrustSourcePlaintext,
	}

	if !explicitTLS {
		// Legacy gNOI sends Basic authorization through the provider context,
		// including on plaintext connections. Preserve this compatibility path
		// only until a device explicitly opts into secure gNOI.
		resolved.dialConfig.Username = spec.Username
		resolved.dialConfig.Password = password
		if !tlsEnabled {
			return resolved, nil
		}
		resolved.dialConfig.TLSConfig, err = tlsutil.ClientTLSFromDeviceTLS(spec.TLS)
		if err != nil {
			return resolvedGNOIConfig{}, fmt.Errorf("shared TLS: %w", err)
		}
		resolved.trustSource = gnoiTrustSourceLegacyShared
		return resolved, nil
	}

	if (spec.Username == "") != (password == "") {
		return resolvedGNOIConfig{}, fmt.Errorf("explicit secure gNOI password authentication requires both username and password, or neither when another authentication method is configured")
	}
	if spec.Username != "" && spec.Driver != ciskov1.DeviceDriverXE {
		return resolvedGNOIConfig{}, fmt.Errorf("explicit secure gNOI password authentication is supported only for driver XE; driver %q requires its own authentication adapter", spec.Driver)
	}

	var tlsCfg *tls.Config
	switch {
	case provisioning != nil:
		// Provisioning trust is isolated from DeviceSpec.TLS. The provisioning
		// bundle adds its validated CA chain and optional bootstrap leaf pin.
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: x509.NewCertPool()}
		resolved.trustSource = gnoiTrustSourceProvisioning
	case dedicatedTLS:
		tlsCfg, err = clientTLSFromGNOIConfig(spec.GNOI.TLS)
		if err != nil {
			return resolvedGNOIConfig{}, fmt.Errorf("spec.gnoi.tls: %w", err)
		}
		resolved.trustSource = gnoiTrustSourceDedicated
	default:
		tlsCfg, err = tlsutil.ClientTLSFromDeviceTLS(spec.TLS)
		if err != nil {
			return resolvedGNOIConfig{}, fmt.Errorf("shared TLS: %w", err)
		}
		if tlsCfg.InsecureSkipVerify {
			return resolvedGNOIConfig{}, fmt.Errorf("verified TLS is required for explicit secure gNOI; shared spec.tls.insecureSkipVerify cannot be inherited")
		}
		resolved.trustSource = gnoiTrustSourceSystem
		if spec.TLS != nil && (spec.TLS.CAFile != "" || spec.TLS.CertFile != "" || spec.TLS.KeyFile != "") {
			resolved.trustSource = gnoiTrustSourceShared
		}
	}

	resolved.dialConfig.TLSConfig = tlsCfg
	if spec.Username != "" {
		resolved.dialConfig.RPCCredentials = devicegrpc.NewIOSXEPasswordCredentials(spec.Username, password)
	}
	if provisioning != nil {
		resolved.provisioningBundle, err = loadGNOIProvisioningBundle(
			spec,
			tlsCfg,
			provisioningDirectory,
			provisioningWritesEnabled,
		)
		if err != nil {
			return resolvedGNOIConfig{}, fmt.Errorf("certificate provisioning: %w", err)
		}
	}
	return resolved, nil
}

func xeGNOICertificateProvisioning(spec *ciskov1.DeviceSpec) *ciskov1.XEGNOICertificateProvisioning {
	if spec == nil || spec.XE == nil || spec.XE.GNOI == nil {
		return nil
	}
	return spec.XE.GNOI.CertificateProvisioning
}

// clientTLSFromGNOIConfig adapts controller-resolved or local gNOI file paths
// to the shared hardened certificate loader. Secret references are rejected by
// GNOIConfig.Validate and must never cross the manager/worker boundary.
func clientTLSFromGNOIConfig(config *ciskov1.GNOITLSConfig) (*tls.Config, error) {
	if config == nil {
		return nil, fmt.Errorf("nil config")
	}
	if config.SecretRef != nil {
		return nil, fmt.Errorf("unresolved secretRef is not valid in worker configuration")
	}
	if config.CAFile == "" {
		return nil, fmt.Errorf("caFile is required")
	}
	return tlsutil.ClientTLSFromDeviceTLS(&ciskov1.TLSConfig{
		CAFile:   config.CAFile,
		CertFile: config.CertFile,
		KeyFile:  config.KeyFile,
	})
}

type unavailableGNOIProvider struct {
	cause error
}

func (p unavailableGNOIProvider) GNOIClient(context.Context) (*gnoi.Client, error) {
	return nil, fmt.Errorf("gNOI unavailable: %w", p.cause)
}

// resolveGNOIPort is intentionally subordinate to resolveGNOIConfig: no caller
// can use its port decision without also applying the resolved TLS and auth
// policy. Invalid environment overrides fail closed instead of being silently
// ignored.
func resolveGNOIPort(spec *ciskov1.DeviceSpec, tlsEnabled, forceInsecure bool) (int, error) {
	if port, set, err := gnoiPortEnvOverride(); err != nil {
		return 0, err
	} else if set {
		return port, nil
	}
	if forceInsecure {
		return inferredGNOIPort(spec.Port, false), nil
	}
	if spec.GNOI != nil && spec.GNOI.Port > 0 {
		return spec.GNOI.Port, nil
	}
	if spec.GNOI != nil && spec.GNOI.TransportSecurity == ciskov1.GNOITransportSecurityTLS {
		return inferredGNOIPort(0, true), nil
	}
	return inferredGNOIPort(spec.Port, tlsEnabled), nil
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

func gnoiPortEnvOverride() (int, bool, error) {
	raw := os.Getenv(gNOIPortEnv)
	if raw == "" {
		return 0, false, nil
	}
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || port <= 0 || port > 65535 {
		return 0, true, fmt.Errorf("%s must be an integer between 1 and 65535", gNOIPortEnv)
	}
	return port, true, nil
}
