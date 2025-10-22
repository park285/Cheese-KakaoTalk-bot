package chess

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/park285/Cheese-KakaoTalk-bot/internal/chess/openingbook"
	"github.com/park285/Cheese-KakaoTalk-bot/internal/chess/uci"
)

const (
	defaultOpeningMaxPly    = 12
	defaultOpeningMinWeight = 1
	maxCatalogWeight        = 0xffff
)

type OpeningOptions struct {
	MaxPly    int
	MinWeight int
}

type Engine struct {
	pool    *uci.Pool
	randMu  sync.Mutex
	rand    *rand.Rand
	opening OpeningOptions
}

func NewEngine(binaryPath string) (*Engine, error) {
	pool, err := uci.NewPool(uci.PoolConfig{BinaryPath: binaryPath})
	if err != nil {
		return nil, err
	}
	return &Engine{
		pool:    pool,
		rand:    rand.New(rand.NewSource(time.Now().UnixNano())),
		opening: defaultOpeningOptions(),
	}, nil
}

func defaultOpeningOptions() OpeningOptions {
	return OpeningOptions{
		MaxPly:    defaultOpeningMaxPly,
		MinWeight: defaultOpeningMinWeight,
	}
}

func (e *Engine) SetOpeningOptions(opts OpeningOptions) {
	if opts.MaxPly <= 0 {
		opts.MaxPly = defaultOpeningMaxPly
	}
	if opts.MinWeight <= 0 {
		opts.MinWeight = defaultOpeningMinWeight
	}
	e.opening = opts
}

type EvaluateRequest struct {
	PresetName string
	FEN        string
	Moves      []string
}

type EvaluateResult struct {
	Preset         DifficultyPreset
	Duration       time.Duration
	Candidates     []Candidate
	Chosen         Candidate
	Blunder        bool
	EngineBestMove string
}

func (e *Engine) Evaluate(ctx context.Context, req EvaluateRequest) (EvaluateResult, error) {
	start := time.Now()

	preset, err := GetPreset(req.PresetName)
	if err != nil {
		return EvaluateResult{}, err
	}

	adjustedPreset := preset
	if len(preset.CandidateWeights) > 0 {
		adjustedPreset.CandidateWeights = append([]float64(nil), preset.CandidateWeights...)
	}

	randSrc := e.random()
	openingCandidates, openingChosen, openingApplied, openingErr := e.tryOpeningMove(req, &adjustedPreset, randSrc)
	if openingErr == nil && openingApplied {
		return EvaluateResult{
			Preset:         adjustedPreset,
			Duration:       time.Since(start),
			Candidates:     openingCandidates,
			Chosen:         openingChosen,
			Blunder:        false,
			EngineBestMove: openingChosen.Move,
		}, nil
	}

	opt := optionsFromPreset(preset)
	session, err := e.pool.Acquire(ctx, opt)
	if err != nil {
		return EvaluateResult{}, err
	}
	var releaseErr error
	defer func() {
		e.pool.Release(session, releaseErr)
	}()

	if err := session.NewGame(ctx); err != nil {
		releaseErr = err
		return EvaluateResult{}, err
	}

	goTokens, err := BuildGoCommand(preset)
	if err != nil {
		releaseErr = err
		return EvaluateResult{}, err
	}
	limits := limitsFromPreset(preset)

	searchStart := time.Now()
	resp, err := session.Search(ctx, uci.SearchRequest{
		FEN:         req.FEN,
		Moves:       req.Moves,
		Limits:      limits,
		GoOverrides: goTokens,
	})
	if err != nil {
		releaseErr = err
		return EvaluateResult{}, err
	}
	dur := time.Since(searchStart)

	candidates := convertCandidates(resp.Candidates)
	if len(candidates) == 0 {
		releaseErr = fmt.Errorf("engine returned no candidates")
		return EvaluateResult{}, releaseErr
	}

	candidates = applyOpeningPreferences(&adjustedPreset, candidates, req.Moves, randSrc)

	chosen, blunder, err := SelectCandidate(adjustedPreset, candidates, randSrc)
	if err != nil {
		releaseErr = err
		return EvaluateResult{}, err
	}
	releaseErr = nil

	return EvaluateResult{
		Preset:         adjustedPreset,
		Duration:       dur,
		Candidates:     candidates,
		Chosen:         chosen,
		Blunder:        blunder,
		EngineBestMove: resp.BestMove,
	}, nil
}

func (e *Engine) random() *rand.Rand {
	e.randMu.Lock()
	seed := e.rand.Int63()
	e.randMu.Unlock()
	return rand.New(rand.NewSource(seed))
}

func (e *Engine) SetRandomSeed(seed int64) {
	e.randMu.Lock()
	e.rand = rand.New(rand.NewSource(seed))
	e.randMu.Unlock()
}

func (e *Engine) Close() error {
	if e.pool == nil {
		return nil
	}
	return e.pool.Close()
}

func (e *Engine) tryOpeningMove(req EvaluateRequest, preset *DifficultyPreset, r *rand.Rand) ([]Candidate, Candidate, bool, error) {
	if preset == nil || r == nil {
		return nil, Candidate{}, false, nil
	}

	limit := preset.OpeningCatalog.MaxPly
	if limit <= 0 {
		limit = e.opening.MaxPly
	}
	if limit <= 0 {
		limit = defaultOpeningMaxPly
	}
	if len(req.Moves) >= limit {
		return nil, Candidate{}, false, nil
	}

	minWeight := preset.OpeningCatalog.MinWeight
	if minWeight <= 0 {
		minWeight = e.opening.MinWeight
	}
	if minWeight <= 0 {
		minWeight = defaultOpeningMinWeight
	}

	tokens := catalogTokens(preset.OpeningCatalog)
	if len(tokens) > 0 {
		results, err := aggregateCatalogMoves(req.Moves, tokens, preset.OpeningCatalog.Color, limit, minWeight)
		if err == nil && len(results) > 0 {
			candidates := catalogResultsToCandidates(results)
			selected := selectCatalogMove(results, r)
			if selected.Move != "" {
				candidates = applyPolyglotMove(candidates, selected.Move)
			}
			chosen, _, err := SelectCandidate(*preset, candidates, r)
			if err != nil {
				return nil, Candidate{}, false, err
			}
			return candidates, chosen, true, nil
		}
		if err != nil {
			return nil, Candidate{}, false, err
		}
	}

	bookResult, err := openingbook.Lookup(req.FEN, req.Moves)
	if err != nil {
		return nil, Candidate{}, false, err
	}
	if bookResult.Move == "" {
		return nil, Candidate{}, false, nil
	}
	if minWeight > 0 && int(bookResult.Weight) < minWeight {
		return nil, Candidate{}, false, nil
	}

	candidates := []Candidate{{
		Move:      bookResult.Move,
		EvalCP:    0,
		Principal: []string{bookResult.Move},
		Forced:    true,
	}}
	chosen, _, err := SelectCandidate(*preset, candidates, r)
	if err != nil {
		return nil, Candidate{}, false, err
	}
	return candidates, chosen, true, nil
}

func catalogTokens(cfg OpeningCatalogConfig) []string {
	if len(cfg.Keys) > 0 {
		return append([]string(nil), cfg.Keys...)
	}
	key := strings.TrimSpace(cfg.Key)
	if key == "" {
		return nil
	}
	return []string{key}
}

func aggregateCatalogMoves(history []string, tokens []string, color string, maxPly, minWeight int) ([]openingbook.Result, error) {
	if len(tokens) == 0 {
		return nil, nil
	}

	combined := make(map[string]int, len(tokens))
	for _, token := range tokens {
		results, _, err := openingbook.LookupCatalog(history, token, color, maxPly)
		if err != nil {
			return nil, err
		}
		for _, res := range results {
			move := strings.TrimSpace(res.Move)
			if move == "" {
				continue
			}
			combined[move] += int(res.Weight)
		}
	}

	out := make([]openingbook.Result, 0, len(combined))
	for move, weight := range combined {
		if minWeight > 0 && weight < minWeight {
			continue
		}
		if weight > maxCatalogWeight {
			weight = maxCatalogWeight
		}
		out = append(out, openingbook.Result{
			Move:   move,
			Weight: uint16(weight),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Weight == out[j].Weight {
			return out[i].Move < out[j].Move
		}
		return out[i].Weight > out[j].Weight
	})

	return out, nil
}

func catalogResultsToCandidates(results []openingbook.Result) []Candidate {
	if len(results) == 0 {
		return nil
	}
	candidates := make([]Candidate, 0, len(results))
	for _, res := range results {
		move := strings.TrimSpace(res.Move)
		if move == "" {
			continue
		}
		candidates = append(candidates, Candidate{
			Move:      move,
			EvalCP:    0,
			Principal: []string{move},
			Forced:    false,
		})
	}
	return candidates
}

func optionsFromPreset(p DifficultyPreset) uci.Options {
	return uci.Options{
		Threads:    p.Threads,
		SkillLevel: p.SkillLevel,
		HashMB:     p.HashMB,
		MultiPV:    p.MultiPV,
		Elo:        presetElo(p.Name),
	}
}

func limitsFromPreset(p DifficultyPreset) uci.Limits {
	return uci.Limits{
		Depth:          p.DepthCap,
		MoveTimeMillis: p.MoveTimeMillis,
		NodeCap:        p.NodeCap,
	}
}

func convertCandidates(in []uci.Candidate) []Candidate {
	out := make([]Candidate, 0, len(in))
	for _, c := range in {
		out = append(out, Candidate{
			Move:      c.Move,
			EvalCP:    c.EvalCP,
			Principal: append([]string(nil), c.Principal...),
			Forced:    false,
		})
	}
	return out
}

func presetElo(name string) int {
	switch name {
	case "level1":
		return 600
	case "level2":
		return 700
	case "level3":
		return 800
	case "level4":
		return 1000
	case "level5":
		return 1200
	case "level6":
		return 1400
	case "level7":
		return 1650
	case "level8":
		return 1900
	default:
		return 1500
	}
}

func applyOpeningPreferences(p *DifficultyPreset, candidates []Candidate, moves []string, r *rand.Rand) []Candidate {
	if p == nil || len(p.OpeningPreferences) == 0 || len(candidates) == 0 {
		return candidates
	}
	if r == nil {
		return candidates
	}

	if len(moves)%2 == 0 {
		return candidates
	}
	whiteHistory, blackHistory := splitMovesByColor(moves)

	type prefMatch struct {
		pref OpeningPreference
		idx  int
	}
	matches := make([]prefMatch, 0)
	forcedMatches := make([]prefMatch, 0)
	totalProb := 0.0
	totalForcedProb := 0.0

	for _, pref := range p.OpeningPreferences {
		if len(pref.WhiteMoves) == 0 {
			continue
		}
		compareLen := len(whiteHistory)
		if compareLen == 0 {
			continue
		}
		if compareLen > len(pref.WhiteMoves) {
			compareLen = len(pref.WhiteMoves)
		}
		if !openingPrefixMatched(whiteHistory[:compareLen], pref.WhiteMoves[:compareLen]) {
			continue
		}
		if len(blackHistory) > len(pref.BlackMoves) {
			continue
		}
		if !openingPrefixMatched(blackHistory, pref.BlackMoves[:len(blackHistory)]) {
			continue
		}
		if len(blackHistory) == len(pref.BlackMoves) {
			continue
		}
		nextMove := strings.ToLower(strings.TrimSpace(pref.BlackMoves[len(blackHistory)]))
		idx := findCandidateIndex(candidates, nextMove)
		probability := pref.Probability
		if pref.Force && probability <= 0 {
			probability = 1.0
		}
		if probability <= 0 {
			continue
		}
		if idx == -1 {
			cloned := pref.clone()
			cloned.Probability = probability
			candidates = append(candidates, Candidate{Move: nextMove})
			idx = len(candidates) - 1
			cloned.BlackMoves = append([]string(nil), pref.BlackMoves...)
			candidates[idx].Forced = pref.Force
			match := prefMatch{pref: cloned, idx: idx}
			if pref.Force {
				forcedMatches = append(forcedMatches, match)
				totalForcedProb += probability
			} else {
				matches = append(matches, match)
				totalProb += probability
			}
		} else {
			if pref.Force {
				candidates[idx].Forced = true
			}
			cloned := pref.clone()
			cloned.Probability = probability
			match := prefMatch{pref: cloned, idx: idx}
			if pref.Force {
				forcedMatches = append(forcedMatches, match)
				totalForcedProb += probability
			} else {
				matches = append(matches, match)
				totalProb += probability
			}
		}
	}

	selectMatch := func(matchSet []prefMatch, total float64) prefMatch {
		if len(matchSet) == 0 {
			return prefMatch{}
		}
		if total <= 0 {
			return matchSet[0]
		}
		roll := r.Float64() * total
		cumulative := 0.0
		selected := matchSet[len(matchSet)-1]
		for _, m := range matchSet {
			cumulative += m.pref.Probability
			if roll < cumulative {
				selected = m
				break
			}
		}
		return selected
	}

	var selected prefMatch
	if len(forcedMatches) > 0 {
		selected = selectMatch(forcedMatches, totalForcedProb)
	} else if len(matches) > 0 {
		selected = selectMatch(matches, totalProb)
	} else {
		return candidates
	}

	if selected.pref.Force {
		candidates[selected.idx].Forced = true
	}
	if selected.idx != 0 {
		candidates[0], candidates[selected.idx] = candidates[selected.idx], candidates[0]
		if selected.pref.Force {
			candidates[selected.idx].Forced = false
		}
	}
	if selected.pref.Force {
		candidates[0].Forced = true
	}
	if len(p.CandidateWeights) > 0 {
		if p.CandidateWeights[0] == 0 {
			p.CandidateWeights[0] = selected.pref.WeightMultiplier
		} else {
			p.CandidateWeights[0] *= selected.pref.WeightMultiplier
		}
	}
	return candidates
}

func splitMovesByColor(allMoves []string) (whiteMoves, blackMoves []string) {
	for i, mv := range allMoves {
		if i%2 == 0 {
			whiteMoves = append(whiteMoves, mv)
		} else {
			blackMoves = append(blackMoves, mv)
		}
	}
	return whiteMoves, blackMoves
}

func openingPrefixMatched(history []string, target []string) bool {
	if len(history) > len(target) {
		return false
	}
	for i := 0; i < len(history); i++ {
		if !strings.EqualFold(strings.TrimSpace(history[i]), strings.TrimSpace(target[i])) {
			return false
		}
	}
	return true
}

func findCandidateIndex(candidates []Candidate, move string) int {
	if move == "" {
		return -1
	}
	for i, cand := range candidates {
		if strings.EqualFold(strings.TrimSpace(cand.Move), move) {
			return i
		}
	}
	return -1
}

func selectCatalogMove(candidates []openingbook.Result, r *rand.Rand) openingbook.Result {
	if len(candidates) == 0 {
		return openingbook.Result{}
	}
	if r == nil {
		return candidates[0]
	}
	total := 0
	for _, cand := range candidates {
		total += int(cand.Weight)
	}
	if total <= 0 {
		return candidates[0]
	}
	roll := r.Intn(total)
	cumulative := 0
	for _, cand := range candidates {
		cumulative += int(cand.Weight)
		if roll < cumulative {
			return cand
		}
	}
	return candidates[len(candidates)-1]
}

func applyPolyglotMove(candidates []Candidate, move string) []Candidate {
	if move == "" {
		return candidates
	}
	idx := findCandidateIndex(candidates, move)
	if idx == -1 {
		eval := 0
		if len(candidates) > 0 {
			eval = candidates[0].EvalCP
		}
		newCandidate := Candidate{
			Move:      move,
			EvalCP:    eval,
			Principal: []string{move},
			Forced:    true,
		}
		return append([]Candidate{newCandidate}, candidates...)
	}

	candidates[idx].Forced = true
	if idx != 0 {
		candidates[0], candidates[idx] = candidates[idx], candidates[0]
	}
	candidates[0].Forced = true
	return candidates
}
