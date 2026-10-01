// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package softwareupgrade

import (
	"context"
	"fmt"
	"io"
	"time"
)

type transferRateContextKey struct{}

func withTransferRate(ctx context.Context, bytesPerSecond int64) context.Context {
	return context.WithValue(ctx, transferRateContextKey{}, bytesPerSecond)
}

func transferRateFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	rate, _ := ctx.Value(transferRateContextKey{}).(int64)
	return rate
}

// bytePacer implements a zero-burst average byte ceiling. It deliberately
// waits before releasing each chunk, so neither a fresh transfer nor a retry
// receives a full token-bucket burst that could violate a constrained link.
type bytePacer struct {
	ctx            context.Context
	bytesPerSecond int64
	startedAt      time.Time
	accountedBytes int64
}

func newBytePacer(ctx context.Context, bytesPerSecond int64) (*bytePacer, error) {
	if ctx == nil {
		return nil, fmt.Errorf("transfer pacer context is required")
	}
	if bytesPerSecond <= 0 {
		return nil, fmt.Errorf("transfer rate must be positive")
	}
	return &bytePacer{ctx: ctx, bytesPerSecond: bytesPerSecond, startedAt: time.Now()}, nil
}

func (p *bytePacer) wait(n int) error {
	if n <= 0 {
		return p.ctx.Err()
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	if int64(n) > maxInt64-p.accountedBytes {
		p.accountedBytes = maxInt64
	} else {
		p.accountedBytes += int64(n)
	}
	due := p.startedAt.Add(pacingDuration(p.accountedBytes, p.bytesPerSecond))
	delay := time.Until(due)
	if delay <= 0 {
		return p.ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return p.ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

func pacingDuration(bytes, bytesPerSecond int64) time.Duration {
	const maxInt64 = int64(^uint64(0) >> 1)
	seconds := bytes / bytesPerSecond
	if seconds >= maxInt64/int64(time.Second) {
		return time.Duration(maxInt64)
	}
	remainder := bytes % bytesPerSecond
	nanos := int64(float64(remainder) / float64(bytesPerSecond) * float64(time.Second))
	return time.Duration(seconds*int64(time.Second) + nanos)
}

type pacedReader struct {
	reader io.Reader
	pacer  *bytePacer
}

// NewPacedReader wraps reader with a context-cancellable, zero-burst byte-rate
// ceiling. It is used for the worker-to-device gNOI transfer segment.
func NewPacedReader(ctx context.Context, reader io.Reader, bytesPerSecond int64) (io.Reader, error) {
	if reader == nil {
		return nil, fmt.Errorf("transfer reader is required")
	}
	pacer, err := newBytePacer(ctx, bytesPerSecond)
	if err != nil {
		return nil, err
	}
	return &pacedReader{reader: reader, pacer: pacer}, nil
}

func (r *pacedReader) Read(buffer []byte) (int, error) {
	n, readErr := r.reader.Read(buffer)
	if n > 0 {
		if err := r.pacer.wait(n); err != nil {
			return 0, err
		}
	}
	return n, readErr
}

type pacedWriter struct {
	writer io.Writer
	pacer  *bytePacer
}

func newPacedWriter(ctx context.Context, writer io.Writer, bytesPerSecond int64) (io.Writer, error) {
	if writer == nil {
		return nil, fmt.Errorf("transfer writer is required")
	}
	pacer, err := newBytePacer(ctx, bytesPerSecond)
	if err != nil {
		return nil, err
	}
	return &pacedWriter{writer: writer, pacer: pacer}, nil
}

func (w *pacedWriter) Write(buffer []byte) (int, error) {
	if err := w.pacer.wait(len(buffer)); err != nil {
		return 0, err
	}
	return w.writer.Write(buffer)
}
