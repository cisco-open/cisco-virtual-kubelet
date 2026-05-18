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

package gnoi

import (
	"context"
	"fmt"
	"io"
	"time"

	syspb "github.com/openconfig/gnoi/system"
)

// PingResult mirrors the structured per-probe output from gNOI
// System.Ping. The device streams one response per ICMP reply plus a
// final summary; Replies is the per-probe sequence and Summary is the
// device's aggregate (sent/received/loss%/rtt-min/avg/max).
type PingResult struct {
	Replies []PingReply
	Summary PingSummary
}

// PingReply is a single ICMP echo reply observation.
type PingReply struct {
	Source   string
	RTT      time.Duration
	TTL      int
	Sequence int
	Bytes    int
}

// PingSummary mirrors the device's aggregate summary.
type PingSummary struct {
	Sent     int
	Received int
	LossPct  float64
	MinRTT   time.Duration
	AvgRTT   time.Duration
	MaxRTT   time.Duration
}

// PingOpts carries optional Ping parameters. Empty / zero values
// defer to the device default.
type PingOpts struct {
	Source   string
	Count    int32
	Interval time.Duration
	Wait     time.Duration
	Size     int32
}

// Ping runs gNOI System.Ping against destination and collects the
// streamed responses into a structured PingResult.
func (c *Client) Ping(ctx context.Context, destination string, opts PingOpts) (*PingResult, error) {
	if err := c.cap.ensureSupported(ServiceSystem); err != nil {
		return nil, err
	}
	req := &syspb.PingRequest{
		Destination: destination,
		Source:      opts.Source,
		Count:       opts.Count,
		Interval:    int64(opts.Interval),
		Wait:        int64(opts.Wait),
		Size:        opts.Size,
	}
	stream, err := c.system.Ping(c.authCtx(ctx), req)
	if err != nil {
		c.cap.Observe(ServiceSystem, err)
		return nil, fmt.Errorf("gnoi System.Ping: %w", err)
	}
	res := &PingResult{}
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.cap.Observe(ServiceSystem, err)
			return res, fmt.Errorf("gnoi System.Ping recv: %w", err)
		}
		if resp.Sent != 0 || resp.Received != 0 {
			res.Summary = PingSummary{
				Sent:     int(resp.Sent),
				Received: int(resp.Received),
				MinRTT:   time.Duration(resp.MinTime),
				AvgRTT:   time.Duration(resp.AvgTime),
				MaxRTT:   time.Duration(resp.MaxTime),
			}
			if resp.Sent > 0 {
				res.Summary.LossPct = 100.0 * float64(resp.Sent-resp.Received) / float64(resp.Sent)
			}
			continue
		}
		res.Replies = append(res.Replies, PingReply{
			Source:   resp.Source,
			RTT:      time.Duration(resp.Time),
			TTL:      int(resp.Ttl),
			Sequence: int(resp.Sequence),
			Bytes:    int(resp.Bytes),
		})
	}
	c.cap.Observe(ServiceSystem, nil)
	return res, nil
}

// TracerouteResult collects the streamed hop responses from gNOI
// System.Traceroute.
type TracerouteResult struct {
	Destination string
	Hops        []TracerouteHop
}

// TracerouteHop captures one hop on the path.
type TracerouteHop struct {
	Hop     int
	Address string
	Name    string
	RTT     time.Duration
	State   string
}

// TracerouteOpts mirrors PingOpts for traceroute.
type TracerouteOpts struct {
	Source   string
	MaxHops  int32
	Wait     time.Duration
	Protocol string // ICMP / UDP / TCP
}

// Traceroute runs gNOI System.Traceroute against destination.
func (c *Client) Traceroute(ctx context.Context, destination string, opts TracerouteOpts) (*TracerouteResult, error) {
	if err := c.cap.ensureSupported(ServiceSystem); err != nil {
		return nil, err
	}
	req := &syspb.TracerouteRequest{
		Destination: destination,
		Source:      opts.Source,
		Wait:        int64(opts.Wait),
	}
	if opts.MaxHops > 0 {
		req.MaxTtl = opts.MaxHops
	}
	switch opts.Protocol {
	case "UDP", "udp":
		req.L4Protocol = syspb.TracerouteRequest_UDP
	case "TCP", "tcp":
		req.L4Protocol = syspb.TracerouteRequest_TCP
	case "ICMP", "icmp", "":
		req.L4Protocol = syspb.TracerouteRequest_ICMP
	}
	stream, err := c.system.Traceroute(c.authCtx(ctx), req)
	if err != nil {
		c.cap.Observe(ServiceSystem, err)
		return nil, fmt.Errorf("gnoi System.Traceroute: %w", err)
	}
	res := &TracerouteResult{Destination: destination}
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.cap.Observe(ServiceSystem, err)
			return res, fmt.Errorf("gnoi System.Traceroute recv: %w", err)
		}
		res.Hops = append(res.Hops, TracerouteHop{
			Hop:     int(resp.Hop),
			Address: resp.Address,
			Name:    resp.Name,
			RTT:     time.Duration(resp.Rtt),
			State:   resp.State.String(),
		})
	}
	c.cap.Observe(ServiceSystem, nil)
	return res, nil
}

// Time returns the device clock as a time.Time. Useful for capability
// probing — among the cheapest gNOI RPCs.
func (c *Client) Time(ctx context.Context) (time.Time, error) {
	if err := c.cap.ensureSupported(ServiceSystem); err != nil {
		return time.Time{}, err
	}
	resp, err := c.system.Time(c.authCtx(ctx), &syspb.TimeRequest{})
	c.cap.Observe(ServiceSystem, err)
	if err != nil {
		return time.Time{}, fmt.Errorf("gnoi System.Time: %w", err)
	}
	return time.Unix(0, int64(resp.Time)), nil
}

// RebootStatusResult mirrors gNOI System.RebootStatus.
type RebootStatusResult struct {
	Active          bool
	Wait            time.Duration
	When            time.Time
	Reason          string
	Count           uint32
	Method          string
	Status          string
	StatusMessage   string
	LastRebootError string
}

// RebootOpts mirrors the RebootRequest inputs.
type RebootOpts struct {
	// Method names the reboot variant; defaults to COLD on empty.
	// IOS-XE supports COLD; NSF/POWERDOWN/HALT are platform-specific.
	Method string

	// Delay before the reboot fires, in nanoseconds since the request.
	// Zero means immediate (subject to device queuing).
	Delay time.Duration

	// Message is an operator-visible reason recorded by the device.
	Message string

	// Force overrides device-side sanity checks (uncommitted config,
	// in-flight ISSU, etc.). Destructive — operators set this only
	// when they have explicit confirmation.
	Force bool
}

// Reboot triggers gNOI System.Reboot. The device returns once the
// reboot has been scheduled; the operator must subsequently observe
// reachability or call RebootStatus to know when it has completed.
func (c *Client) Reboot(ctx context.Context, opts RebootOpts) error {
	if err := c.cap.ensureSupported(ServiceSystem); err != nil {
		return err
	}
	req := &syspb.RebootRequest{
		Method:  rebootMethodFromString(opts.Method),
		Delay:   uint64(opts.Delay),
		Message: opts.Message,
		Force:   opts.Force,
	}
	_, err := c.system.Reboot(c.authCtx(ctx), req)
	c.cap.Observe(ServiceSystem, err)
	if err != nil {
		return fmt.Errorf("gnoi System.Reboot: %w", err)
	}
	return nil
}

// CancelReboot cancels any pending reboot scheduled via Reboot with a
// non-zero Delay. Returns nil if there was nothing to cancel.
func (c *Client) CancelReboot(ctx context.Context, message string) error {
	if err := c.cap.ensureSupported(ServiceSystem); err != nil {
		return err
	}
	_, err := c.system.CancelReboot(c.authCtx(ctx), &syspb.CancelRebootRequest{Message: message})
	c.cap.Observe(ServiceSystem, err)
	if err != nil {
		return fmt.Errorf("gnoi System.CancelReboot: %w", err)
	}
	return nil
}

// KillProcessOpts mirrors the KillProcessRequest inputs.
type KillProcessOpts struct {
	// PID, when non-zero, names the target process by id.
	PID uint32
	// Name, when non-empty, names the target process by name. The
	// device chooses Pid when both are set.
	Name string
	// Signal: TERM, KILL, HUP, ABRT. Empty defaults to TERM.
	Signal string
	// Restart the process after the kill?
	Restart bool
}

// KillProcess sends a signal to a device process.
func (c *Client) KillProcess(ctx context.Context, opts KillProcessOpts) error {
	if err := c.cap.ensureSupported(ServiceSystem); err != nil {
		return err
	}
	req := &syspb.KillProcessRequest{
		Pid:     opts.PID,
		Name:    opts.Name,
		Signal:  killSignalFromString(opts.Signal),
		Restart: opts.Restart,
	}
	_, err := c.system.KillProcess(c.authCtx(ctx), req)
	c.cap.Observe(ServiceSystem, err)
	if err != nil {
		return fmt.Errorf("gnoi System.KillProcess: %w", err)
	}
	return nil
}

func rebootMethodFromString(s string) syspb.RebootMethod {
	switch s {
	case "COLD", "":
		return syspb.RebootMethod_COLD
	case "NSF":
		return syspb.RebootMethod_NSF
	case "POWERDOWN":
		return syspb.RebootMethod_POWERDOWN
	case "HALT":
		return syspb.RebootMethod_HALT
	case "WARM":
		return syspb.RebootMethod_WARM
	}
	return syspb.RebootMethod_UNKNOWN
}

func killSignalFromString(s string) syspb.KillProcessRequest_Signal {
	switch s {
	case "TERM", "":
		return syspb.KillProcessRequest_SIGNAL_TERM
	case "KILL":
		return syspb.KillProcessRequest_SIGNAL_KILL
	case "HUP":
		return syspb.KillProcessRequest_SIGNAL_HUP
	case "ABRT":
		return syspb.KillProcessRequest_SIGNAL_ABRT
	}
	return syspb.KillProcessRequest_SIGNAL_UNSPECIFIED
}

// RebootStatus polls the device for the status of any in-flight or
// completed reboot. Subcomponents is currently unused — IOS-XE
// platform-wide reboots only.
func (c *Client) RebootStatus(ctx context.Context) (*RebootStatusResult, error) {
	if err := c.cap.ensureSupported(ServiceSystem); err != nil {
		return nil, err
	}
	resp, err := c.system.RebootStatus(c.authCtx(ctx), &syspb.RebootStatusRequest{})
	c.cap.Observe(ServiceSystem, err)
	if err != nil {
		return nil, fmt.Errorf("gnoi System.RebootStatus: %w", err)
	}
	res := &RebootStatusResult{
		Active: resp.Active,
		Wait:   time.Duration(resp.Wait),
		Reason: resp.Reason,
		Count:  resp.Count,
		Method: resp.Method.String(),
		Status: resp.Status.String(),
	}
	if resp.When != 0 {
		res.When = time.Unix(0, int64(resp.When))
	}
	return res, nil
}
