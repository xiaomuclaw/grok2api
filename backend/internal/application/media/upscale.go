package media

import (
	"context"
	"errors"
	"fmt"

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
	asset, body, err := s.OpenImage(ctx, imageID)
	if err != nil {
		return mediadomain.Asset{}, err
	}
	defer body.Close()

	raw, err := readAll(body, s.maxImageBytes*2) // 允许放大后的图片更大
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

func readAll(r interface{ Read([]byte) (int, error) }, maxBytes int64) ([]byte, error) {
	buf := make([]byte, 0, 32*1024)
	for {
		if len(buf) == cap(buf) {
			newCap := cap(buf) * 2
			if int64(newCap) > maxBytes {
				newCap = int(maxBytes)
			}
			if newCap == cap(buf) {
				return nil, ErrInvalidImage
			}
			newBuf := make([]byte, len(buf), newCap)
			copy(newBuf, buf)
			buf = newBuf
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return nil, err
		}
		if int64(len(buf)) > maxBytes {
			return nil, ErrInvalidImage
		}
	}
}
