package upscale

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"

	// 注册 JPEG/GIF 解码器；image.Decode 依赖 init 副作用，缺少即报未知格式。
	_ "image/gif"
	_ "image/jpeg"

	"golang.org/x/image/draw"
	// WebP 解码器：上游图片可能是 WebP。
	_ "golang.org/x/image/webp"
)

// Upscaler 图片放大器
type Upscaler struct{}

// New 创建一个新的放大器实例
func New() *Upscaler {
	return &Upscaler{}
}

// Upscale 将图片放大到目标尺寸（较长边）
// 使用 CatmullRom 插值算法，质量接近 Lanczos
func (u *Upscaler) Upscale(ctx context.Context, imgData []byte, targetSize int) ([]byte, error) {
	// 解码原图
	img, format, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil, fmt.Errorf("解码图片失败: %w", err)
	}

	switch format {
	case "png", "jpeg", "webp", "gif":
	default:
		return nil, fmt.Errorf("不支持的图片格式: %s", format)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("图片尺寸无效: %dx%d", width, height)
	}
	if targetSize <= 0 || targetSize > 8192 {
		return nil, fmt.Errorf("目标尺寸超出范围: %d", targetSize)
	}

	// 计算缩放比例（保持宽高比）
	var newWidth, newHeight int
	if width >= height {
		// 横图或正方形：宽度为目标尺寸
		scale := float64(targetSize) / float64(width)
		newWidth = targetSize
		newHeight = int(float64(height) * scale)
	} else {
		// 竖图：高度为目标尺寸
		scale := float64(targetSize) / float64(height)
		newHeight = targetSize
		newWidth = int(float64(width) * scale)
	}

	// 如果已经是目标尺寸或更大，直接返回原图
	if width >= newWidth && height >= newHeight {
		return imgData, nil
	}

	// 创建目标图片
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// 使用 CatmullRom 缩放（高质量，接近 Lanczos）
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	// 编码为 PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("编码图片失败: %w", err)
	}

	return buf.Bytes(), nil
}
