package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
)

func DeliverGeneratedImage(ctx context.Context, store ImageAssetStore, data []byte, targetMaxEdge int) (ProcessedImage, error) {
	if store == nil {
		return ProcessedImage{}, fmt.Errorf("图片媒体存储未配置")
	}
	if targetMaxEdge > 0 {
		generatedStore, ok := store.(GeneratedImageStore)
		if !ok {
			return ProcessedImage{}, fmt.Errorf("图片媒体存储不支持 2K/4K 后处理")
		}
		return generatedStore.ProcessGeneratedImage(ctx, data, targetMaxEdge)
	}
	asset, err := store.SaveImage(ctx, data)
	if err != nil {
		return ProcessedImage{}, err
	}
	config, _, _ := image.DecodeConfig(bytes.NewReader(data))
	return ProcessedImage{Asset: asset, Bytes: data, Width: config.Width, Height: config.Height, SourceWidth: config.Width, SourceHeight: config.Height}, nil
}

func CleanupGeneratedImages(ctx context.Context, store ImageAssetStore, ids []string) error {
	generatedStore, ok := store.(GeneratedImageStore)
	if !ok {
		return nil
	}
	var cleanupErrors []error
	for _, id := range ids {
		if err := generatedStore.DeleteGeneratedImage(ctx, id); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete generated image %s: %w", id, err))
		}
	}
	return errors.Join(cleanupErrors...)
}
