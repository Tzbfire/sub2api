// Package service: legacy openai_images.go was removed in favor of the
// openaiimages/ subpackage (chatgpt.com webdriver path). However, several
// helpers from the legacy file are still referenced by upstream code paths
// (account_test_service.go, image_generation_intent.go) that survived the
// rebase. This file restores the minimal symbol set needed for compilation
// without resurrecting the full legacy implementation.
package service

import (
	"strconv"
	"strings"
)

const (
	openAIImagesGenerationsEndpoint = "/v1/images/generations"
	openAIImagesEditsEndpoint       = "/v1/images/edits"
)

// buildOpenAIImagesURL composes an OpenAI-compatible images endpoint URL.
// Originated from the legacy openai_images.go.
func buildOpenAIImagesURL(base string, endpoint string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	relative := strings.TrimPrefix(strings.TrimSpace(endpoint), "/v1")
	if strings.HasSuffix(normalized, endpoint) || strings.HasSuffix(normalized, relative) {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1") {
		return normalized + relative
	}
	return normalized + endpoint
}

const openAIImage2KMaxPixels = 2560 * 1440

// normalizeOpenAIImageSizeTier maps an image size string to a billing tier.
// Originated from the legacy openai_images.go.
func normalizeOpenAIImageSizeTier(size string) string {
	trimmed := strings.TrimSpace(size)
	normalized := strings.ToLower(trimmed)
	switch normalized {
	case "", "auto":
		return "2K"
	case "1024x1024":
		return "1K"
	case "1536x1024", "1024x1536", "1792x1024", "1024x1792", "2048x2048", "2048x1152", "1152x2048":
		return "2K"
	case "3840x2160", "2160x3840":
		return "4K"
	}
	width, height, ok := parseOpenAIImageSizeDimensions(trimmed)
	if !ok {
		return "2K"
	}
	return classifyUnknownOpenAIImageSizeTier(width, height)
}

func parseOpenAIImageSizeDimensions(size string) (int, int, bool) {
	trimmed := strings.TrimSpace(size)
	parts := strings.Split(strings.ToLower(trimmed), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func classifyUnknownOpenAIImageSizeTier(width int, height int) string {
	if height > 0 && width > openAIImage2KMaxPixels/height {
		return "4K"
	}
	return "2K"
}
