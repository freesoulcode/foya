package contextdata

import (
	"encoding/json"
	"errors"
	"fmt"

	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func (s *Store) ListRules(scope Scope, projectID string) []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope == ScopeProject || (scope == "" && projectID != "") {
		_ = s.ensureProjectRulesLoadedLocked(projectID)
	}
	items := make([]Rule, 0, len(s.rules))
	for _, item := range s.rules {
		if matches(item.Scope, item.ProjectID, scope, projectID) {
			items = append(items, item)
		}
	}
	sortRules(items)
	return items
}

func (s *Store) ListMemories(scope Scope, projectID string) []Memory {
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope == ScopeProject || (scope == "" && projectID != "") {
		_ = s.ensureProjectLoadedLocked(projectID)
	}
	items := make([]Memory, 0, 2)
	if scope == "" || scope == ScopeGlobal {
		if item, ok := s.memoryForScopeLocked(ScopeGlobal, ""); ok {
			items = append(items, item)
		}
	}
	if (scope == ScopeProject || scope == "") && projectID != "" {
		if item, ok := s.memoryForScopeLocked(ScopeProject, projectID); ok {
			items = append(items, item)
		}
	}
	sortMemories(items)
	return items
}

func (s *Store) EffectiveRules(projectID string) []Rule {
	return s.ListRules("", projectID)
}

func (s *Store) ActiveRules(projectID, activity string) []Rule {
	items := s.EffectiveRules(projectID)
	active := make([]Rule, 0, len(items))
	for _, item := range items {
		switch item.Trigger {
		case RuleAlways:
			active = append(active, item)
		case RuleGlob:
			if ruleMatchesActivity(item, activity) {
				active = append(active, item)
			}
		case RuleManual:
			if strings.Contains(activity, "@"+item.Name) ||
				strings.Contains(activity, "@"+item.ID) {
				active = append(active, item)
			}
		}
	}
	return active
}

func (s *Store) AvailableRules(projectID string) []Rule {
	items := s.EffectiveRules(projectID)
	available := make([]Rule, 0, len(items))
	for _, item := range items {
		if item.Trigger == RuleModelDecision {
			available = append(available, item)
		}
	}
	return available
}

func (s *Store) GetRule(projectID, ref string) (Rule, error) {
	for _, item := range s.EffectiveRules(projectID) {
		if item.Trigger == RuleModelDecision && (item.ID == ref || item.Name == ref) {
			return item, nil
		}
	}
	return Rule{}, ErrNotFound
}

func (s *Store) EffectiveMemories(projectID string) []Memory {
	if !s.MemoryEnabled() {
		return nil
	}
	return s.ListMemories("", projectID)
}

func ruleMatchesActivity(rule Rule, activity string) bool {
	activity = filepath.ToSlash(activity)
	for _, pattern := range rule.Globs {
		expression := globExpression(filepath.ToSlash(pattern))
		if matched, _ := regexp.MatchString(expression, activity); matched {
			return true
		}
	}
	return false
}

func globExpression(pattern string) string {
	var out strings.Builder
	out.WriteString(`(^|[\s"'=:,(])`)
	for i := 0; i < len(pattern); {
		switch {
		case i+1 < len(pattern) && pattern[i:i+2] == "**":
			out.WriteString(`.*`)
			i += 2
		case pattern[i] == '*':
			out.WriteString(`[^/\s"'=:,)]*`)
			i++
		case pattern[i] == '?':
			out.WriteString(`[^/\s"'=:,)]`)
			i++
		default:
			out.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
			i++
		}
	}
	out.WriteString(`($|[\s"'=:,)])`)
	return out.String()
}

func (s *Store) CreateRule(
	scope Scope,
	projectID, content string,
	options ...RuleOptions,
) (Rule, error) {
	content, err := validate(scope, projectID, content, maxRuleRunes)
	if err != nil {
		return Rule{}, err
	}
	now := time.Now()
	item := Rule{
		ID: newID(), Scope: scope, ProjectID: normalizedProjectID(scope, projectID),
		Content: content, CreatedAt: now, UpdatedAt: now,
	}
	if err := applyRuleOptions(&item, options); err != nil {
		return Rule{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope == ScopeProject {
		if err := s.ensureProjectRulesLoadedLocked(projectID); err != nil {
			return Rule{}, err
		}
	}
	s.rules[item.ID] = item
	if err := s.writeRuleLocked(item); err != nil {
		delete(s.rules, item.ID)
		return Rule{}, err
	}
	ev := Event{Seq: s.seq + 1, Kind: "rule_created", Rule: &item, Time: now}
	if err := s.appendRuleEventLocked(ev); err != nil {
		delete(s.rules, item.ID)
		_ = s.deleteRuleFileLocked(item)
		return Rule{}, err
	}
	s.publishLocked(ev)
	return item, nil
}

func (s *Store) UpdateRule(id, content string, options ...RuleOptions) (Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.rules[id]
	if !ok {
		if err := s.loadAllProjectRulesLocked(); err != nil {
			return Rule{}, err
		}
		item, ok = s.rules[id]
	}
	if !ok {
		return Rule{}, ErrNotFound
	}
	cleaned, err := validate(item.Scope, item.ProjectID, content, maxRuleRunes)
	if err != nil {
		return Rule{}, err
	}
	previous := item
	item.Content = cleaned
	if err := applyRuleOptions(&item, options); err != nil {
		return Rule{}, err
	}
	item.UpdatedAt = time.Now()
	s.rules[id] = item
	previousFile := s.ruleFiles[id]
	if err := s.writeRuleLocked(item); err != nil {
		s.rules[id] = previous
		return Rule{}, err
	}
	currentFile := s.ruleFiles[id]
	if item.Scope == ScopeProject && previousFile != "" && previousFile != currentFile {
		if err := os.Remove(previousFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.rules[id] = previous
			s.ruleFiles[id] = previousFile
			_ = os.Remove(currentFile)
			return Rule{}, err
		}
	}
	ev := Event{Seq: s.seq + 1, Kind: "rule_updated", Rule: &item, Time: item.UpdatedAt}
	if err := s.appendRuleEventLocked(ev); err != nil {
		s.rules[id] = previous
		if item.Scope == ScopeProject && previousFile != "" && previousFile != currentFile {
			_ = os.Remove(currentFile)
			s.ruleFiles[id] = previousFile
		}
		_ = s.writeRuleLocked(previous)
		return Rule{}, err
	}
	s.publishLocked(ev)
	return item, nil
}

func (s *Store) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.rules[id]
	if !ok {
		if err := s.loadAllProjectRulesLocked(); err != nil {
			return err
		}
		item, ok = s.rules[id]
	}
	if !ok {
		return ErrNotFound
	}
	delete(s.rules, id)
	if err := s.deleteRuleFileLocked(item); err != nil {
		s.rules[id] = item
		return err
	}
	ev := Event{Seq: s.seq + 1, Kind: "rule_deleted", DeletedID: id, Time: time.Now()}
	if err := s.appendRuleEventLocked(ev); err != nil {
		s.rules[id] = item
		_ = s.writeRuleLocked(item)
		return err
	}
	s.publishLocked(ev)
	return nil
}

func (s *Store) ensureProjectRulesLoadedLocked(projectID string) error {
	if projectID == "" || s.loadedRuleProjects[projectID] {
		return nil
	}
	return s.loadProjectRulesLocked(projectID)
}

func (s *Store) loadAllProjectRulesLocked() error {
	if s.listProjectPaths == nil {
		return nil
	}
	for projectID := range s.listProjectPaths() {
		if err := s.ensureProjectRulesLoadedLocked(projectID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadGlobalRulesLocked() error {
	path := filepath.Join(s.ruleRoot, "user_rules.md")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	items, err := parseRuleMarkdown(data, ScopeGlobal, "", "", path, info.ModTime())
	if err != nil {
		return fmt.Errorf("parse rule file %s: %w", path, err)
	}
	for _, item := range items {
		s.rules[item.ID] = item
		s.ruleFiles[item.ID] = path
	}
	return nil
}

func (s *Store) loadProjectRulesLocked(projectID string) error {
	root, err := s.projectPathLocked(projectID)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, ".foya", "rules")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		s.loadedRuleProjects[projectID] = true
		return nil
	} else if err != nil {
		return err
	}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		depth := ruleDirectoryDepth(relative, entry.IsDir())
		if entry.IsDir() {
			if relative != "." && depth > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > 3 || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rulePath := filepath.ToSlash(relative)
		items, err := parseRuleMarkdown(
			data, ScopeProject, projectID, rulePath, path, info.ModTime(),
		)
		if err != nil {
			return fmt.Errorf("parse rule file %s: %w", path, err)
		}
		if len(items) > 1 {
			return fmt.Errorf("parse rule file %s: project rule files contain one rule each", path)
		}
		for _, item := range items {
			s.rules[item.ID] = item
			s.ruleFiles[item.ID] = path
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.loadedRuleProjects[projectID] = true
	return nil
}

func (s *Store) writeRuleLocked(item Rule) error {
	if item.Scope == ScopeGlobal {
		path := filepath.Join(s.ruleRoot, "user_rules.md")
		items := make([]Rule, 0)
		for _, candidate := range s.rules {
			if candidate.Scope == ScopeGlobal {
				items = append(items, candidate)
			}
		}
		sortRules(items)
		data, err := renderGlobalRulesMarkdown(items)
		if err != nil {
			return err
		}
		if err := writeAtomicFile(path, ".rules-*.tmp", data); err != nil {
			return err
		}
		for _, candidate := range items {
			s.ruleFiles[candidate.ID] = path
		}
		return nil
	}
	root, err := s.projectPathLocked(item.ProjectID)
	if err != nil {
		return err
	}
	relative := item.Path
	if relative == "" {
		relative = item.ID + ".md"
	}
	path := filepath.Join(root, ".foya", "rules", filepath.FromSlash(relative))
	data, err := renderProjectRuleMarkdown(item)
	if err != nil {
		return err
	}
	if err := writeAtomicFile(path, ".rule-*.tmp", data); err != nil {
		return err
	}
	s.ruleFiles[item.ID] = path
	return nil
}

func (s *Store) deleteRuleFileLocked(item Rule) error {
	if item.Scope == ScopeGlobal {
		if err := s.writeRuleLocked(item); err != nil {
			return err
		}
		delete(s.ruleFiles, item.ID)
		return nil
	}
	path := s.ruleFiles[item.ID]
	if path == "" {
		return ErrNotFound
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	delete(s.ruleFiles, item.ID)
	return nil
}

func (s *Store) projectPathLocked(projectID string) (string, error) {
	if s.resolveProjectPath != nil {
		if projectPath, ok := s.resolveProjectPath(projectID); ok {
			return projectPath, nil
		}
		return "", fmt.Errorf("resolve project path for %q", projectID)
	}
	return filepath.Join(s.fallbackProjectDir, projectID), nil
}

func renderGlobalRulesMarkdown(items []Rule) ([]byte, error) {
	var out strings.Builder
	out.WriteString("# Foya User Rules\n")
	for _, item := range items {
		data, err := json.Marshal(ruleMetadata{
			ID: item.ID, Name: item.Name, Description: item.Description,
			Trigger: item.Trigger, Globs: item.Globs, Path: item.Path,
			Scope: item.Scope, ProjectID: item.ProjectID,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
		if err != nil {
			return nil, err
		}
		out.WriteString("\n<!-- foya-rule: ")
		out.Write(data)
		out.WriteString(" -->\n")
		out.WriteString(item.Content)
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}

func renderProjectRuleMarkdown(item Rule) ([]byte, error) {
	data, err := yaml.Marshal(ruleFrontmatter{
		ID: item.ID, Name: item.Name, Description: item.Description,
		Trigger: item.Trigger, Globs: item.Globs,
	})
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(data) + "---\n" + item.Content + "\n"), nil
}

func parseRuleMarkdown(
	data []byte,
	scope Scope,
	projectID, rulePath, sourcePath string,
	modTime time.Time,
) ([]Rule, error) {
	if frontmatter, body, ok := splitFrontmatter(string(data)); ok {
		var meta ruleFrontmatter
		if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
			return nil, err
		}
		content := strings.TrimSpace(body)
		if content == "" {
			return nil, nil
		}
		id := strings.TrimSpace(meta.ID)
		if id == "" {
			id = stableID(sourcePath)
		}
		item := Rule{
			ID: id, Name: meta.Name, Description: meta.Description,
			Trigger: meta.Trigger, Globs: append([]string(nil), meta.Globs...),
			Path: rulePath, Scope: scope, ProjectID: normalizedProjectID(scope, projectID),
			Content: content, CreatedAt: modTime, UpdatedAt: modTime,
		}
		if err := applyRuleDefaults(&item); err != nil {
			return nil, err
		}
		return []Rule{item}, nil
	}
	const prefix = "<!-- foya-rule: "
	const suffix = " -->"
	lines := strings.Split(string(data), "\n")
	var items []Rule
	var current *ruleMetadata
	var content strings.Builder
	foundMarker := false
	flush := func() {
		if current == nil {
			return
		}
		text := strings.TrimSpace(content.String())
		if text != "" && utf8.RuneCountInString(text) <= maxRuleRunes {
			createdAt := current.CreatedAt
			if createdAt.IsZero() {
				createdAt = modTime
			}
			updatedAt := current.UpdatedAt
			if updatedAt.IsZero() {
				updatedAt = modTime
			}
			items = append(items, Rule{
				ID: current.ID, Name: current.Name, Description: current.Description,
				Trigger: current.Trigger, Globs: append([]string(nil), current.Globs...),
				Path: current.Path, Scope: scope, ProjectID: normalizedProjectID(scope, projectID),
				Content: text, CreatedAt: createdAt, UpdatedAt: updatedAt,
			})
		}
		current = nil
		content.Reset()
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) && strings.HasSuffix(trimmed, suffix) {
			foundMarker = true
			flush()
			var meta ruleMetadata
			raw := strings.TrimSuffix(strings.TrimPrefix(trimmed, prefix), suffix)
			if err := json.Unmarshal([]byte(raw), &meta); err != nil {
				return nil, err
			}
			if meta.ID == "" {
				return nil, errors.New("rule id is required")
			}
			current = &meta
			continue
		}
		if current != nil {
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}
	flush()
	if foundMarker {
		for i := range items {
			if err := applyRuleDefaults(&items[i]); err != nil {
				return nil, err
			}
		}
		return items, nil
	}
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "# Foya User Rules") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "# Foya User Rules"))
	}
	if text == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(text) > maxRuleRunes {
		return nil, ErrTooLarge
	}
	items = []Rule{{
		ID: stableID(sourcePath), Path: rulePath,
		Scope: scope, ProjectID: normalizedProjectID(scope, projectID),
		Content: text, CreatedAt: modTime, UpdatedAt: modTime,
	}}
	if err := applyRuleDefaults(&items[0]); err != nil {
		return nil, err
	}
	return items, nil
}

func applyRuleOptions(item *Rule, options []RuleOptions) error {
	if len(options) > 0 {
		option := options[0]
		item.Name = strings.TrimSpace(option.Name)
		item.Description = strings.TrimSpace(option.Description)
		item.Trigger = option.Trigger
		item.Globs = cleanStrings(option.Globs)
		if item.Scope == ScopeProject && strings.TrimSpace(option.Path) != "" {
			path, err := normalizeRulePath(option.Path)
			if err != nil {
				return err
			}
			item.Path = path
		}
	}
	return applyRuleDefaults(item)
}

func applyRuleDefaults(item *Rule) error {
	if item.Name == "" {
		name := strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))
		if name == "" || name == "." {
			name = "rule-" + item.ID[:min(8, len(item.ID))]
		}
		item.Name = name
	}
	if item.Scope == ScopeProject && item.Path == "" {
		item.Path = defaultRulePath(item.Name, item.ID)
	}
	if item.Trigger == "" {
		if item.Scope == ScopeProject && ruleDirectoryDepth(item.Path, false) > 0 {
			item.Trigger = RuleGlob
		} else {
			item.Trigger = RuleAlways
		}
	}
	switch item.Trigger {
	case RuleAlways, RuleManual:
	case RuleModelDecision:
		if item.Description == "" {
			return fmt.Errorf("%w: model_decision requires description", ErrInvalidRule)
		}
	case RuleGlob:
		if len(item.Globs) == 0 {
			dir := filepath.ToSlash(filepath.Dir(item.Path))
			if dir == "." || dir == "" {
				return fmt.Errorf("%w: glob trigger requires globs", ErrInvalidRule)
			}
			item.Globs = []string{dir + "/**"}
		}
	default:
		return fmt.Errorf("%w: unsupported trigger %q", ErrInvalidRule, item.Trigger)
	}
	for _, pattern := range item.Globs {
		if strings.TrimSpace(pattern) == "" {
			return fmt.Errorf("%w: glob cannot be empty", ErrInvalidRule)
		}
	}
	return nil
}

func defaultRulePath(name, id string) string {
	var out strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_':
			out.WriteRune(r)
			lastDash = r == '-'
		case !lastDash:
			out.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(out.String(), "-_")
	if slug == "" {
		slug = "rule-" + id[:min(8, len(id))]
	}
	return slug + ".md"
}

func normalizeRulePath(value string) (string, error) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "/")
	cleaned := filepath.ToSlash(filepath.Clean(value))
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: invalid rule path", ErrInvalidRule)
	}
	if strings.ToLower(filepath.Ext(cleaned)) != ".md" {
		cleaned += ".md"
	}
	if ruleDirectoryDepth(cleaned, false) > 3 {
		return "", fmt.Errorf("%w: rule path exceeds three directory levels", ErrInvalidRule)
	}
	return cleaned, nil
}

func ruleDirectoryDepth(relative string, isDir bool) int {
	if relative == "." || relative == "" {
		return 0
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if !isDir {
		parts = parts[:len(parts)-1]
	}
	return len(parts)
}

func splitFrontmatter(source string) (string, string, bool) {
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", source, false
	}
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return "", source, false
	}
	end += 4
	return normalized[4:end], normalized[end+5:], true
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			out = append(out, filepath.ToSlash(cleaned))
		}
	}
	return out
}

func sortRules(items []Rule) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return items[i].Scope == ScopeGlobal
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.Before(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
}
