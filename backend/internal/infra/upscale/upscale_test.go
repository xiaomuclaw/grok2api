package upscale

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestProcessScalesLongEdgeWithoutChangingAspectRatio(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		wantWidth     int
		wantHeight    int
	}{
		{name: "landscape", width: 100, height: 50, wantWidth: 4096, wantHeight: 2048},
		{name: "portrait", width: 50, height: 100, wantWidth: 2048, wantHeight: 4096},
		{name: "square", width: 80, height: 80, wantWidth: 4096, wantHeight: 4096},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Process(context.Background(), encodePNG(t, test.width, test.height, false), 4096)
			if err != nil {
				t.Fatal(err)
			}
			if result.Width != test.wantWidth || result.Height != test.wantHeight {
				t.Fatalf("size = %dx%d, want %dx%d", result.Width, result.Height, test.wantWidth, test.wantHeight)
			}
			if result.SourceWidth != test.width || result.SourceHeight != test.height || !result.Upscaled {
				t.Fatalf("unexpected metadata: %+v", result)
			}
		})
	}
}

func TestProcessPreservesSupportedSourceFormat(t *testing.T) {
	pngResult, err := Process(context.Background(), encodePNG(t, 16, 8, true), 32)
	if err != nil {
		t.Fatal(err)
	}
	if pngResult.MIMEType != "image/png" {
		t.Fatalf("PNG MIME = %q", pngResult.MIMEType)
	}
	pngImage, format, err := image.Decode(bytes.NewReader(pngResult.Bytes))
	if err != nil || format != "png" {
		t.Fatalf("decode PNG: format=%q err=%v", format, err)
	}
	_, _, _, alpha := pngImage.At(0, 0).RGBA()
	if alpha == 0xffff {
		t.Fatal("transparent PNG became opaque")
	}

	jpegResult, err := Process(context.Background(), encodeJPEG(t, 16, 8), 32)
	if err != nil {
		t.Fatal(err)
	}
	if jpegResult.MIMEType != "image/jpeg" {
		t.Fatalf("JPEG MIME = %q", jpegResult.MIMEType)
	}
	if _, format, err := image.Decode(bytes.NewReader(jpegResult.Bytes)); err != nil || format != "jpeg" {
		t.Fatalf("decode JPEG: format=%q err=%v", format, err)
	}
}

func TestProcessDoesNotReencodeImageAlreadyAtTarget(t *testing.T) {
	source := encodePNG(t, 4096, 4, false)
	result, err := Process(context.Background(), source, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if result.Upscaled || !bytes.Equal(result.Bytes, source) {
		t.Fatal("image at target was unexpectedly re-encoded")
	}
}

func TestProcessScalesTo2KLongEdge(t *testing.T) {
	result, err := Process(context.Background(), encodePNG(t, 100, 50, false), 2048)
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 2048 || result.Height != 1024 || !result.Upscaled {
		t.Fatalf("2K result = %+v", result)
	}
}

func encodePNG(t *testing.T, width, height int, transparent bool) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	alpha := uint8(255)
	if transparent {
		alpha = 96
	}
	for y := range height {
		for x := range width {
			value.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 127, A: alpha})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			value.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 127, A: 255})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, value, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
