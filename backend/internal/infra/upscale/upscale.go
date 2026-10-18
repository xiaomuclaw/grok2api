package upscale

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	_ "image/gif"
	_ "image/jpeg"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const MaxTargetEdge = 8192

var slots = make(chan struct{}, 2)

type Result struct {
	Bytes        []byte
	MIMEType     string
	Width        int
	Height       int
	SourceWidth  int
	SourceHeight int
	Upscaled     bool
}

func Process(ctx context.Context, data []byte, targetMaxEdge int) (Result, error) {
	if len(data) == 0 {
		return Result{}, fmt.Errorf("图片内容为空")
	}
	if targetMaxEdge < 0 || targetMaxEdge > MaxTargetEdge {
		return Result{}, fmt.Errorf("目标长边超出范围: %d", targetMaxEdge)
	}

	source, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("解码图片: %w", err)
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return Result{}, fmt.Errorf("图片尺寸无效: %dx%d", width, height)
	}
	mimeType := sourceMIMEType(format)
	if targetMaxEdge == 0 || max(width, height) >= targetMaxEdge {
		return Result{Bytes: data, MIMEType: mimeType, Width: width, Height: height, SourceWidth: width, SourceHeight: height}, nil
	}

	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}

	newWidth, newHeight := targetDimensions(width, height, targetMaxEdge)
	destination := image.NewNRGBA(image.Rect(0, 0, newWidth, newHeight))
	draw.CatmullRom.Scale(destination, destination.Bounds(), source, bounds, draw.Src, nil)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	encoded, outputMIME, err := encode(destination, format)
	if err != nil {
		return Result{}, err
	}
	actual, _, err := image.DecodeConfig(bytes.NewReader(encoded))
	if err != nil {
		return Result{}, fmt.Errorf("校验放大图片: %w", err)
	}
	if max(actual.Width, actual.Height) != targetMaxEdge {
		return Result{}, fmt.Errorf("放大图片尺寸不符合预期: %dx%d", actual.Width, actual.Height)
	}
	return Result{
		Bytes: encoded, MIMEType: outputMIME, Width: actual.Width, Height: actual.Height,
		SourceWidth: width, SourceHeight: height, Upscaled: true,
	}, nil
}

func targetDimensions(width, height, target int) (int, int) {
	if width >= height {
		return target, max(1, int(math.Round(float64(height)*float64(target)/float64(width))))
	}
	return max(1, int(math.Round(float64(width)*float64(target)/float64(height)))), target
}

func sourceMIMEType(format string) string {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	default:
		return "image/png"
	}
}

func encode(image *image.NRGBA, sourceFormat string) ([]byte, string, error) {
	var output bytes.Buffer
	switch strings.ToLower(sourceFormat) {
	case "jpeg", "jpg":
		if err := jpeg.Encode(&output, image, &jpeg.Options{Quality: 95}); err != nil {
			return nil, "", fmt.Errorf("编码 JPEG: %w", err)
		}
		return output.Bytes(), "image/jpeg", nil
	case "png":
		encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := encoder.Encode(&output, image); err != nil {
			return nil, "", fmt.Errorf("编码 PNG: %w", err)
		}
		return output.Bytes(), "image/png", nil
	default:
		if image.Opaque() {
			if err := jpeg.Encode(&output, image, &jpeg.Options{Quality: 95}); err != nil {
				return nil, "", fmt.Errorf("编码 JPEG: %w", err)
			}
			return output.Bytes(), "image/jpeg", nil
		}
		encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := encoder.Encode(&output, image); err != nil {
			return nil, "", fmt.Errorf("编码 PNG: %w", err)
		}
		return output.Bytes(), "image/png", nil
	}
}
