//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"image/png"
	"testing"
)

func liveFloorImageJPEG(t *testing.T, revision byte) []byte {
	t.Helper()
	picture, err := png.Decode(bytes.NewReader(liveFloorImagePNG(t, revision)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, picture, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// A deterministic one-page vector floor plan, including a complete cross-reference
// table. No filesystem, URL, font, or external asset is involved.
func liveFloorImagePDF() []byte {
	stream := "q\n0.96 0.96 0.96 rg\n0 0 640 480 re f\n0.12 0.25 0.62 RG\n2 w\n"
	for x := 0; x <= 640; x += 80 {
		stream += fmt.Sprintf("%d 0 m %d 480 l S\n", x, x)
	}
	for y := 0; y <= 480; y += 80 {
		stream += fmt.Sprintf("0 %d m 640 %d l S\n", y, y)
	}
	stream += "Q\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 640 480] /Resources << >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream),
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return out.Bytes()
}
