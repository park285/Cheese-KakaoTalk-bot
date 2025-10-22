package openingbook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	chesslib "github.com/corentings/chess/v2"
	"github.com/corentings/chess/v2/opening"
)

var (
	bookOnce sync.Once
	book     *chesslib.PolyglotBook
	bookErr  error

	catalogOnce sync.Once
	catalog     *catalogStore
	catalogErr  error
)

const maxCatalogWeight = 0xffff

type Result struct {
	Move   string
	Weight uint16
}

type LineMove struct {
	Ply    int    `json:"ply"`
	Color  string `json:"color"`
	Move   string `json:"move"`
	SAN    string `json:"san"`
	Weight int    `json:"weight"`
}

type CatalogVariation struct {
	Moves       []LineMove `json:"moves"`
	FinalFEN    string     `json:"final_fen"`
	TotalWeight int        `json:"total_weight"`
}

type CatalogEntry struct {
	Key         string             `json:"key"`
	ECOCode     string             `json:"eco_code,omitempty"`
	ECOTitle    string             `json:"eco_title,omitempty"`
	TotalWeight int                `json:"total_weight"`
	Variations  []CatalogVariation `json:"variations"`
}

type CatalogOptions struct {
	MaxPly    int
	MinWeight uint16
}

func newCatalogStore(entries []CatalogEntry) *catalogStore {
	store := &catalogStore{
		entries: append([]CatalogEntry(nil), entries...),
		byKey:   make(map[string]*CatalogEntry, len(entries)),
		byTitle: make(map[string]*CatalogEntry, len(entries)),
	}
	for i := range store.entries {
		entry := &store.entries[i]
		if token := normalizeCatalogToken(entry.Key); token != "" {
			store.byKey[token] = entry
		}
		if token := normalizeCatalogToken(entry.ECOCode); token != "" {
			store.byKey[token] = entry
		}
		if token := normalizeCatalogToken(entry.ECOTitle); token != "" {
			store.byTitle[token] = entry
		}
	}
	return store
}

func (c *catalogStore) findEntry(token string) *CatalogEntry {
	if c == nil {
		return nil
	}
	norm := normalizeCatalogToken(token)
	if norm == "" {
		return nil
	}
	if entry, ok := c.byKey[norm]; ok {
		return entry
	}
	if entry, ok := c.byTitle[norm]; ok {
		return entry
	}
	return nil
}

type catalogFile struct {
	Entries []CatalogEntry `json:"entries"`
}

type catalogStore struct {
	entries []CatalogEntry
	byKey   map[string]*CatalogEntry
	byTitle map[string]*CatalogEntry
}

func Lookup(fen string, moves []string) (Result, error) {
	book, err := loadBook()
	if err != nil {
		return Result{}, err
	}
	if book == nil {
		return Result{}, nil
	}

	game, err := buildGameFromPosition(fen, moves)
	if err != nil {
		return Result{}, err
	}
	if game.Position().Turn() != chesslib.Black {
		return Result{}, nil
	}

	positionFEN := game.FEN()

	hasher := chesslib.NewZobristHasher()
	hashStr, err := hasher.HashPosition(positionFEN)
	if err != nil {
		return Result{}, fmt.Errorf("compute polyglot hash: %w", err)
	}

	hash := chesslib.ZobristHashToUint64(hashStr)
	entries := book.FindMoves(hash)
	if len(entries) == 0 {
		return Result{}, nil
	}

	entry := entries[0]
	move := chesslib.DecodeMove(entry.Move).ToMove()
	uciMove := move.String()

	verifyGame, err := buildGameFromPosition(fen, moves)
	if err != nil {
		return Result{}, err
	}
	if err := verifyGame.PushNotationMove(uciMove, chesslib.UCINotation{}, nil); err != nil {
		return Result{}, fmt.Errorf("book move %q invalid for position: %w", uciMove, err)
	}

	return Result{
		Move:   uciMove,
		Weight: entry.Weight,
	}, nil
}

func loadBook() (*chesslib.PolyglotBook, error) {
	bookOnce.Do(func() {
		bookPath, resolveErr := ResolveBookPath()
		if resolveErr != nil {
			bookErr = resolveErr
			return
		}
		if bookPath == "" {
			book = nil
			return
		}
		file, err := os.Open(bookPath)
		if err != nil {
			bookErr = fmt.Errorf("open polyglot book %q: %w", bookPath, err)
			return
		}
		defer file.Close()

		book, err = chesslib.LoadFromReader(file)
		if err != nil {
			bookErr = fmt.Errorf("load polyglot book %q: %w", bookPath, err)
			return
		}
	})

	return book, bookErr
}

func loadCatalog() (*catalogStore, error) {
	catalogOnce.Do(func() {
		catalogPath, resolveErr := ResolveCatalogPath()
		if resolveErr != nil {
			catalogErr = resolveErr
			return
		}
		if strings.TrimSpace(catalogPath) == "" {
			catalog = nil
			return
		}
		file, err := os.Open(catalogPath)
		if err != nil {
			catalogErr = fmt.Errorf("open opening catalog %q: %w", catalogPath, err)
			return
		}
		defer file.Close()

		var payload catalogFile
		if err := json.NewDecoder(file).Decode(&payload); err != nil {
			catalogErr = fmt.Errorf("decode opening catalog %q: %w", catalogPath, err)
			return
		}
		catalog = newCatalogStore(payload.Entries)
	})

	return catalog, catalogErr
}

func ResolveBookPath() (string, error) {
	if envPath := os.Getenv("CHESS_POLYGLOT_BOOK_PATH"); envPath != "" {
		if exists(envPath) {
			return envPath, nil
		}
		return "", fmt.Errorf("env CHESS_POLYGLOT_BOOK_PATH points to missing file: %s", envPath)
	}

	for _, candidate := range defaultBookPaths() {
		if exists(candidate) {
			return candidate, nil
		}
	}

	return "", nil
}

func ResolveCatalogPath() (string, error) {
	if envPath := os.Getenv("CHESS_OPENING_CATALOG_PATH"); envPath != "" {
		if exists(envPath) {
			return envPath, nil
		}
		return "", fmt.Errorf("env CHESS_OPENING_CATALOG_PATH points to missing file: %s", envPath)
	}

	for _, candidate := range defaultCatalogPaths() {
		if exists(candidate) {
			return candidate, nil
		}
	}

	return "", nil
}

func defaultBookPaths() []string {
	return []string{
		filepath.Join("resources", "opening", "Cerebellum3Merge.bin"),
		filepath.Join("cheese lib", "Cerebellum3Merge.bin"),
		filepath.Join("cheese lib", "lib", "Cerebellum3Merge.bin"),
	}
}

func defaultCatalogPaths() []string {
	return []string{
		filepath.Join("resources", "opening", "catalog.json"),
	}
}

func exists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func buildGameFromPosition(fen string, moves []string) (*chesslib.Game, error) {
	var (
		game *chesslib.Game
		err  error
	)

	if strings.TrimSpace(fen) == "" || fen == "startpos" {
		game = chesslib.NewGame()
	} else {
		var option func(*chesslib.Game)
		option, err = chesslib.FEN(fen)
		if err != nil {
			return nil, fmt.Errorf("parse fen %q: %w", fen, err)
		}
		game = chesslib.NewGame(option)
	}

	for _, mv := range moves {
		if err := game.PushNotationMove(mv, chesslib.UCINotation{}, nil); err != nil {
			return nil, fmt.Errorf("apply move %q: %w", mv, err)
		}
	}
	return game, nil
}

func LoadFromPath(bookPath string) (*chesslib.PolyglotBook, error) {
	if strings.TrimSpace(bookPath) == "" {
		return nil, fmt.Errorf("polyglot book path required")
	}
	file, err := os.Open(bookPath)
	if err != nil {
		return nil, fmt.Errorf("open polyglot book %q: %w", bookPath, err)
	}
	defer file.Close()

	book, err := chesslib.LoadFromReader(file)
	if err != nil {
		return nil, fmt.Errorf("load polyglot book %q: %w", bookPath, err)
	}
	return book, nil
}

func BuildCatalog(book *chesslib.PolyglotBook, opts CatalogOptions) ([]CatalogEntry, error) {
	if book == nil {
		return nil, fmt.Errorf("polyglot book is nil")
	}
	maxPly := opts.MaxPly
	if maxPly <= 0 {
		maxPly = 12
	}
	minWeight := opts.MinWeight
	if minWeight == 0 {
		minWeight = 1
	}

	hasher := chesslib.NewZobristHasher()
	ecoBook := opening.NewBookECO()
	uciNotation := chesslib.UCINotation{}
	algebraic := chesslib.AlgebraicNotation{}

	groups := make(map[string]*CatalogEntry)

	var walk func(game *chesslib.Game, path []LineMove) error
	walk = func(game *chesslib.Game, path []LineMove) error {
		if len(path) >= maxPly {
			return appendCatalogEntry(groups, game, path, ecoBook)
		}

		hashStr, err := hasher.HashPosition(game.FEN())
		if err != nil {
			return fmt.Errorf("compute polyglot hash: %w", err)
		}
		entries := book.FindMoves(chesslib.ZobristHashToUint64(hashStr))
		filtered := make([]chesslib.PolyglotEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.Weight >= minWeight {
				filtered = append(filtered, entry)
			}
		}
		if len(filtered) == 0 {
			return appendCatalogEntry(groups, game, path, ecoBook)
		}

		for _, entry := range filtered {
			move := chesslib.DecodeMove(entry.Move).ToMove()
			moveStr := move.String()
			moveSAN := algebraic.Encode(game.Position(), &move)

			lineMove := LineMove{
				Ply:    len(path) + 1,
				Color:  colorToString(game.Position().Turn()),
				Move:   moveStr,
				SAN:    moveSAN,
				Weight: int(entry.Weight),
			}

			child := game.Clone()
			if err := child.PushNotationMove(moveStr, uciNotation, nil); err != nil {
				return fmt.Errorf("apply move %q: %w", moveStr, err)
			}

			nextPath := append(append([]LineMove(nil), path...), lineMove)
			if err := walk(child, nextPath); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(chesslib.NewGame(), nil); err != nil {
		return nil, err
	}

	entries := make([]CatalogEntry, 0, len(groups))
	for _, group := range groups {
		sort.Slice(group.Variations, func(i, j int) bool {
			iKey := joinMoves(group.Variations[i].Moves)
			jKey := joinMoves(group.Variations[j].Moves)
			if iKey == jKey {
				return group.Variations[i].FinalFEN < group.Variations[j].FinalFEN
			}
			return iKey < jKey
		})
		entries = append(entries, *group)
	}

	sort.Slice(entries, func(i, j int) bool {
		iKey := sortKey(entries[i])
		jKey := sortKey(entries[j])
		if iKey == jKey {
			return entries[i].TotalWeight > entries[j].TotalWeight
		}
		return iKey < jKey
	})

	return entries, nil
}

func LookupCatalog(history []string, entryToken, color string, maxPly int) ([]Result, *CatalogEntry, error) {
	if strings.TrimSpace(entryToken) == "" {
		return nil, nil, nil
	}
	store, err := loadCatalog()
	if err != nil {
		return nil, nil, err
	}
	if store == nil {
		return nil, nil, nil
	}
	entry := store.findEntry(entryToken)
	if entry == nil {
		return nil, nil, nil
	}
	ply := len(history)
	limit := maxPly
	if limit <= 0 {
		limit = 12
	}
	if ply >= limit {
		return nil, entry, nil
	}
	sideToMove := sideToMoveFromHistory(history)
	colorConstraint := strings.ToLower(strings.TrimSpace(color))
	if colorConstraint == "" {
		colorConstraint = "black"
	}
	if colorConstraint != "both" && colorConstraint != sideToMove {
		return nil, entry, nil
	}

	matches := make([]CatalogVariation, 0)
	for _, variation := range entry.Variations {
		if len(history) >= len(variation.Moves) {
			continue
		}
		if !variationPrefixMatches(variation, history) {
			continue
		}
		nextMove := variation.Moves[ply]
		if normalizeColorToken(nextMove.Color) != sideToMove {
			continue
		}
		matches = append(matches, variation)
	}

	if len(matches) == 0 {
		return nil, entry, nil
	}

	moveWeights := make(map[string]int)
	for _, variation := range matches {
		next := variation.Moves[ply]
		move := strings.ToLower(strings.TrimSpace(next.Move))
		if move == "" {
			continue
		}
		weight := next.Weight
		if weight <= 0 {
			continue
		}
		moveWeights[move] += weight
	}

	if len(moveWeights) == 0 {
		return nil, entry, nil
	}

	results := make([]Result, 0, len(moveWeights))
	for move, weight := range moveWeights {
		if weight > maxCatalogWeight {
			weight = maxCatalogWeight
		}
		results = append(results, Result{Move: move, Weight: uint16(weight)})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Weight == results[j].Weight {
			return results[i].Move < results[j].Move
		}
		return results[i].Weight > results[j].Weight
	})

	return results, entry, nil
}

func appendCatalogEntry(groups map[string]*CatalogEntry, game *chesslib.Game, path []LineMove, ecoBook *opening.BookECO) error {
	if len(path) == 0 {
		return nil
	}

	movesCopy := append([]LineMove(nil), path...)
	totalWeight := 0
	for _, mv := range movesCopy {
		totalWeight += mv.Weight
	}

	var ecoCode string
	var ecoTitle string
	if eco := ecoBook.Find(game.Moves()); eco != nil {
		ecoCode = eco.Code()
		ecoTitle = eco.Title()
	}

	key := ecoCode
	if key == "" {
		key = joinMoves(movesCopy)
	}

	group, ok := groups[key]
	if !ok {
		group = &CatalogEntry{
			Key:      key,
			ECOCode:  ecoCode,
			ECOTitle: ecoTitle,
		}
		groups[key] = group
	} else {
		if group.ECOCode == "" && ecoCode != "" {
			group.ECOCode = ecoCode
		}
		if group.ECOTitle == "" && ecoTitle != "" {
			group.ECOTitle = ecoTitle
		}
	}

	group.Variations = append(group.Variations, CatalogVariation{
		Moves:       movesCopy,
		FinalFEN:    game.FEN(),
		TotalWeight: totalWeight,
	})
	group.TotalWeight += totalWeight

	return nil
}

func colorToString(color chesslib.Color) string {
	switch color {
	case chesslib.White:
		return "white"
	case chesslib.Black:
		return "black"
	default:
		return "unknown"
	}
}

func joinMoves(moves []LineMove) string {
	parts := make([]string, 0, len(moves))
	for _, mv := range moves {
		parts = append(parts, mv.Move)
	}
	return strings.Join(parts, " ")
}

func sortKey(entry CatalogEntry) string {
	if entry.ECOCode != "" {
		return entry.ECOCode
	}
	return entry.Key
}

func normalizeCatalogToken(s string) string {
	trimmed := strings.ToLower(strings.TrimSpace(s))
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeColorToken(color string) string {
	switch strings.ToLower(strings.TrimSpace(color)) {
	case "white", "w":
		return "white"
	case "black", "b":
		return "black"
	default:
		return ""
	}
}

func sideToMoveFromHistory(history []string) string {
	if len(history)%2 == 0 {
		return "white"
	}
	return "black"
}

func variationPrefixMatches(variation CatalogVariation, history []string) bool {
	if len(history) > len(variation.Moves) {
		return false
	}
	for i, mv := range history {
		if !strings.EqualFold(strings.TrimSpace(variation.Moves[i].Move), strings.TrimSpace(mv)) {
			return false
		}
	}
	return true
}
