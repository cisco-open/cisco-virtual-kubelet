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

package deviceoperation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
)

// isGNOIKind returns true when the operation kind is dispatched via
// the gNOI client rather than the CLI/diagnostic transport. Kept as a
// small helper so the main Reconcile body has a single branch point.
func isGNOIKind(kind opsv1alpha1.OperationKind) bool {
	switch kind {
	case opsv1alpha1.OperationKindGNOIPing,
		opsv1alpha1.OperationKindGNOITraceroute,
		opsv1alpha1.OperationKindGNOITime,
		opsv1alpha1.OperationKindGNOIFileGet,
		opsv1alpha1.OperationKindGNOIFileStat,
		opsv1alpha1.OperationKindGNOICertGet,
		opsv1alpha1.OperationKindGNOICanGenerateCSR,
		opsv1alpha1.OperationKindGNOIRebootStatus,
		opsv1alpha1.OperationKindGNOIOSVerify:
		return true
	}
	return false
}

// dispatchGNOI runs the gNOI operation and returns the structured
// output plus a per-operation success message. Errors flow through the
// returned error and the reconciler decides the terminal phase as it
// does for the CLI path.
func (r *Reconciler) dispatchGNOI(
	ctx context.Context,
	op *opsv1alpha1.DeviceOperation,
) (outputs []opsv1alpha1.DeviceOperationOutput, successMessage string, err error) {
	if r.GNOI == nil {
		return nil, "", errors.New("gnoi provider is not configured on this reconciler")
	}
	client, err := r.GNOI.GNOIClient(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("gnoi client: %w", err)
	}
	if client == nil {
		return nil, "", errors.New("gnoi provider returned nil client")
	}

	kind := op.Spec.Operation.Kind
	args := op.Spec.Operation.GNOI

	switch kind {
	case opsv1alpha1.OperationKindGNOIPing:
		return gnoiPing(ctx, client, args)
	case opsv1alpha1.OperationKindGNOITraceroute:
		return gnoiTraceroute(ctx, client, args)
	case opsv1alpha1.OperationKindGNOITime:
		return gnoiTime(ctx, client)
	case opsv1alpha1.OperationKindGNOIFileStat:
		return gnoiFileStat(ctx, client, args)
	case opsv1alpha1.OperationKindGNOIFileGet:
		return gnoiFileGet(ctx, client, args)
	case opsv1alpha1.OperationKindGNOICertGet:
		return gnoiCertGet(ctx, client)
	case opsv1alpha1.OperationKindGNOICanGenerateCSR:
		return gnoiCanGenerateCSR(ctx, client, args)
	case opsv1alpha1.OperationKindGNOIRebootStatus:
		return gnoiRebootStatus(ctx, client)
	case opsv1alpha1.OperationKindGNOIOSVerify:
		return gnoiOSVerify(ctx, client)
	default:
		return nil, "", fmt.Errorf("gnoi operation kind %q is not implemented", kind)
	}
}

// --- per-kind helpers ---

func gnoiPing(ctx context.Context, client *gnoi.Client, args *opsv1alpha1.GNOIArgs) ([]opsv1alpha1.DeviceOperationOutput, string, error) {
	if args == nil || args.Ping == nil || args.Ping.Destination == "" {
		return nil, "", errors.New("operation.gnoi.ping.destination is required")
	}
	res, err := client.Ping(ctx, args.Ping.Destination, gnoi.PingOpts{
		Source:   args.Ping.Source,
		Count:    args.Ping.Count,
		Interval: time.Duration(args.Ping.IntervalMillis) * time.Millisecond,
		Wait:     time.Duration(args.Ping.WaitMillis) * time.Millisecond,
		Size:     args.Ping.Size,
	})
	if err != nil {
		return nil, "", err
	}
	return jsonOutput(string(opsv1alpha1.OperationKindGNOIPing), res),
		fmt.Sprintf("%d ping reply/replies received", res.Summary.Received), nil
}

func gnoiTraceroute(ctx context.Context, client *gnoi.Client, args *opsv1alpha1.GNOIArgs) ([]opsv1alpha1.DeviceOperationOutput, string, error) {
	if args == nil || args.Traceroute == nil || args.Traceroute.Destination == "" {
		return nil, "", errors.New("operation.gnoi.traceroute.destination is required")
	}
	res, err := client.Traceroute(ctx, args.Traceroute.Destination, gnoi.TracerouteOpts{
		Source:   args.Traceroute.Source,
		MaxHops:  args.Traceroute.MaxHops,
		Wait:     time.Duration(args.Traceroute.WaitMillis) * time.Millisecond,
		Protocol: args.Traceroute.Protocol,
	})
	if err != nil {
		return nil, "", err
	}
	return jsonOutput(string(opsv1alpha1.OperationKindGNOITraceroute), res),
		fmt.Sprintf("%d hop(s) traced", len(res.Hops)), nil
}

func gnoiTime(ctx context.Context, client *gnoi.Client) ([]opsv1alpha1.DeviceOperationOutput, string, error) {
	t, err := client.Time(ctx)
	if err != nil {
		return nil, "", err
	}
	payload := struct {
		Time string `json:"time"`
	}{Time: t.UTC().Format(time.RFC3339Nano)}
	return jsonOutput(string(opsv1alpha1.OperationKindGNOITime), payload), "device time captured", nil
}

func gnoiFileStat(ctx context.Context, client *gnoi.Client, args *opsv1alpha1.GNOIArgs) ([]opsv1alpha1.DeviceOperationOutput, string, error) {
	if args == nil || args.File == nil || args.File.Path == "" {
		return nil, "", errors.New("operation.gnoi.file.path is required")
	}
	stats, err := client.Stat(ctx, args.File.Path)
	if err != nil {
		return nil, "", err
	}
	return jsonOutput(string(opsv1alpha1.OperationKindGNOIFileStat), stats),
		fmt.Sprintf("%d file stat entry/entries", len(stats)), nil
}

func gnoiFileGet(ctx context.Context, client *gnoi.Client, args *opsv1alpha1.GNOIArgs) ([]opsv1alpha1.DeviceOperationOutput, string, error) {
	if args == nil || args.File == nil || args.File.Path == "" {
		return nil, "", errors.New("operation.gnoi.file.path is required")
	}
	// Phase B inline-only: cap output at MaxBytes (or default). File
	// artefact spill into ConfigMap reuses the existing enforceTotalInlineBudget
	// fallback in the reconciler, so we just write the bytes into the inline
	// Output and the budget check truncates / spills downstream.
	maxBytes := args.File.MaxBytes
	if maxBytes == 0 {
		maxBytes = defaultInlineMaxBytes
	}
	var buf bytes.Buffer
	hash, err := client.Get(ctx, args.File.Path, &capWriter{w: &buf, max: maxBytes})
	if err != nil {
		return nil, "", err
	}
	payload := struct {
		Path     string `json:"path"`
		Size     int    `json:"size"`
		Hash     string `json:"hash,omitempty"`
		HashAlgo string `json:"hashAlgorithm,omitempty"`
		Content  string `json:"content,omitempty"` // hex when binary, utf-8 when printable
	}{
		Path: args.File.Path,
		Size: buf.Len(),
	}
	if hash != nil {
		payload.Hash = fmt.Sprintf("%x", hash.Hash)
		payload.HashAlgo = hash.Method.String()
	}
	payload.Content = encodeFileContent(buf.Bytes())
	return jsonOutput(string(opsv1alpha1.OperationKindGNOIFileGet), payload),
		fmt.Sprintf("%d byte(s) retrieved from %s", buf.Len(), args.File.Path), nil
}

func gnoiCertGet(ctx context.Context, client *gnoi.Client) ([]opsv1alpha1.DeviceOperationOutput, string, error) {
	certs, err := client.GetCertificates(ctx)
	if err != nil {
		return nil, "", err
	}
	return jsonOutput(string(opsv1alpha1.OperationKindGNOICertGet), certs),
		fmt.Sprintf("%d certificate(s) installed", len(certs)), nil
}

func gnoiCanGenerateCSR(ctx context.Context, client *gnoi.Client, args *opsv1alpha1.GNOIArgs) ([]opsv1alpha1.DeviceOperationOutput, string, error) {
	opts := gnoi.CanGenerateCSROpts{}
	if args != nil && args.Cert != nil {
		opts.KeyType = args.Cert.KeyType
		opts.CertificateType = args.Cert.CertificateType
		opts.KeySize = args.Cert.KeySize
	}
	ok, err := client.CanGenerateCSR(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	payload := struct {
		CanGenerate bool `json:"canGenerate"`
	}{CanGenerate: ok}
	return jsonOutput(string(opsv1alpha1.OperationKindGNOICanGenerateCSR), payload),
		fmt.Sprintf("canGenerate=%v", ok), nil
}

func gnoiRebootStatus(ctx context.Context, client *gnoi.Client) ([]opsv1alpha1.DeviceOperationOutput, string, error) {
	res, err := client.RebootStatus(ctx)
	if err != nil {
		return nil, "", err
	}
	msg := "reboot inactive"
	if res.Active {
		msg = "reboot active"
	}
	return jsonOutput(string(opsv1alpha1.OperationKindGNOIRebootStatus), res), msg, nil
}

func gnoiOSVerify(ctx context.Context, client *gnoi.Client) ([]opsv1alpha1.DeviceOperationOutput, string, error) {
	res, err := client.Verify(ctx)
	if err != nil {
		return nil, "", err
	}
	return jsonOutput(string(opsv1alpha1.OperationKindGNOIOSVerify), res),
		fmt.Sprintf("running version: %s", res.Version), nil
}

// --- helpers ---

// jsonOutput serialises payload as JSON_IETF-ish indented JSON and
// wraps it in a single DeviceOperationOutput. Errors during marshal
// are surfaced as a synthetic output whose Err carries the cause.
func jsonOutput(command string, payload any) []opsv1alpha1.DeviceOperationOutput {
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return []opsv1alpha1.DeviceOperationOutput{{
			Command: command,
			Err:     fmt.Sprintf("encode gnoi result: %v", err),
		}}
	}
	return []opsv1alpha1.DeviceOperationOutput{{
		Command: command,
		Output:  string(b),
	}}
}

// capWriter wraps an io.Writer with a max-byte cap; writes past the
// cap silently truncate (the metadata payload records the actual byte
// count from the device-side stream).
type capWriter struct {
	w   *bytes.Buffer
	max int64
	n   int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.n >= c.max {
		return len(p), nil
	}
	remaining := c.max - c.n
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return len(p), err //nolint:wrapcheck // we silently truncate
}

// encodeFileContent picks a representation safe for inlining: utf-8
// when the bytes are printable text; otherwise hex.
func encodeFileContent(b []byte) string {
	if isPrintableASCII(b) {
		return string(b)
	}
	return fmt.Sprintf("%x", b)
}

func isPrintableASCII(b []byte) bool {
	for _, c := range b {
		if c == '\n' || c == '\r' || c == '\t' {
			continue
		}
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
