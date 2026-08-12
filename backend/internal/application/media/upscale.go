package media

import (
	"context"
	"errors"
	"fmt"
	"io"

	mediadomain "github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/infra/upscale"
)

var (
	ErrUpscaleNotSupported = errors.New("放大功能不可用")
	ErrUpscaleFailed       = errors.New("图片放大失败")
)

// UpscaleImage 读取指定图片，放大到目标尺寸，保存为新图片并返回。
// targetSize 是输出图片较长边的像素数（如 2048、4096）。
func (s *Service) UpscaleImage(ctx context.Context, imageID string, targetSize int) (mediadomain.Asset, error) {
	// 读取原图
	_, body, err := s.OpenImage(ctx, imageID)
	if err != nil {
		return mediadomain.Asset{}, err
	}
	defer body.Close()

	raw, err := readAll(body, s.runtimeConfig().MaxImageBytes*2) // 放大后的图片会更大
	if err != nil {
		return mediadomain.Asset{}, fmt.Errorf("读取原图: %w", err)
	}

	// 放大
	upscaler := upscale.New()
	upscaled, err := upscaler.Upscale(ctx, raw, targetSize)
	if err != nil {
		return mediadomain.Asset{}, fmt.Errorf("%w: %v", ErrUpscaleFailed, err)
	}

	// 保存新图
	newAsset, err := s.SaveImage(ctx, upscaled)
	if err != nil {
		return mediadomain.Asset{}, fmt.Errorf("保存放大图片: %w", err)
	}

	return newAsset, nil
}

// readAll 读取至多 maxBytes 字节；超限视为无效图片，避免无界内存增长。
func readAll(r io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrInvalidImage
	}
	return data, nil
}
