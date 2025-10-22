package fonts

import (
	_ "embed"
	"fmt"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

//go:embed D2Coding-Ver1.3.2-20180524.ttf
var captionFontData []byte

var (
	captionFontOnce sync.Once
	captionFont     *opentype.Font
	captionFontErr  error

	captionFaceCache sync.Map
)

func CaptionFace() (font.Face, error) {
	return CaptionFaceSized(24)
}

func CaptionFaceSized(size float64) (font.Face, error) {
	if size <= 0 {
		size = 24
	}

	fontData, err := loadCaptionFont()
	if err != nil {
		return nil, err
	}

	cacheKey := fmt.Sprintf("%.2f", size)
	if face, ok := captionFaceCache.Load(cacheKey); ok {
		return face.(font.Face), nil
	}

	face, err := opentype.NewFace(fontData, &opentype.FaceOptions{
		Size:    size,
		DPI:     96,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("create caption font size %.2f: %w", size, err)
	}

	actual, _ := captionFaceCache.LoadOrStore(cacheKey, face)
	return actual.(font.Face), nil
}

func loadCaptionFont() (*opentype.Font, error) {
	captionFontOnce.Do(func() {
		fnt, err := opentype.Parse(captionFontData)
		if err != nil {
			captionFontErr = fmt.Errorf("parse caption font: %w", err)
			return
		}
		captionFont = fnt
	})
	return captionFont, captionFontErr
}
