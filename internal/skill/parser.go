package skill

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func scanRoot(root string, scope Scope) (ScanResult, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return ScanResult{}, nil
	}
	if err != nil {
		return ScanResult{}, err
	}
	if !info.IsDir() {
		return ScanResult{}, nil
	}
	var out []Skill
	var rejected []RejectedSkill
	var diagnostics []Diagnostic
	seen := make(map[string]bool)
	addSkill := func(path string) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = path
		}
		if seen[resolved] || len(out) >= maxSkillsPerRoot {
			return
		}
		seen[resolved] = true
		item, itemDiagnostics, err := parseFile(path, scope)
		if err != nil {
			name := filepath.Base(filepath.Dir(path))
			rejectedDiagnostics := []Diagnostic{
				diagnostic("", name, path, "invalid_skill", "error", err.Error(), ""),
			}
			rejected = append(rejected, RejectedSkill{
				Name:        name,
				Path:        path,
				Scope:       scope,
				Diagnostics: rejectedDiagnostics,
			})
			diagnostics = append(diagnostics, rejectedDiagnostics...)
			return
		}
		item.Diagnostics = itemDiagnostics
		diagnostics = append(diagnostics, itemDiagnostics...)
		out = append(out, item)
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			targetInfo, err := os.Stat(target)
			if err != nil {
				return nil
			}
			if targetInfo.IsDir() {
				mainPath := filepath.Join(target, "SKILL.md")
				mainInfo, err := os.Stat(mainPath)
				if err == nil && !mainInfo.IsDir() {
					addSkill(mainPath)
				}
			} else if strings.EqualFold(entry.Name(), "SKILL.md") {
				addSkill(path)
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(entry.Name(), "SKILL.md") {
			return nil
		}
		if len(out) >= maxSkillsPerRoot {
			return filepath.SkipAll
		}
		addSkill(path)
		return nil
	})
	return ScanResult{Skills: out, Inventory: out, Rejected: rejected, Diagnostics: diagnostics}, err
}

func parseFile(path string, scope Scope) (Skill, []Diagnostic, error) {
	file, err := os.Open(path)
	if err != nil {
		return Skill{}, nil, err
	}
	defer file.Close()
	data, err := ioReadAllLimit(file, maxSkillFileBytes)
	if err != nil {
		return Skill{}, nil, err
	}
	meta, body, fields, hasFrontmatter, err := parseDocument(string(data))
	if err != nil {
		return Skill{}, nil, err
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	if name == "" || len([]rune(name)) > 128 {
		return Skill{}, nil, errors.New("skill name is empty or too long")
	}
	root := filepath.Dir(path)
	manifest := SkillManifest{
		Name:                 name,
		Description:          strings.TrimSpace(meta.Description),
		AllowedTools:         compactStrings(meta.AllowedTools),
		RequiredTools:        compactStrings(meta.RequiredTools),
		RequiredCapabilities: compactStrings(meta.RequiredCapabilities),
		License:              strings.TrimSpace(meta.License),
		Compatibility:        strings.TrimSpace(meta.Compatibility),
		Metadata:             compactMap(meta.Metadata),
		Category:             strings.TrimSpace(meta.Category),
	}
	item := Skill{
		Ref:                  skillRef(scope, name),
		Name:                 name,
		Description:          manifest.Description,
		Scope:                scope,
		Path:                 path,
		Root:                 root,
		MainPath:             path,
		Manifest:             manifest,
		Enabled:              true,
		AllowedTools:         manifest.AllowedTools,
		RequiredTools:        manifest.RequiredTools,
		RequiredCapabilities: manifest.RequiredCapabilities,
		Resources:            collectResources(root, path),
		ContentHash:          hashBytes(data),
		Body:                 strings.TrimSpace(body),
	}
	var diagnostics []Diagnostic
	if !hasFrontmatter {
		diagnostics = append(diagnostics, diagnostic(item.Ref, item.Name, path, "missing_frontmatter", "warning", "SKILL.md has no YAML frontmatter", "frontmatter"))
	}
	if item.Description == "" {
		diagnostics = append(diagnostics, diagnostic(item.Ref, item.Name, path, "missing_description", "warning", "SKILL.md has no description", "description"))
	}
	for _, field := range unsupportedManifestFields(fields) {
		diagnostics = append(diagnostics, diagnostic(item.Ref, item.Name, path, "unsupported_field", "warning", "unsupported frontmatter field: "+field, field))
	}
	return item, diagnostics, nil
}

func parseDocument(source string) (SkillManifest, string, map[string]bool, bool, error) {
	if !strings.HasPrefix(source, "---\n") && !strings.HasPrefix(source, "---\r\n") {
		return SkillManifest{}, source, nil, false, nil
	}
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return SkillManifest{}, "", nil, true, errors.New("unterminated YAML frontmatter")
	}
	end += 4
	raw := normalized[4:end]
	var meta SkillManifest
	if err := yaml.Unmarshal([]byte(raw), &meta); err != nil {
		return SkillManifest{}, "", nil, true, fmt.Errorf("decode frontmatter: %w", err)
	}
	fields := make(map[string]bool)
	var rawFields map[string]any
	if err := yaml.Unmarshal([]byte(raw), &rawFields); err == nil {
		for key := range rawFields {
			fields[key] = true
		}
	}
	return meta, normalized[end+5:], fields, true, nil
}

func ioReadAllLimit(file *os.File, max int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > max {
		return nil, fmt.Errorf("skill file exceeds %d bytes", max)
	}
	return io.ReadAll(io.LimitReader(file, max+1))
}

func compactStrings(values []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func compactMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			value = strings.TrimSpace(text)
			if value == "" {
				continue
			}
		}
		out[key] = value
	}
	return out
}

func completeSkillDefaults(item *Skill, scope Scope) {
	if item.Scope == "" {
		item.Scope = scope
	}
	item.Name = strings.TrimSpace(item.Name)
	item.Description = strings.TrimSpace(item.Description)
	item.AllowedTools = compactStrings(item.AllowedTools)
	item.RequiredTools = compactStrings(item.RequiredTools)
	item.RequiredCapabilities = compactStrings(item.RequiredCapabilities)
	item.Manifest.Name = item.Name
	item.Manifest.Description = item.Description
	item.Manifest.AllowedTools = item.AllowedTools
	item.Manifest.RequiredTools = item.RequiredTools
	item.Manifest.RequiredCapabilities = item.RequiredCapabilities
	if item.Ref == "" && item.Name != "" {
		item.Ref = skillRef(item.Scope, item.Name)
	}
	if item.MainPath == "" {
		item.MainPath = item.Path
	}
	if item.Root == "" && item.MainPath != "" {
		item.Root = filepath.Dir(item.MainPath)
	}
	if item.ContentHash == "" {
		item.ContentHash = hashBytes([]byte(item.Body))
	}
}
