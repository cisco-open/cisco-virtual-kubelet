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

package softwareupgrade

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

// WithManagedImageSourcePolicy marks image resolution as manager-controlled.
// The marker is deliberately scoped to one Resolve call so the same resolver
// can retain its backwards-compatible standalone behaviour for legacy leaf
// upgrades. Managed callers must apply it before ImageResolver.Resolve.
func WithManagedImageSourcePolicy(ctx context.Context) context.Context {
	return context.WithValue(ctx, managedImageSourcePolicyKey{}, managedImageSourcePolicy{})
}

type managedImageSourcePolicyKey struct{}

type managedImageSourcePolicy struct {
	lookupNetIP     managedLookupNetIPFunc
	dialContext     managedDialContextFunc
	sourceSecretUID string
}

type managedLookupNetIPFunc func(context.Context, string) ([]netip.Addr, error)
type managedDialContextFunc func(context.Context, string, string) (net.Conn, error)

var (
	errManagedImageSourceRedirect          = errors.New("managed image source redirects are disabled")
	errManagedImageSourceDestinationDenied = errors.New("managed image source destination is not permitted")
	managedMetadataAddresses               = map[netip.Addr]struct{}{
		netip.MustParseAddr("100.100.100.200"): {}, // Alibaba Cloud metadata
		netip.MustParseAddr("168.63.129.16"):   {}, // Azure platform virtual IP
		netip.MustParseAddr("fd00:ec2::254"):   {}, // AWS IPv6 IMDS
		netip.MustParseAddr("fd20:ce::254"):    {}, // Google Cloud IPv6 metadata
	}
	managedSpecialUsePrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("::/96"),
		netip.MustParsePrefix("64:ff9b::/96"),
		netip.MustParsePrefix("64:ff9b:1::/48"),
		netip.MustParsePrefix("2001::/32"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2002::/16"),
		netip.MustParsePrefix("fec0::/10"),
	}
)

func managedImageSourcePolicyFromContext(ctx context.Context) (managedImageSourcePolicy, bool) {
	if ctx == nil {
		return managedImageSourcePolicy{}, false
	}
	policy, ok := ctx.Value(managedImageSourcePolicyKey{}).(managedImageSourcePolicy)
	return policy, ok
}

// withManagedImageSourceNetworkHooks is intentionally unexported. It makes
// the security boundary deterministic in unit tests without adding a runtime
// configuration surface for bypassing destination validation.
func withManagedImageSourceNetworkHooks(
	ctx context.Context,
	lookup managedLookupNetIPFunc,
	dial managedDialContextFunc,
) context.Context {
	policy, ok := managedImageSourcePolicyFromContext(ctx)
	if !ok {
		panic("managed image source network hooks require the managed policy marker")
	}
	policy.lookupNetIP = lookup
	policy.dialContext = dial
	return context.WithValue(ctx, managedImageSourcePolicyKey{}, policy)
}

// withManagedImageSourceSecretIdentity binds the credential object consumed
// by the resolver to the exact frozen manager-approved incarnation. The
// resolver rechecks this after its own Secret fetch, closing the validation to
// use/cache race between managed leaf admission and transport construction.
func withManagedImageSourceSecretIdentity(ctx context.Context, uid string) context.Context {
	policy, ok := managedImageSourcePolicyFromContext(ctx)
	if !ok {
		panic("managed image source Secret identity requires the managed policy marker")
	}
	policy.sourceSecretUID = strings.TrimSpace(uid)
	return context.WithValue(ctx, managedImageSourcePolicyKey{}, policy)
}

func validateManagedImageSource(u *url.URL, src opsv1alpha1.UpgradeImageSource) error {
	if u == nil {
		return errors.New("managed image source URL is invalid")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "sftp" {
		return fmt.Errorf("managed image source URL scheme %q is not permitted; use HTTPS or SFTP", u.Scheme)
	}
	if u.User != nil {
		return errors.New("managed image source URL must not contain user information; use an endpoint-bound urlSecretRef")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return errors.New("managed image source URL must not contain a query string")
	}
	if u.Fragment != "" {
		return errors.New("managed image source URL must not contain a fragment")
	}
	if u.Hostname() == "" {
		return errors.New("managed image source URL host is required")
	}

	switch scheme {
	case "https":
		if src.URLSecretRef != nil {
			return errors.New("managed HTTPS image sources are anonymous and must not specify urlSecretRef")
		}
		if addr, err := netip.ParseAddr(u.Hostname()); err == nil {
			if err := validateManagedResolvedAddress(addr, false); err != nil {
				return fmt.Errorf("managed HTTPS image source destination is not permitted: %w", err)
			}
		}
	case "sftp":
		if src.URLSecretRef == nil || strings.TrimSpace(src.URLSecretRef.Name) == "" {
			return errors.New("managed SFTP image source requires an endpoint-bound urlSecretRef")
		}
		if addr, err := netip.ParseAddr(u.Hostname()); err == nil {
			if err := validateManagedResolvedAddress(addr, true); err != nil {
				return fmt.Errorf("managed SFTP image source destination is not permitted: %w", err)
			}
		}
	}
	return nil
}

func validateManagedSSHURL(u *url.URL, ref *corev1.LocalObjectReference) error {
	return validateManagedImageSource(u, opsv1alpha1.UpgradeImageSource{URLSecretRef: ref})
}

func managedHTTPClient(ctx context.Context, base *http.Client, u *url.URL) (*http.Client, error) {
	_, ok := managedImageSourcePolicyFromContext(ctx)
	if !ok {
		return base, nil
	}
	if base == nil {
		base = http.DefaultClient
	}

	var configured *http.Transport
	switch baseTransport := base.Transport.(type) {
	case nil:
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, errors.New("managed HTTPS image source: default HTTP transport is not enforceable")
		}
		configured = defaultTransport
	case *http.Transport:
		configured = baseTransport
	default:
		return nil, errors.New("managed HTTPS image source requires an enforceable *http.Transport")
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if configuredTLS := configured.TLSClientConfig; configuredTLS != nil {
		if configuredTLS.InsecureSkipVerify {
			return nil, errors.New("managed HTTPS image source requires TLS certificate verification")
		}
		if configuredTLS.MaxVersion != 0 && configuredTLS.MaxVersion < tls.VersionTLS12 {
			return nil, errors.New("managed HTTPS image source requires TLS 1.2 or newer")
		}
		if configuredTLS.MinVersion > tlsConfig.MinVersion {
			tlsConfig.MinVersion = configuredTLS.MinVersion
		}
		tlsConfig.MaxVersion = configuredTLS.MaxVersion
		if tlsConfig.MaxVersion != 0 && tlsConfig.MinVersion > tlsConfig.MaxVersion {
			return nil, errors.New("managed HTTPS image source has an invalid TLS version range")
		}
		if configuredTLS.RootCAs != nil {
			tlsConfig.RootCAs = configuredTLS.RootCAs.Clone()
		}
	}
	// Bind certificate verification to the source endpoint even when the base
	// client had a ServerName intended for some other service. Build from a
	// minimal allowlist (trust roots + TLS version floor) so client certificates,
	// key logs, verification callbacks, session caches, and future extension
	// hooks on a shared base config cannot leak secrets or make side connections.
	tlsConfig.ServerName = strings.TrimSuffix(u.Hostname(), ".")

	dialContext, err := managedEndpointDialer(ctx, u, "443", false)
	if err != nil {
		return nil, err
	}
	// Construct a fresh standard transport rather than cloning hidden alternate
	// protocol handlers registered on the base transport. Those handlers are
	// arbitrary RoundTrippers and could bypass the pinned dialer just like a
	// custom top-level transport. Only inert tuning knobs and verified TLS roots
	// are inherited.
	transport := &http.Transport{
		DialContext:            dialContext,
		TLSClientConfig:        tlsConfig,
		TLSHandshakeTimeout:    configured.TLSHandshakeTimeout,
		DisableKeepAlives:      configured.DisableKeepAlives,
		DisableCompression:     configured.DisableCompression,
		MaxIdleConns:           configured.MaxIdleConns,
		MaxIdleConnsPerHost:    configured.MaxIdleConnsPerHost,
		MaxConnsPerHost:        configured.MaxConnsPerHost,
		IdleConnTimeout:        configured.IdleConnTimeout,
		ResponseHeaderTimeout:  configured.ResponseHeaderTimeout,
		ExpectContinueTimeout:  configured.ExpectContinueTimeout,
		MaxResponseHeaderBytes: configured.MaxResponseHeaderBytes,
		WriteBufferSize:        configured.WriteBufferSize,
		ReadBufferSize:         configured.ReadBufferSize,
		ForceAttemptHTTP2:      configured.ForceAttemptHTTP2,
	}

	managed := *base
	managed.Transport = transport
	managed.Jar = nil
	managed.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errManagedImageSourceRedirect
	}
	return &managed, nil
}

func managedEndpointDialer(
	ctx context.Context,
	u *url.URL,
	defaultPort string,
	allowPrivate bool,
) (func(context.Context, string, string) (net.Conn, error), error) {
	policy, ok := managedImageSourcePolicyFromContext(ctx)
	if !ok {
		return nil, errors.New("managed image source policy marker is absent")
	}
	expectedHost, err := canonicalEndpointHost(u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("managed image source URL host is invalid: %w", err)
	}
	expectedPort := u.Port()
	if expectedPort == "" {
		expectedPort = defaultPort
	}

	lookup := policy.lookupNetIP
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	dial := policy.dialContext
	if dial == nil {
		dialer := &net.Dialer{}
		dial = dialer.DialContext
	}
	addresses, err := resolveManagedEndpointOnce(ctx, expectedHost, lookup)
	if err != nil {
		return nil, classifyConnectionFailure(err)
	}
	for _, addr := range addresses {
		if err := validateManagedResolvedAddress(addr, allowPrivate); err != nil {
			// Reject the complete DNS response rather than selecting only a
			// safe-looking answer that may change on a subsequent connection.
			return nil, fmt.Errorf("managed image source resolved destination is not permitted: %w", err)
		}
	}

	return func(dialCtx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, errors.New("managed image source transport attempted an unexpected network")
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("managed image source dial endpoint is malformed")
		}
		canonicalHost, err := canonicalEndpointHost(host)
		if err != nil || canonicalHost != expectedHost || port != expectedPort {
			return nil, errors.New("managed image source transport attempted an unexpected endpoint")
		}

		var dialErrors []error
		for _, addr := range addresses {
			if network == "tcp4" && !addr.Is4() {
				continue
			}
			if network == "tcp6" && !addr.Is6() {
				continue
			}
			conn, err := dial(dialCtx, network, net.JoinHostPort(addr.String(), expectedPort))
			if err == nil {
				return conn, nil
			}
			dialErrors = append(dialErrors, err)
		}
		if len(dialErrors) == 0 {
			return nil, errors.New("managed image source has no address compatible with the requested network")
		}
		return nil, errors.Join(dialErrors...)
	}, nil
}

func resolveManagedEndpointOnce(
	ctx context.Context,
	host string,
	lookup managedLookupNetIPFunc,
) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{literal.Unmap()}, nil
	}
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("managed image source DNS lookup for %s failed: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("managed image source DNS lookup for %s returned no addresses", host)
	}

	unique := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		unique = append(unique, address)
	}
	return unique, nil
}

func validateManagedResolvedAddress(addr netip.Addr, allowPrivate bool) error {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.Zone() != "" {
		return fmt.Errorf("%w: invalid IP address", errManagedImageSourceDestinationDenied)
	}
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || !addr.IsGlobalUnicast() {
		return fmt.Errorf("%w: address %s is local, multicast, unspecified, or non-unicast", errManagedImageSourceDestinationDenied, addr)
	}
	if managedMetadataAddress(addr) {
		return fmt.Errorf("%w: address %s is reserved for instance/platform metadata", errManagedImageSourceDestinationDenied, addr)
	}
	if managedSpecialUseAddress(addr) {
		return fmt.Errorf("%w: address %s is not a permitted artifact endpoint", errManagedImageSourceDestinationDenied, addr)
	}
	if addr.IsPrivate() && !allowPrivate {
		return fmt.Errorf("%w: private address %s requires explicit endpoint authorization", errManagedImageSourceDestinationDenied, addr)
	}
	return nil
}

func managedMetadataAddress(addr netip.Addr) bool {
	_, found := managedMetadataAddresses[addr]
	return found
}

func managedSpecialUseAddress(addr netip.Addr) bool {
	for _, prefix := range managedSpecialUsePrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
