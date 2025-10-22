package chess

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"sync"

	nchess "github.com/corentings/chess/v2"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

//go:embed assets/pieces/*.svg
var pieceFiles embed.FS

type pieceCacheKey struct {
	piece nchess.Piece
	size  int
}

var (
	pieceCache   = map[pieceCacheKey]image.Image{}
	pieceCacheMu sync.RWMutex
)

func renderPieceImage(piece nchess.Piece, size int) (image.Image, error) {
	key := pieceCacheKey{piece: piece, size: size}

	pieceCacheMu.RLock()
	if img, ok := pieceCache[key]; ok {
		pieceCacheMu.RUnlock()
		return img, nil
	}
	pieceCacheMu.RUnlock()

	name := pieceAssetName(piece)
	data, err := pieceFiles.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read piece asset %s: %w", name, err)
	}

	icon, err := oksvg.ReadIconStream(bytes.NewReader(sanitizeSVG(data)))
	if err != nil {
		return nil, fmt.Errorf("parse piece svg: %w", err)
	}

	w := int(icon.ViewBox.W)
	h := int(icon.ViewBox.H)
	if w <= 0 {
		w = size
		icon.ViewBox.W = float64(w)
	}
	if h <= 0 {
		h = size
		icon.ViewBox.H = float64(h)
	}

	icon.SetTarget(0, 0, float64(size), float64(size))

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.Transparent), image.Point{}, draw.Src)

	scanner := rasterx.NewScannerGV(size, size, img, img.Bounds())
	raster := rasterx.NewDasher(size, size, scanner)
	icon.Draw(raster, 1.0)

	pieceCacheMu.Lock()
	pieceCache[key] = img
	pieceCacheMu.Unlock()

	return img, nil
}

func pieceAssetName(piece nchess.Piece) string {
	var prefix string
	if piece.Color() == nchess.White {
		prefix = "w"
	} else {
		prefix = "b"
	}

	var suffix string
	switch piece.Type() {
	case nchess.King:
		suffix = "K"
	case nchess.Queen:
		suffix = "Q"
	case nchess.Rook:
		suffix = "R"
	case nchess.Bishop:
		suffix = "B"
	case nchess.Knight:
		suffix = "N"
	case nchess.Pawn:
		suffix = "P"
	}

	return fmt.Sprintf("assets/pieces/%s%s.svg", prefix, suffix)
}
