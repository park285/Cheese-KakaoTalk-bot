package chess

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	imagedraw "image/draw"
	"image/png"
	"math"
	"strings"

	nchess "github.com/corentings/chess/v2"
	fontassets "github.com/kapu/kakao-cheese-bot-go/internal/assets/fonts"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

type MoveHighlight struct {
	From nchess.Square
	To   nchess.Square
}

type RenderOptions struct {
	Highlight *MoveHighlight
	Player    *PlayerMarker
	Material  MaterialScore
	Captured  CapturedPieces
	HUDHeader string
	HUDTurn   string
}

type PlayerMarker struct {
	Square nchess.Square
}

type BoardRenderer interface {
	RenderPNG(ctx context.Context, board *nchess.Board, opts RenderOptions) ([]byte, error)
}

type svgBoardRenderer struct {
}

func NewSVGBoardRenderer(options ...func(*nchess.Board)) BoardRenderer {
	return &svgBoardRenderer{}
}

func (r *svgBoardRenderer) RenderPNG(ctx context.Context, board *nchess.Board, opts RenderOptions) ([]byte, error) {
	if board == nil {
		return nil, fmt.Errorf("board is nil")
	}

	const (
		squareSize           = 72
		boardSquares         = 8
		boardSize            = squareSize * boardSquares
		sideMargin           = 36
		topMargin            = 110
		bottomMargin         = 36
		titleHeight          = 40
		secondaryPanelHeight = 32
		gapBetweenPanels     = 14
		gapToBoard           = 22
		panelRadius          = 12
		titlePaddingX        = 28
		scorePaddingX        = 24
		turnPaddingX         = 20
		titleMinWidth        = 320
		scoreMinWidth        = 96
		turnMinWidth         = 140
		scoreOffsetY         = 0
		shadowOffsetY        = 6
	)

	totalWidth := boardSize + sideMargin*2
	totalHeight := boardSize + topMargin + bottomMargin
	boardOrigin := image.Point{X: sideMargin, Y: topMargin}
	boardRect := image.Rect(
		boardOrigin.X,
		boardOrigin.Y,
		boardOrigin.X+boardSize,
		boardOrigin.Y+boardSize,
	)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	img := image.NewRGBA(image.Rect(0, 0, totalWidth, totalHeight))

	drawHUD(
		img,
		opts,
		boardRect,
		panelRadius,
		titleHeight,
		secondaryPanelHeight,
		gapBetweenPanels,
		gapToBoard,
		titlePaddingX,
		scorePaddingX,
		turnPaddingX,
		titleMinWidth,
		scoreMinWidth,
		turnMinWidth,
		scoreOffsetY,
		shadowOffsetY,
	)
	drawSquares(img, squareSize, boardOrigin)
	if err := drawPieces(img, board, squareSize, boardOrigin); err != nil {
		return nil, err
	}
	drawHighlight(img, board, opts.Highlight, squareSize, boardOrigin)
	drawPlayerMarker(img, board, opts.Player, squareSize, boardOrigin)

	if err := drawCoordinates(img, squareSize, boardOrigin, sideMargin); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}

	return pngBuf.Bytes(), nil
}

var (
	lightSquare               = color.RGBA{233, 207, 163, 255}
	darkSquare                = color.RGBA{187, 136, 96, 255}
	friendlyHighlightColor    = color.NRGBA{R: 182, G: 184, B: 190, A: 130}
	whiteMoveHighlightFill    = color.NRGBA{R: 255, G: 228, B: 120, A: 140}
	blackMoveHighlightArrow   = color.NRGBA{R: 148, G: 207, B: 255, A: 170}
	neutralMoveHighlightArrow = color.NRGBA{R: 182, G: 184, B: 190, A: 140}
	hudPanelColor             = color.NRGBA{R: 28, G: 31, B: 46, A: 250}
	hudTurnPanelColor         = color.NRGBA{R: 32, G: 35, B: 52, A: 245}
	hudShadowColor            = color.NRGBA{0, 0, 0, 50}
	hudTextPrimary            = color.NRGBA{R: 236, G: 239, B: 255, A: 255}
	hudTurnTextColor          = color.NRGBA{R: 204, G: 210, B: 236, A: 255}
	boardShadowColor          = color.NRGBA{0, 0, 0, 60}
	coordinateTextColor       = color.NRGBA{R: 8, G: 214, B: 120, A: 255}
)

func drawBoardShadow(img *image.RGBA, boardRect image.Rectangle) {
	if img == nil {
		return
	}
	shadowRect := image.Rect(
		boardRect.Min.X+4,
		boardRect.Min.Y+8,
		boardRect.Max.X+10,
		boardRect.Max.Y+12,
	)
	imagedraw.Draw(img, shadowRect, image.NewUniform(boardShadowColor), image.Point{}, imagedraw.Over)
}

func drawSquares(dst imagedraw.Image, squareSize int, origin image.Point) {
	ranks := []nchess.Rank{nchess.Rank8, nchess.Rank7, nchess.Rank6, nchess.Rank5, nchess.Rank4, nchess.Rank3, nchess.Rank2, nchess.Rank1}
	files := []nchess.File{nchess.FileA, nchess.FileB, nchess.FileC, nchess.FileD, nchess.FileE, nchess.FileF, nchess.FileG, nchess.FileH}

	for row, rank := range ranks {
		for col, file := range files {
			x := origin.X + col*squareSize
			y := origin.Y + row*squareSize
			sq := nchess.NewSquare(file, rank)
			clr := squareColor(sq)
			imagedraw.Draw(dst, image.Rect(x, y, x+squareSize, y+squareSize), image.NewUniform(clr), image.Point{}, imagedraw.Src)
		}
	}
}

func drawPieces(dst imagedraw.Image, board *nchess.Board, squareSize int, origin image.Point) error {
	boardMap := board.SquareMap()
	ranks := []nchess.Rank{nchess.Rank8, nchess.Rank7, nchess.Rank6, nchess.Rank5, nchess.Rank4, nchess.Rank3, nchess.Rank2, nchess.Rank1}
	files := []nchess.File{nchess.FileA, nchess.FileB, nchess.FileC, nchess.FileD, nchess.FileE, nchess.FileF, nchess.FileG, nchess.FileH}

	for row, rank := range ranks {
		for col, file := range files {
			sq := nchess.NewSquare(file, rank)
			piece := boardMap[sq]
			if piece == nchess.NoPiece {
				continue
			}
			img, err := renderPieceImage(piece, squareSize)
			if err != nil {
				return err
			}
			x := origin.X + col*squareSize
			y := origin.Y + row*squareSize
			imagedraw.Draw(dst, image.Rect(x, y, x+squareSize, y+squareSize), img, image.Point{}, imagedraw.Over)
		}
	}
	return nil
}

func drawHighlight(img *image.RGBA, board *nchess.Board, highlight *MoveHighlight, squareSize int, origin image.Point) {
	if highlight == nil {
		return
	}
	switch moverColor, ok := moveHighlightMoverColor(board, highlight); {
	case ok && moverColor == nchess.Black:
		drawArrow(img, highlight.From, highlight.To, squareSize, origin, blackMoveHighlightArrow)
	case ok && moverColor == nchess.White:
		drawSquareOverlay(img, highlight.From, squareSize, origin, whiteMoveHighlightFill)
		drawSquareOverlay(img, highlight.To, squareSize, origin, whiteMoveHighlightFill)
	default:
		drawArrow(img, highlight.From, highlight.To, squareSize, origin, neutralMoveHighlightArrow)
	}
}

func moveHighlightMoverColor(board *nchess.Board, highlight *MoveHighlight) (nchess.Color, bool) {
	if board == nil || highlight == nil {
		return nchess.NoColor, false
	}
	if piece := board.Piece(highlight.To); piece != nchess.NoPiece {
		return piece.Color(), true
	}
	if piece := board.Piece(highlight.From); piece != nchess.NoPiece {
		return piece.Color(), true
	}
	return nchess.NoColor, false
}

func playerMarkerColor(board *nchess.Board, square nchess.Square) color.Color {
	if board != nil {
		if piece := board.Piece(square); piece != nchess.NoPiece {
			if piece.Color() == nchess.White {
				return whiteMoveHighlightFill
			}
		}
	}
	return friendlyHighlightColor
}

func drawHUD(
	img *image.RGBA,
	opts RenderOptions,
	boardRect image.Rectangle,
	radius,
	titleHeight,
	secondaryPanelHeight,
	gapBetweenPanels,
	gapToBoard,
	titlePaddingX,
	scorePaddingX,
	turnPaddingX,
	titleMinWidth,
	scoreMinWidth,
	turnMinWidth,
	scoreOffsetY,
	shadowOffsetY int,
) {
	if img == nil {
		return
	}

	face, err := fontassets.CaptionFace()
	if err != nil {
		return
	}

	drawer := &font.Drawer{
		Dst:  img,
		Face: face,
	}

	title := strings.TrimSpace(opts.HUDHeader)
	if title == "" {
		title = "Player vs Bot"
	}

	scoreText := formatMaterialDiff(opts.Material)

	turnText := strings.TrimSpace(opts.HUDTurn)
	if turnText == "" {
		turnText = "Turn"
	}

	turnBottom := boardRect.Min.Y - gapToBoard
	turnTop := turnBottom - secondaryPanelHeight
	titleBottom := turnTop - gapBetweenPanels
	titleTop := titleBottom - titleHeight

	scoreBottom := boardRect.Min.Y - gapToBoard + scoreOffsetY
	scoreTop := scoreBottom - secondaryPanelHeight

	titleWidth := titleMinWidth
	scoreWidth := scoreMinWidth
	turnWidth := turnMinWidth

	titleMeasured := drawer.MeasureString(title).Round() + titlePaddingX*2
	if titleMeasured > titleWidth {
		titleWidth = titleMeasured
	}

	scoreMeasured := drawer.MeasureString(scoreText).Round() + scorePaddingX*2
	if scoreMeasured > scoreWidth {
		scoreWidth = scoreMeasured
	}

	turnMeasured := drawer.MeasureString(turnText).Round() + turnPaddingX*2
	if turnMeasured > turnWidth {
		turnWidth = turnMeasured
	}

	maxTitleWidth := boardRect.Dx() - scoreWidth - 24
	if maxTitleWidth < titleMinWidth {
		maxTitleWidth = titleMinWidth
	}
	if titleWidth > maxTitleWidth {
		titleWidth = maxTitleWidth
	}

	maxTurnWidth := boardRect.Dx() - 40
	if turnWidth > maxTurnWidth {
		turnWidth = maxTurnWidth
	}

	titleRect := image.Rect(
		boardRect.Min.X,
		titleTop,
		boardRect.Min.X+titleWidth,
		titleBottom,
	)

	scoreRect := image.Rect(
		boardRect.Max.X-scoreWidth,
		scoreTop,
		boardRect.Max.X,
		scoreBottom,
	)

	turnLeft := boardRect.Min.X + (boardRect.Dx()-turnWidth)/2
	turnRect := image.Rect(
		turnLeft,
		turnTop,
		turnLeft+turnWidth,
		turnBottom,
	)

	drawRoundedPanel(img, titleRect.Add(image.Pt(0, shadowOffsetY)), radius, hudShadowColor)
	drawRoundedPanel(img, scoreRect.Add(image.Pt(0, shadowOffsetY)), radius, hudShadowColor)
	drawRoundedPanel(img, turnRect.Add(image.Pt(0, shadowOffsetY)), radius, hudShadowColor)

	title = truncateWithEllipsis(face, title, titleRect.Dx()-titlePaddingX*2)
	turnText = truncateWithEllipsis(face, turnText, turnRect.Dx()-turnPaddingX*2)

	drawRoundedPanel(img, titleRect, radius, hudPanelColor)
	drawRoundedPanel(img, scoreRect, radius, hudPanelColor)
	drawRoundedPanel(img, turnRect, radius, hudTurnPanelColor)

	drawCenteredString(drawer, titleRect, title, hudTextPrimary)
	drawCenteredString(drawer, scoreRect, scoreText, hudTextPrimary)
	drawCenteredString(drawer, turnRect, turnText, hudTurnTextColor)
}

func drawPlayerMarker(img *image.RGBA, board *nchess.Board, marker *PlayerMarker, squareSize int, origin image.Point) {
	if img == nil || marker == nil {
		return
	}
	clr := playerMarkerColor(board, marker.Square)
	drawSquareOverlay(img, marker.Square, squareSize, origin, clr)
}

func drawSquareOverlay(img *image.RGBA, sq nchess.Square, squareSize int, origin image.Point, clr color.Color) {
	if img == nil {
		return
	}
	rect := squareRect(sq, squareSize, origin)
	imagedraw.Draw(img, rect, image.NewUniform(clr), image.Point{}, imagedraw.Over)
}

func drawArrow(img *image.RGBA, from, to nchess.Square, squareSize int, origin image.Point, clr color.Color) {
	if img == nil {
		return
	}
	if from == to {
		return
	}
	startRect := squareRect(from, squareSize, origin)
	endRect := squareRect(to, squareSize, origin)
	start := startRect.Max
	start.X = startRect.Min.X + squareSize/2
	start.Y = startRect.Min.Y + squareSize/2
	end := endRect.Max
	end.X = endRect.Min.X + squareSize/2
	end.Y = endRect.Min.Y + squareSize/2

	dx := float64(end.X - start.X)
	dy := float64(end.Y - start.Y)
	length := math.Hypot(dx, dy)
	if length == 0 {
		return
	}

	dirX := dx / length
	dirY := dy / length
	perpX := -dirY
	perpY := dirX

	baseLength := length - float64(squareSize)*0.45
	if baseLength < float64(squareSize)*0.35 {
		baseLength = length * 0.6
	}
	halfWidth := float64(squareSize) * 0.18
	headWidth := float64(squareSize) * 0.32

	baseX := float64(start.X) + dirX*baseLength
	baseY := float64(start.Y) + dirY*baseLength

	shaftStartLeft := pointF{
		X: float64(start.X) - perpX*halfWidth,
		Y: float64(start.Y) - perpY*halfWidth,
	}
	shaftStartRight := pointF{
		X: float64(start.X) + perpX*halfWidth,
		Y: float64(start.Y) + perpY*halfWidth,
	}
	shaftEndLeft := pointF{
		X: baseX - perpX*halfWidth,
		Y: baseY - perpY*halfWidth,
	}
	shaftEndRight := pointF{
		X: baseX + perpX*halfWidth,
		Y: baseY + perpY*halfWidth,
	}

	fillQuad(img, shaftStartLeft, shaftStartRight, shaftEndRight, shaftEndLeft, clr)

	headLeft := pointF{
		X: baseX - perpX*headWidth/2,
		Y: baseY - perpY*headWidth/2,
	}
	headRight := pointF{
		X: baseX + perpX*headWidth/2,
		Y: baseY + perpY*headWidth/2,
	}
	headTip := pointF{
		X: float64(end.X),
		Y: float64(end.Y),
	}

	fillTriangleF(img, headTip, headLeft, headRight, clr)
}

func truncateWithEllipsis(face font.Face, text string, maxWidth int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || maxWidth <= 0 || face == nil {
		return trimmed
	}

	drawer := font.Drawer{Face: face}
	if drawer.MeasureString(trimmed).Round() <= maxWidth {
		return trimmed
	}

	ellipsis := "..."
	ellipsisWidth := drawer.MeasureString(ellipsis).Round()
	if ellipsisWidth > maxWidth {
		return ""
	}

	runes := []rune(trimmed)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		candidate := string(runes) + ellipsis
		if drawer.MeasureString(candidate).Round() <= maxWidth {
			return candidate
		}
	}

	return ellipsis
}

func drawRoundedPanel(img *image.RGBA, rect image.Rectangle, radius int, clr color.Color) {
	if img == nil || rect.Empty() {
		return
	}
	if radius < 0 {
		radius = 0
	}
	maxRadius := rect.Dx() / 2
	if r := rect.Dy() / 2; r < maxRadius {
		maxRadius = r
	}
	if radius > maxRadius {
		radius = maxRadius
	}
	fill := image.NewUniform(clr)
	if radius == 0 {
		imagedraw.Draw(img, rect, fill, image.Point{}, imagedraw.Over)
		return
	}

	core := image.Rect(rect.Min.X+radius, rect.Min.Y, rect.Max.X-radius, rect.Max.Y)
	if core.Dx() > 0 {
		imagedraw.Draw(img, core, fill, image.Point{}, imagedraw.Over)
	}

	topRect := image.Rect(rect.Min.X+radius, rect.Min.Y, rect.Max.X-radius, rect.Min.Y+radius)
	if topRect.Dy() > 0 {
		imagedraw.Draw(img, topRect, fill, image.Point{}, imagedraw.Over)
	}

	bottomRect := image.Rect(rect.Min.X+radius, rect.Max.Y-radius, rect.Max.X-radius, rect.Max.Y)
	if bottomRect.Dy() > 0 {
		imagedraw.Draw(img, bottomRect, fill, image.Point{}, imagedraw.Over)
	}

	leftRect := image.Rect(rect.Min.X, rect.Min.Y+radius, rect.Min.X+radius, rect.Max.Y-radius)
	if leftRect.Dx() > 0 {
		imagedraw.Draw(img, leftRect, fill, image.Point{}, imagedraw.Over)
	}

	rightRect := image.Rect(rect.Max.X-radius, rect.Min.Y+radius, rect.Max.X, rect.Max.Y-radius)
	if rightRect.Dx() > 0 {
		imagedraw.Draw(img, rightRect, fill, image.Point{}, imagedraw.Over)
	}

	corners := []image.Point{
		{rect.Min.X + radius, rect.Min.Y + radius},
		{rect.Max.X - radius - 1, rect.Min.Y + radius},
		{rect.Min.X + radius, rect.Max.Y - radius - 1},
		{rect.Max.X - radius - 1, rect.Max.Y - radius - 1},
	}
	for _, center := range corners {
		drawDisc(img, center, radius, clr)
	}
}

func drawCenteredString(drawer *font.Drawer, rect image.Rectangle, text string, clr color.Color) {
	if drawer == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	metrics := drawer.Face.Metrics()
	width := drawer.MeasureString(text).Round()
	x := rect.Min.X + (rect.Dx()-width)/2
	if x < rect.Min.X {
		x = rect.Min.X
	}
	baseline := rect.Min.Y + (rect.Dy()+metrics.Ascent.Ceil()-metrics.Descent.Ceil())/2
	drawer.Src = image.NewUniform(clr)
	drawer.Dot = fixed.P(x, baseline)
	drawer.DrawString(text)
}

func formatMaterialDiff(material MaterialScore) string {
	diff := material.Diff()
	if diff == 0 {
		return "0"
	}
	return fmt.Sprintf("%+d", diff)
}

func drawCoordinates(dst imagedraw.Image, squareSize int, origin image.Point, margin int) error {
	face, err := fontassets.CaptionFace()
	if err != nil {
		return err
	}

	drawer := &font.Drawer{
		Dst:  dst,
		Face: face,
	}

	ranks := []nchess.Rank{nchess.Rank8, nchess.Rank7, nchess.Rank6, nchess.Rank5, nchess.Rank4, nchess.Rank3, nchess.Rank2, nchess.Rank1}
	files := []nchess.File{nchess.FileA, nchess.FileB, nchess.FileC, nchess.FileD, nchess.FileE, nchess.FileF, nchess.FileG, nchess.FileH}

	ascent := face.Metrics().Ascent.Ceil()

	boardStartX := origin.X
	boardStartY := origin.Y
	boardEndY := origin.Y + len(ranks)*squareSize

	for row, rank := range ranks {
		for col, file := range files {
			sq := nchess.NewSquare(file, rank)
			clr := coordinateColor(sq)
			drawer.Src = image.NewUniform(clr)

			rankCenter := boardStartY + row*squareSize + squareSize/2
			fileCenter := boardStartX + col*squareSize + squareSize/2

			if col == 0 {
				rankBaseline := rankCenter + ascent/2
				rankX := boardStartX - margin/2
				drawCenteredText(drawer, rank.String(), rankX, rankBaseline)
			}

			if row == len(ranks)-1 {
				fileBaseline := boardEndY + ascent
				drawCenteredText(drawer, file.String(), fileCenter, fileBaseline)
			}
		}
	}

	return nil
}

func drawDisc(img *image.RGBA, center image.Point, radius int, clr color.Color) {
	if radius <= 0 {
		blendPixel(img, center.X, center.Y, clr)
		return
	}
	rSquared := radius * radius
	for y := -radius; y <= radius; y++ {
		for x := -radius; x <= radius; x++ {
			if x*x+y*y > rSquared {
				continue
			}
			blendPixel(img, center.X+x, center.Y+y, clr)
		}
	}
}

func blendPixel(img *image.RGBA, x, y int, clr color.Color) {
	if img == nil {
		return
	}
	if !(image.Point{X: x, Y: y}).In(img.Bounds()) {
		return
	}

	sr, sg, sb, sa := clr.RGBA()
	srcA := float64(sa) / 65535.0
	if srcA <= 0 {
		return
	}
	srcR := float64(sr) / 65535.0
	srcG := float64(sg) / 65535.0
	srcB := float64(sb) / 65535.0

	dst := img.RGBAAt(x, y)
	dstA := float64(dst.A) / 255.0

	var dstR, dstG, dstB float64
	if dstA > 0 {
		inv := 1.0 / dstA
		dstR = float64(dst.R) / 255.0 * inv
		dstG = float64(dst.G) / 255.0 * inv
		dstB = float64(dst.B) / 255.0 * inv
	}

	outA := srcA + dstA*(1-srcA)
	if outA <= 0 {
		img.SetRGBA(x, y, color.RGBA{})
		return
	}

	outR := (srcR*srcA + dstR*dstA*(1-srcA)) / outA
	outG := (srcG*srcA + dstG*dstA*(1-srcA)) / outA
	outB := (srcB*srcA + dstB*dstA*(1-srcA)) / outA

	img.SetRGBA(x, y, color.RGBA{
		R: floatToUint8(outR * outA * 255.0),
		G: floatToUint8(outG * outA * 255.0),
		B: floatToUint8(outB * outA * 255.0),
		A: floatToUint8(outA * 255.0),
	})
}

func floatToUint8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func squareRect(sq nchess.Square, squareSize int, origin image.Point) image.Rectangle {
	file := int(sq.File())
	rank := int(sq.Rank())
	row := 7 - rank
	col := file
	x := origin.X + col*squareSize
	y := origin.Y + row*squareSize
	return image.Rect(x, y, x+squareSize, y+squareSize)
}

func pointInTriangleFloat(x, y float64, a, b, c pointF) bool {
	denom := (b.Y-c.Y)*(a.X-c.X) + (c.X-b.X)*(a.Y-c.Y)
	if denom == 0 {
		return false
	}
	alpha := ((b.Y-c.Y)*(x-c.X) + (c.X-b.X)*(y-c.Y)) / denom
	beta := ((c.Y-a.Y)*(x-c.X) + (a.X-c.X)*(y-c.Y)) / denom
	gamma := 1 - alpha - beta
	return alpha >= 0 && beta >= 0 && gamma >= 0
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func drawCenteredText(drawer *font.Drawer, text string, centerX, baseline int) {
	if text == "" {
		return
	}
	width := drawer.MeasureString(text).Round()
	drawer.Dot = fixed.P(centerX-width/2, baseline)
	drawer.DrawString(text)
}

func squareColor(sq nchess.Square) color.Color {
	if (int(sq.File())+int(sq.Rank()))%2 == 0 {
		return darkSquare
	}
	return lightSquare
}

func coordinateColor(nchess.Square) color.Color {
	return coordinateTextColor
}
func fillQuad(img *image.RGBA, p0, p1, p2, p3 pointF, clr color.Color) {
	fillTriangleF(img, p0, p1, p2, clr)
	fillTriangleF(img, p0, p2, p3, clr)
}

func fillTriangleF(img *image.RGBA, a, b, c pointF, clr color.Color) {
	minX := int(math.Floor(minFloat(a.X, minFloat(b.X, c.X))))
	maxX := int(math.Ceil(maxFloat(a.X, maxFloat(b.X, c.X))))
	minY := int(math.Floor(minFloat(a.Y, minFloat(b.Y, c.Y))))
	maxY := int(math.Ceil(maxFloat(a.Y, maxFloat(b.Y, c.Y))))

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if pointInTriangleFloat(float64(x)+0.5, float64(y)+0.5, a, b, c) {
				blendPixel(img, x, y, clr)
			}
		}
	}
}

type pointF struct {
	X float64
	Y float64
}
