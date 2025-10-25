package msgcat

import (
    "embed"
    "errors"
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "sync"
    "text/template"

    yaml "gopkg.in/yaml.v3"
)

//go:embed messages.ko.yaml
var defaultFiles embed.FS

// Catalog loads string templates from embedded defaults and an optional override directory.
// Values are rendered with text/template (missing keys cause errors).
type Catalog struct {
    mu   sync.RWMutex
    data map[string]string // flattened dot-keys → template text
}

// New loads the embedded default messages and then applies overrides from dir if provided.
func New(overrideDir string) (*Catalog, error) {
    base := &Catalog{data: make(map[string]string)}

    if err := base.loadEmbedded(); err != nil {
        return nil, err
    }
    if strings.TrimSpace(overrideDir) != "" {
        if err := base.applyDir(overrideDir); err != nil {
            return nil, err
        }
    }
    return base, nil
}

func (c *Catalog) loadEmbedded() error {
    raw, err := fs.ReadFile(defaultFiles, "messages.ko.yaml")
    if err != nil {
        return fmt.Errorf("read embedded messages: %w", err)
    }
    return c.applyYAML(raw)
}

func (c *Catalog) applyDir(dir string) error {
    entries, err := os.ReadDir(dir)
    if err != nil {
        return fmt.Errorf("read template dir: %w", err)
    }
    // Sort for deterministic order
    files := make([]string, 0, len(entries))
    for _, e := range entries {
        if e.IsDir() { continue }
        n := e.Name()
        ext := strings.ToLower(filepath.Ext(n))
        if ext == ".yaml" || ext == ".yml" { files = append(files, n) }
    }
    sort.Strings(files)
    // Guard against duplicate keys across override files
    seen := make(map[string]string) // key -> filename
    for _, name := range files {
        b, err := os.ReadFile(filepath.Join(dir, name))
        if err != nil { return fmt.Errorf("read %s: %w", name, err) }
        flat, err := parseYAMLToFlat(b)
        if err != nil { return fmt.Errorf("parse %s: %w", name, err) }
        // Detect duplicates before applying
        for k := range flat {
            if prev, ok := seen[k]; ok {
                return fmt.Errorf("duplicate override key %q in %s and %s", k, prev, name)
            }
            seen[k] = name
        }
        c.mu.Lock()
        for k, v := range flat { c.data[k] = v }
        c.mu.Unlock()
    }
    return nil
}

func parseYAMLToFlat(b []byte) (map[string]string, error) {
    var m map[string]any
    if err := yaml.Unmarshal(b, &m); err != nil {
        return nil, err
    }
    flat := make(map[string]string)
    if err := flattenStrings(m, "", flat); err != nil {
        return nil, err
    }
    return flat, nil
}

func (c *Catalog) applyYAML(b []byte) error {
    flat, err := parseYAMLToFlat(b)
    if err != nil { return err }
    c.mu.Lock()
    for k, v := range flat {
        c.data[k] = v // override
    }
    c.mu.Unlock()
    return nil
}

func flattenStrings(src any, prefix string, out map[string]string) error {
    switch v := src.(type) {
    case map[string]any:
        for k, vv := range v {
            key := k
            if prefix != "" { key = prefix + "." + k }
            if err := flattenStrings(vv, key, out); err != nil { return err }
        }
        return nil
    case map[any]any: // tolerate legacy YAML decoders
        tmp := make(map[string]any)
        for kk, vv := range v {
            tmp[fmt.Sprint(kk)] = vv
        }
        return flattenStrings(tmp, prefix, out)
    case string:
        if prefix == "" { return errors.New("string value without key prefix") }
        out[prefix] = v
        return nil
    case nil:
        return nil
    default:
        // Only string leaves are allowed to avoid type confusion
        return fmt.Errorf("unsupported value at %s: %T", prefix, v)
    }
}

// Render executes a template by key with the provided data map.
// Missing keys cause errors; caller should provide safe fallback.
func (c *Catalog) Render(key string, data any) (string, error) {
    c.mu.RLock()
    tpl, ok := c.data[strings.TrimSpace(key)]
    c.mu.RUnlock()
    if !ok || strings.TrimSpace(tpl) == "" {
        return "", fmt.Errorf("template not found: %s", key)
    }
    t, err := template.New(key).Option("missingkey=error").Parse(tpl)
    if err != nil { return "", err }
    var b strings.Builder
    if err := t.Execute(&b, data); err != nil { return "", err }
    return b.String(), nil
}

