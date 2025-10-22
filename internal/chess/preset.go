package chess

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/park285/Cheese-KakaoTalk-bot/internal/chess/openingbook"
)

type DifficultyPreset struct {
	Name               string
	SkillLevel         int
	Threads            int
	HashMB             int
	MoveTimeMillis     int
	NodeCap            int
	DepthCap           int
	MultiPV            int
	PrimaryChoices     int
	CandidateWeights   []float64
	EvalNoise          int
	OpeningPreferences []OpeningPreference
	OpeningCatalog     OpeningCatalogConfig
}

type OpeningPreference struct {
	Name             string
	Color            string
	WhiteMoves       []string
	BlackMoves       []string
	Probability      float64
	WeightMultiplier float64
	Force            bool
}

type OpeningCatalogConfig struct {
	Key       string
	Keys      []string
	Color     string
	MaxPly    int
	MinWeight int
	StyleKey  string
}

var presetMu sync.RWMutex

var defaultOpeningPreferences = []OpeningPreference{
	{
		Name:             "sicilian-mainline",
		Color:            "black",
		WhiteMoves:       []string{"e2e4", "g1f3", "d2d4", "f3d4"},
		BlackMoves:       []string{"c7c5", "d7d6", "c5d4", "g8f6"},
		Probability:      0.45,
		WeightMultiplier: 2.0,
		Force:            true,
	},
	{
		Name:             "sicilian-closed-variation",
		Color:            "black",
		WhiteMoves:       []string{"e2e4", "b1c3", "g1f3"},
		BlackMoves:       []string{"c7c5", "g8f6", "d7d6", "e7e6"},
		Probability:      0.05,
		WeightMultiplier: 2.0,
	},
	{
		Name:             "berlin-mainline",
		Color:            "black",
		WhiteMoves:       []string{"e2e4", "g1f3", "f1b5", "e1g1"},
		BlackMoves:       []string{"e7e5", "b8c6", "g8f6", "f8e7"},
		Probability:      0.45,
		WeightMultiplier: 2.0,
		Force:            true,
	},
	{
		Name:             "berlin-anti-variation",
		Color:            "black",
		WhiteMoves:       []string{"e2e4", "g1f3", "f1b5", "d2d3"},
		BlackMoves:       []string{"e7e5", "b8c6", "g8f6", "a7a6"},
		Probability:      0.05,
		WeightMultiplier: 2.0,
	},
	{
		Name:             "grunfeld-mainline",
		Color:            "black",
		WhiteMoves:       []string{"d2d4", "c2c4", "b1c3", "g1f3"},
		BlackMoves:       []string{"g8f6", "g7g6", "d7d5", "f8g7"},
		Probability:      0.4,
		WeightMultiplier: 2.0,
		Force:            true,
	},
	{
		Name:             "grunfeld-fianchetto",
		Color:            "black",
		WhiteMoves:       []string{"d2d4", "c2c4", "g2g3"},
		BlackMoves:       []string{"g8f6", "g7g6", "d7d5", "c7c6"},
		Probability:      0.1,
		WeightMultiplier: 2.0,
	},
	{
		Name:             "kings-indian-classical",
		Color:            "black",
		WhiteMoves:       []string{"d2d4", "c2c4", "g1f3", "e2e4"},
		BlackMoves:       []string{"g8f6", "g7g6", "f8g7", "d7d6"},
		Probability:      0.4,
		WeightMultiplier: 2.0,
		Force:            true,
	},
	{
		Name:             "kings-indian-fianchetto",
		Color:            "black",
		WhiteMoves:       []string{"d2d4", "c2c4", "g2g3", "g1f3"},
		BlackMoves:       []string{"g8f6", "g7g6", "f8g7", "d7d6"},
		Probability:      0.1,
		WeightMultiplier: 2.0,
	},
	{
		Name:             "english-caro-structure",
		Color:            "black",
		WhiteMoves:       []string{"c2c4", "g1f3", "d2d4"},
		BlackMoves:       []string{"c7c6", "d7d5", "g8f6", "e7e6"},
		Probability:      0.7,
		WeightMultiplier: 2.0,
		Force:            true,
	},
	{
		Name:             "english-caro-advance",
		Color:            "black",
		WhiteMoves:       []string{"c2c4", "d2d4", "c4d5"},
		BlackMoves:       []string{"c7c6", "d7d5", "c6d5", "g8f6"},
		Probability:      0.3,
		WeightMultiplier: 2.0,
	},
	{
		Name:             "from-gambit-main",
		Color:            "black",
		WhiteMoves:       []string{"f2f4", "f4e5", "d2d4", "e2e4"},
		BlackMoves:       []string{"e7e5", "d7d6", "d8h4", "g8f6"},
		Probability:      0.7,
		WeightMultiplier: 2.0,
		Force:            true,
	},
	{
		Name:             "from-gambit-decline",
		Color:            "black",
		WhiteMoves:       []string{"f2f4", "g2g3", "b1c3"},
		BlackMoves:       []string{"e7e5", "d8h4", "g8f6", "f8c5"},
		Probability:      0.3,
		WeightMultiplier: 2.0,
	},
}

func cloneOpeningPreferences(prefs []OpeningPreference) []OpeningPreference {
	if len(prefs) == 0 {
		return nil
	}
	out := make([]OpeningPreference, len(prefs))
	copy(out, prefs)
	return out
}

func (op OpeningPreference) clone() OpeningPreference {
	dup := op
	dup.WhiteMoves = append([]string(nil), op.WhiteMoves...)
	dup.BlackMoves = append([]string(nil), op.BlackMoves...)
	return dup
}

func cloneOpeningCatalogConfig(cfg OpeningCatalogConfig) OpeningCatalogConfig {
	dup := cfg
	if len(cfg.Keys) > 0 {
		dup.Keys = append([]string(nil), cfg.Keys...)
	}
	return dup
}

const defaultThreads = 2
const forlv8 = 6

var DefaultPresets = map[string]DifficultyPreset{
	"level1": {
		Name:               "level1",
		SkillLevel:         0,
		Threads:            defaultThreads,
		HashMB:             16,
		MoveTimeMillis:     20,
		NodeCap:            0,
		DepthCap:           5,
		MultiPV:            5,
		PrimaryChoices:     3,
		CandidateWeights:   []float64{0.5, 0.3, 0.2},
		EvalNoise:          80,
		OpeningPreferences: cloneOpeningPreferences(defaultOpeningPreferences),
	},
	"level2": {
		Name:               "level2",
		SkillLevel:         0,
		Threads:            defaultThreads,
		HashMB:             16,
		MoveTimeMillis:     60,
		NodeCap:            0,
		DepthCap:           6,
		MultiPV:            5,
		PrimaryChoices:     3,
		CandidateWeights:   []float64{0.6, 0.3, 0.1},
		EvalNoise:          60,
		OpeningPreferences: cloneOpeningPreferences(defaultOpeningPreferences),
	},
	"level3": {
		Name:               "level3",
		SkillLevel:         1,
		Threads:            defaultThreads,
		HashMB:             24,
		MoveTimeMillis:     80,
		NodeCap:            0,
		DepthCap:           8,
		MultiPV:            5,
		PrimaryChoices:     3,
		CandidateWeights:   []float64{0.7, 0.2, 0.1},
		EvalNoise:          45,
		OpeningPreferences: cloneOpeningPreferences(defaultOpeningPreferences),
	},
	"level4": {
		Name:               "level4",
		SkillLevel:         3,
		Threads:            defaultThreads,
		HashMB:             32,
		MoveTimeMillis:     140,
		NodeCap:            0,
		DepthCap:           10,
		MultiPV:            5,
		PrimaryChoices:     3,
		CandidateWeights:   []float64{0.65, 0.25, 0.1},
		EvalNoise:          30,
		OpeningPreferences: cloneOpeningPreferences(defaultOpeningPreferences),
	},
	"level5": {
		Name:               "level5",
		SkillLevel:         7,
		Threads:            defaultThreads,
		HashMB:             48,
		MoveTimeMillis:     200,
		NodeCap:            0,
		DepthCap:           12,
		MultiPV:            5,
		PrimaryChoices:     3,
		CandidateWeights:   []float64{0.7, 0.2, 0.1},
		EvalNoise:          25,
		OpeningPreferences: cloneOpeningPreferences(defaultOpeningPreferences),
	},
	"level6": {
		Name:               "level6",
		SkillLevel:         11,
		Threads:            defaultThreads,
		HashMB:             64,
		MoveTimeMillis:     300,
		NodeCap:            0,
		DepthCap:           16,
		MultiPV:            2,
		PrimaryChoices:     2,
		CandidateWeights:   []float64{0.8, 0.2},
		EvalNoise:          10,
		OpeningPreferences: cloneOpeningPreferences(defaultOpeningPreferences),
	},
	"level7": {
		Name:               "level7",
		SkillLevel:         16,
		Threads:            defaultThreads,
		HashMB:             96,
		MoveTimeMillis:     500,
		NodeCap:            0,
		DepthCap:           20,
		MultiPV:            2,
		PrimaryChoices:     2,
		CandidateWeights:   []float64{0.85, 0.15},
		EvalNoise:          5,
		OpeningPreferences: cloneOpeningPreferences(defaultOpeningPreferences),
	},
	"level8": {
		Name:               "level8",
		SkillLevel:         20,
		Threads:            forlv8,
		HashMB:             128,
		MoveTimeMillis:     1000,
		NodeCap:            0,
		DepthCap:           30,
		MultiPV:            1,
		PrimaryChoices:     1,
		CandidateWeights:   []float64{1.0},
		EvalNoise:          0,
		OpeningPreferences: cloneOpeningPreferences(defaultOpeningPreferences),
	},
}

func GetPreset(name string) (DifficultyPreset, error) {
	switch name {
	case "beginner":
		name = "level1"
	case "intermediate":
		name = "level5"
	case "advanced":
		name = "level7"
	case "master":
		name = "level8"
	}
	presetMu.RLock()
	p, ok := DefaultPresets[name]
	presetMu.RUnlock()
	if ok {
		p.OpeningCatalog = cloneOpeningCatalogConfig(p.OpeningCatalog)
		return p, nil
	}
	return DifficultyPreset{}, fmt.Errorf("unknown chess preset: %s", name)
}

func SetPresetOpeningPreferences(name string, prefs []OpeningPreference) error {
	presetMu.Lock()
	defer presetMu.Unlock()

	preset, ok := DefaultPresets[name]
	if !ok {
		return fmt.Errorf("unknown chess preset: %s", name)
	}

	if err := validateOpeningPreferences(prefs); err != nil {
		return err
	}

	preset.OpeningPreferences = append([]OpeningPreference(nil), prefs...)
	DefaultPresets[name] = preset
	return nil
}

func SetPresetOpeningCatalog(name string, cfg OpeningCatalogConfig) error {
	presetMu.Lock()
	defer presetMu.Unlock()

	preset, ok := DefaultPresets[name]
	if !ok {
		return fmt.Errorf("unknown chess preset: %s", name)
	}

	normalized := normalizeOpeningCatalogConfig(cfg)
	if err := validateOpeningCatalog(normalized); err != nil {
		return err
	}

	preset.OpeningCatalog = normalized
	DefaultPresets[name] = preset
	return nil
}

func SetPresetOpeningStyle(name string, styleKey string) error {
	presetMu.Lock()
	defer presetMu.Unlock()

	preset, ok := DefaultPresets[name]
	if !ok {
		return fmt.Errorf("unknown chess preset: %s", name)
	}

	trimmedStyle := strings.TrimSpace(styleKey)
	if trimmedStyle == "" {
		preset.OpeningCatalog.Keys = nil
		preset.OpeningCatalog.StyleKey = ""
		preset.OpeningCatalog.Key = strings.TrimSpace(preset.OpeningCatalog.Key)
		DefaultPresets[name] = preset
		return nil
	}

	styleTokens := parseStyleKeys(trimmedStyle)
	if len(styleTokens) == 0 {
		return fmt.Errorf("opening style key required")
	}

	store, err := openingbook.LoadStyleCatalog()
	if err != nil {
		return fmt.Errorf("load opening style catalog: %w", err)
	}
	if store == nil {
		return fmt.Errorf("opening style catalog not configured")
	}

	collectedKeys := make([]string, 0, len(styleTokens))
	rawTokens := make([]string, 0)

	for _, token := range styleTokens {
		group := store.FindByKey(token)
		if group == nil {
			return fmt.Errorf("unknown opening style: %s", token)
		}
		collectedKeys = append(collectedKeys, group.Key)
		for _, entry := range group.Entries {
			ecoToken := strings.TrimSpace(entry.ECO)
			if ecoToken == "" {
				continue
			}
			rawTokens = append(rawTokens, ecoToken)
		}
	}

	tokens := normalizeCatalogTokens(rawTokens)
	if len(tokens) == 0 {
		return fmt.Errorf("opening styles %s have no ECO entries", strings.Join(collectedKeys, ","))
	}

	preset.OpeningCatalog.Keys = tokens
	preset.OpeningCatalog.StyleKey = strings.Join(collectedKeys, ",")
	preset.OpeningCatalog.Key = tokens[0]

	if err := validateOpeningCatalog(preset.OpeningCatalog); err != nil {
		return err
	}

	DefaultPresets[name] = preset
	return nil
}

func parseStyleKeys(input string) []string {
	replacer := strings.NewReplacer(",", " ", ";", " ", "+", " ", "|", " ")
	normalized := replacer.Replace(input)
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.ToLower(strings.TrimSpace(field))
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func AddPresetOpeningPreference(name string, pref OpeningPreference) error {
	presetMu.Lock()
	defer presetMu.Unlock()

	preset, ok := DefaultPresets[name]
	if !ok {
		return fmt.Errorf("unknown chess preset: %s", name)
	}

	if err := validateOpeningPreference(pref); err != nil {
		return err
	}

	preset.OpeningPreferences = append(preset.OpeningPreferences, pref)
	DefaultPresets[name] = preset
	return nil
}

func validateOpeningPreferences(prefs []OpeningPreference) error {
	for _, pref := range prefs {
		if err := validateOpeningPreference(pref); err != nil {
			return err
		}
	}
	return nil
}

func validateOpeningPreference(pref OpeningPreference) error {
	name := strings.TrimSpace(pref.Name)
	if name == "" {
		return fmt.Errorf("opening preference name required")
	}
	if len(pref.WhiteMoves) == 0 {
		return fmt.Errorf("opening preference %s must define white move triggers", name)
	}
	if len(pref.BlackMoves) == 0 {
		return fmt.Errorf("opening preference %s must define black move sequence", name)
	}
	color := strings.ToLower(strings.TrimSpace(pref.Color))
	if color == "" {
		color = "black"
	}
	if color != "black" {
		return fmt.Errorf("opening preference %s only supports black openings", name)
	}
	if pref.Probability <= 0 || pref.Probability > 1 {
		return fmt.Errorf("opening preference %s must have probability in (0,1]", name)
	}
	if pref.WeightMultiplier <= 0 || math.IsNaN(pref.WeightMultiplier) || math.IsInf(pref.WeightMultiplier, 0) {
		return fmt.Errorf("opening preference %s must have positive finite weight multiplier", name)
	}
	for i, mv := range pref.WhiteMoves {
		if strings.TrimSpace(mv) == "" {
			return fmt.Errorf("opening preference %s has empty white move at index %d", name, i)
		}
	}
	for i, mv := range pref.BlackMoves {
		if strings.TrimSpace(mv) == "" {
			return fmt.Errorf("opening preference %s has empty black move at index %d", name, i)
		}
	}
	return nil
}

func normalizeCatalogTokens(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tokens))
	result := make([]string, 0, len(tokens))
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeOpeningCatalogConfig(cfg OpeningCatalogConfig) OpeningCatalogConfig {
	normalized := OpeningCatalogConfig{
		Key:       strings.TrimSpace(cfg.Key),
		MaxPly:    cfg.MaxPly,
		MinWeight: cfg.MinWeight,
		StyleKey:  strings.TrimSpace(cfg.StyleKey),
	}
	color := strings.TrimSpace(cfg.Color)
	if color != "" {
		normalized.Color = strings.ToLower(color)
	}
	normalized.Keys = normalizeCatalogTokens(cfg.Keys)
	if normalized.Key == "" && len(normalized.Keys) > 0 {
		normalized.Key = normalized.Keys[0]
	}
	if normalized.StyleKey == "" {
		normalized.StyleKey = ""
	}
	return cloneOpeningCatalogConfig(normalized)
}

func validateOpeningCatalog(cfg OpeningCatalogConfig) error {
	key := strings.TrimSpace(cfg.Key)
	if len(cfg.Keys) == 0 && key == "" {
		if cfg.StyleKey != "" {
			return fmt.Errorf("opening catalog style key requires entries")
		}
		if cfg.MaxPly < 0 {
			return fmt.Errorf("opening catalog max ply must be >= 0: %d", cfg.MaxPly)
		}
		if cfg.MinWeight < 0 {
			return fmt.Errorf("opening catalog min weight must be >= 0: %d", cfg.MinWeight)
		}
		return nil
	}
	if cfg.MaxPly < 0 {
		return fmt.Errorf("opening catalog max ply must be >= 0: %d", cfg.MaxPly)
	}
	if cfg.MinWeight < 0 {
		return fmt.Errorf("opening catalog min weight must be >= 0: %d", cfg.MinWeight)
	}
	color := strings.ToLower(strings.TrimSpace(cfg.Color))
	if color == "" {
		color = "black"
	}
	switch color {
	case "black", "white", "both":
	default:
		return fmt.Errorf("opening catalog color must be one of white, black, both: %s", cfg.Color)
	}
	seen := make(map[string]struct{}, len(cfg.Keys))
	for i, token := range cfg.Keys {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			return fmt.Errorf("opening catalog key at index %d is empty", i)
		}
		lower := strings.ToLower(trimmed)
		if _, exists := seen[lower]; exists {
			return fmt.Errorf("opening catalog keys must be unique: duplicate %q", trimmed)
		}
		seen[lower] = struct{}{}
	}
	if cfg.StyleKey != "" && len(cfg.Keys) == 0 {
		return fmt.Errorf("opening catalog style key requires entries")
	}
	return nil
}

func ValidatePreset(p DifficultyPreset) error {
	switch {
	case p.SkillLevel < 0 || p.SkillLevel > 20:
		return fmt.Errorf("skill level %d out of range 0-20", p.SkillLevel)
	case p.Threads <= 0:
		return fmt.Errorf("threads must be > 0: %d", p.Threads)
	case p.HashMB <= 0:
		return fmt.Errorf("hash size must be > 0: %d", p.HashMB)
	case p.MultiPV <= 0:
		return fmt.Errorf("multipv must be > 0: %d", p.MultiPV)
	case p.PrimaryChoices <= 0:
		return fmt.Errorf("primary choices must be > 0: %d", p.PrimaryChoices)
	case p.PrimaryChoices > p.MultiPV:
		return fmt.Errorf("primary choices (%d) must not exceed multipv (%d)", p.PrimaryChoices, p.MultiPV)
	case len(p.CandidateWeights) == 0:
		return fmt.Errorf("candidate weights must not be empty")
	case len(p.CandidateWeights) < p.PrimaryChoices:
		return fmt.Errorf("candidate weights (%d) must cover primary choices (%d)", len(p.CandidateWeights), p.PrimaryChoices)
	}

	sum := 0.0
	for i := 0; i < p.PrimaryChoices; i++ {
		w := p.CandidateWeights[i]
		if w < 0 {
			return fmt.Errorf("candidate weight at index %d is negative: %f", i, w)
		}
		sum += w
	}
	if sum == 0 {
		return fmt.Errorf("candidate weights sum to zero")
	}
	if p.MoveTimeMillis < 0 {
		return fmt.Errorf("move time must be >= 0: %d", p.MoveTimeMillis)
	}
	if p.NodeCap < 0 {
		return fmt.Errorf("node cap must be >= 0: %d", p.NodeCap)
	}
	if p.DepthCap < 0 {
		return fmt.Errorf("depth cap must be >= 0: %d", p.DepthCap)
	}
	if p.EvalNoise < 0 {
		return fmt.Errorf("eval noise must be >= 0: %d", p.EvalNoise)
	}
	if err := validateOpeningPreferences(p.OpeningPreferences); err != nil {
		return err
	}
	if err := validateOpeningCatalog(p.OpeningCatalog); err != nil {
		return err
	}
	return nil
}
