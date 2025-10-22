package openingbook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	styleCatalogOnce sync.Once
	styleCatalog     *StyleCatalogStore
	styleCatalogErr  error
)

type StyleEntry struct {
	ECO   string `json:"eco"`
	Title string `json:"title"`
}

type StyleGroup struct {
	Key         string       `json:"key"`
	Label       string       `json:"label"`
	Description string       `json:"description"`
	Entries     []StyleEntry `json:"entries"`
}

type styleCatalogFile struct {
	Groups []StyleGroup `json:"groups"`
}

type StyleCatalogStore struct {
	groups []StyleGroup
	byKey  map[string]*StyleGroup
	byECO  map[string][]*StyleGroup
}

func LoadStyleCatalog() (*StyleCatalogStore, error) {
	styleCatalogOnce.Do(func() {
		path, resolveErr := resolveStyleCatalogPath()
		if resolveErr != nil {
			styleCatalogErr = resolveErr
			return
		}
		if strings.TrimSpace(path) == "" {
			styleCatalog = nil
			return
		}
		store, err := LoadStyleCatalogFromPath(path)
		if err != nil {
			styleCatalogErr = err
			return
		}
		styleCatalog = store
	})
	return styleCatalog, styleCatalogErr
}

func LoadStyleCatalogFromPath(path string) (*StyleCatalogStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("style catalog path required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open style catalog %q: %w", path, err)
	}
	defer file.Close()

	var payload styleCatalogFile
	if err := json.NewDecoder(file).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode style catalog %q: %w", path, err)
	}
	return newStyleCatalogStore(payload.Groups)
}

func newStyleCatalogStore(groups []StyleGroup) (*StyleCatalogStore, error) {
	store := &StyleCatalogStore{
		groups: make([]StyleGroup, 0, len(groups)),
		byKey:  make(map[string]*StyleGroup, len(groups)),
		byECO:  make(map[string][]*StyleGroup),
	}

	for _, group := range groups {
		key := strings.TrimSpace(group.Key)
		if key == "" {
			return nil, fmt.Errorf("style catalog group missing key")
		}

		groupCopy := StyleGroup{
			Key:         key,
			Label:       strings.TrimSpace(group.Label),
			Description: strings.TrimSpace(group.Description),
			Entries:     make([]StyleEntry, 0, len(group.Entries)),
		}

		for _, entry := range group.Entries {
			eco := normalizeECOCode(entry.ECO)
			if eco == "" {
				continue
			}
			groupCopy.Entries = append(groupCopy.Entries, StyleEntry{
				ECO:   eco,
				Title: strings.TrimSpace(entry.Title),
			})
		}

		store.groups = append(store.groups, groupCopy)

		keyToken := normalizeCatalogToken(groupCopy.Key)
		if keyToken != "" {
			store.byKey[keyToken] = &store.groups[len(store.groups)-1]
		}

		for idx := range groupCopy.Entries {
			entry := &groupCopy.Entries[idx]
			store.byECO[entry.ECO] = append(store.byECO[entry.ECO], &store.groups[len(store.groups)-1])
		}
	}

	return store, nil
}

func (s *StyleCatalogStore) Groups() []StyleGroup {
	if s == nil {
		return nil
	}
	out := make([]StyleGroup, len(s.groups))
	copy(out, s.groups)
	return out
}

func (s *StyleCatalogStore) FindByKey(key string) *StyleGroup {
	if s == nil {
		return nil
	}
	token := normalizeCatalogToken(key)
	if token == "" {
		return nil
	}
	group, ok := s.byKey[token]
	if !ok {
		return nil
	}
	copyGroup := *group
	copyGroup.Entries = append([]StyleEntry(nil), group.Entries...)
	return &copyGroup
}

func (s *StyleCatalogStore) FindGroupsForECO(eco string) []StyleGroup {
	if s == nil {
		return nil
	}
	normalized := normalizeECOCode(eco)
	if normalized == "" {
		return nil
	}
	groups := s.byECO[normalized]
	if len(groups) == 0 {
		return nil
	}
	out := make([]StyleGroup, 0, len(groups))
	for _, group := range groups {
		copyGroup := *group
		copyGroup.Entries = append([]StyleEntry(nil), group.Entries...)
		out = append(out, copyGroup)
	}
	return out
}

func resolveStyleCatalogPath() (string, error) {
	if envPath := os.Getenv("CHESS_OPENING_STYLES_PATH"); envPath != "" {
		if exists(envPath) {
			return envPath, nil
		}
		return "", fmt.Errorf("env CHESS_OPENING_STYLES_PATH points to missing file: %s", envPath)
	}

	for _, candidate := range defaultStyleCatalogPaths() {
		if exists(candidate) {
			return candidate, nil
		}
	}

	return "", nil
}

func defaultStyleCatalogPaths() []string {
	return []string{
		filepath.Join("resources", "opening", "catalog_styles.json"),
	}
}

func normalizeECOCode(code string) string {
	trimmed := strings.ToUpper(strings.TrimSpace(code))
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range trimmed {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
