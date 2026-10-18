package media

import (
	"context"
	"errors"
	"fmt"
	"io"

	mediadomain "github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/upscale"
)

const maxGeneratedImageBytes = 96 << 20

var ErrUpscaleFailed = errors.New("图片放大失败")

// ProcessGeneratedImage is the single delivery boundary for generated images.
func (s *Service) ProcessGeneratedImage(ctx context.Context, data []byte, targetMaxEdge int) (provider.ProcessedImage, error) {
	processed, err := upscale.Process(ctx, data, targetMaxEdge)
	if err != nil {
		return provider.ProcessedImage{}, fmt.Errorf("%w: %v", ErrUpscaleFailed, err)
	}
	limit := s.runtimeConfig().MaxImageBytes
	if targetMaxEdge > 0 {
		limit = maxGeneratedImageBytes
	}
	if int64(len(processed.Bytes)) > limit {
		return provider.ProcessedImage{}, fmt.Errorf("%w: 成品超过 %d MiB", ErrInvalidImage, limit>>20)
	}
	asset, err := s.saveImageWithLimit(ctx, processed.Bytes, nil, "img_", limit)
	if err != nil {
		return provider.ProcessedImage{}, err
	}
	return provider.ProcessedImage{
		Asset: asset, Bytes: processed.Bytes, Width: processed.Width, Height: processed.Height,
		SourceWidth: processed.SourceWidth, SourceHeight: processed.SourceHeight, Upscaled: processed.Upscaled,
	}, nil
}

func (s *Service) DeleteGeneratedImage(ctx context.Context, id string) error {
	_, err := s.AdminDeleteImages(ctx, []string{id})
	return err
}

func (s *Service) UpscaleImage(ctx context.Context, imageID string, targetSize int) (mediadomain.Asset, error) {
	_, body, err := s.OpenImage(ctx, imageID)
	if err != nil {
		return mediadomain.Asset{}, err
	}
	defer body.Close()
	raw, err := readAll(body, maxGeneratedImageBytes)
	if err != nil {
		return mediadomain.Asset{}, fmt.Errorf("读取原图: %w", err)
	}
	processed, err := s.ProcessGeneratedImage(ctx, raw, targetSize)
	if err != nil {
		return mediadomain.Asset{}, err
	}
	return processed.Asset, nil
}

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
