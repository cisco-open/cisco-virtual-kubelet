// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package softwareupgrade

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPacedReaderEnforcesZeroBurstRate(t *testing.T) {
	const (
		payloadSize = 40
		byteRate    = 1000
	)
	reader, err := NewPacedReader(context.Background(), bytes.NewReader(make([]byte, payloadSize)), byteRate)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != payloadSize {
		t.Fatalf("read %d bytes, want %d", len(content), payloadSize)
	}
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond {
		t.Fatalf("paced read completed in %s, want at least 35ms", elapsed)
	}
}

func TestPacedReaderCancellationInterruptsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, err := NewPacedReader(ctx, bytes.NewReader(make([]byte, 1024)), 1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, readErr := reader.Read(make([]byte, 1024))
		done <- readErr
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Read() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("paced read did not stop after cancellation")
	}
}

func TestPacedWriterEnforcesRate(t *testing.T) {
	var destination bytes.Buffer
	writer, err := newPacedWriter(context.Background(), &destination, 1000)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := writer.Write(make([]byte, 40)); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond {
		t.Fatalf("paced write completed in %s, want at least 35ms", elapsed)
	}
	if destination.Len() != 40 {
		t.Fatalf("destination length = %d, want 40", destination.Len())
	}
}

func TestPacedMaterializationRejectsOversizeBeforeWaiting(t *testing.T) {
	cacheDir := t.TempDir()
	ctx := withTransferRate(context.Background(), 1)
	started := time.Now()
	_, err := materializeRemoteImage(ctx, "paced test", strings.Repeat("0", 64), cacheDir, 10, func(writer io.Writer) (int64, error) {
		n, writeErr := writer.Write(make([]byte, 20))
		return int64(n), writeErr
	})
	if !errors.Is(err, errImageTooLarge) {
		t.Fatalf("materializeRemoteImage() error = %v, want errImageTooLarge", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("oversized paced response took %s; size rejection occurred after pacing", elapsed)
	}
}

func TestPacingDurationSaturatesInsteadOfWrapping(t *testing.T) {
	const maxInt64 = int64(^uint64(0) >> 1)
	if got := pacingDuration(maxInt64, 1); got != time.Duration(maxInt64) {
		t.Fatalf("pacingDuration(max, 1) = %s, want saturation", got)
	}
	if got := pacingDuration(1500, 1000); got != 1500*time.Millisecond {
		t.Fatalf("pacingDuration(1500, 1000) = %s, want 1.5s", got)
	}
}
