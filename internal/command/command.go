// Package command owns user-invoked commands. A custom command is a Markdown
// prompt file; builtin commands are narrow, deterministic kernel controls.
package command

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Scope string

const (
	ScopeBuiltin Scope = "builtin"
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

type Kind string

const (
	KindPrompt   Kind = "prompt"
	KindWorkflow Kind = "workflow"
)

const (
	maxCommandFileBytes      = 256 << 10
	maxCommandDirectoryDepth = 3
)

var (
	ErrNotFound       = errors.New("command not found")
	ErrInvalidScope   = errors.New("invalid command scope")
	ErrInvalidName    = errors.New("invalid command name")
	ErrReservedName   = errors.New("command name is reserved")
	ErrDuplicateName  = errors.New("duplicate command name")
	ErrInvalidContent = errors.New("invalid command content")
)

var commandNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9:_-]{0,63}$`)

// Command is the public command descriptor. Body only exists for custom prompt
// commands and is omitted from lightweight lists.
type Command struct {
	Ref         string    `json:"ref"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Scope       Scope     `json:"scope"`
	ProjectID   string    `json:"project_id,omitempty"`
	Kind        Kind      `json:"kind"`
	Path        string    `json:"path,omitempty"`
	Body        string    `json:"body,omitempty"`
	Builtin     bool      `json:"builtin,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type CreateInput struct {
	Scope     Scope
	ProjectID string
	Name      string
	Body      string
}

type UpdateInput struct {
	Scope       Scope
	ProjectID   string
	Name        string
	Description string
	Body        string
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Manager resolves commands from a global root and arbitrary project roots.
type Manager struct {
	homeDir string
}

func NewManager(homeDir string) *Manager {
	return &Manager{homeDir: homeDir}
}

func GlobalRoot(homeDir string) string {
	return filepath.Join(homeDir, ".foya", "commands")
}

func ProjectRoot(projectPath string) string {
	return filepath.Join(projectPath, ".foya", "commands")
}

// Builtins returns the explicit workflows implemented by the kernel.
func Builtins() []Command {
	return []Command{
		{
			Ref: "builtin:plan", Name: "plan", Scope: ScopeBuiltin, Kind: KindWorkflow, Builtin: true,
			Description: "Start a Plan workflow",
		},
		{
			Ref: "builtin:spec", Name: "spec", Scope: ScopeBuiltin, Kind: KindWorkflow, Builtin: true,
			Description: "Start a Spec workflow",
		},
		{
			Ref: "builtin:goal", Name: "goal", Scope: ScopeBuiltin, Kind: KindWorkflow, Builtin: true,
			Description: "Start a Goal workflow",
		},
	}
}

// ListScope returns all commands in exactly one user-configured scope.
func (m *Manager) ListScope(ctx context.Context, scope Scope, projectID, projectPath string) ([]Command, error) {
	switch scope {
	case ScopeGlobal:
		return scan(ctx, GlobalRoot(m.homeDir), scope, "")
	case ScopeProject:
		if projectID == "" || projectPath == "" {
			return nil, ErrInvalidScope
		}
		return scan(ctx, ProjectRoot(projectPath), scope, projectID)
	default:
		return nil, ErrInvalidScope
	}
}

// List returns effective commands for a project. Builtins are first and cannot
// be shadowed. Project commands override global commands with the same name.
func (m *Manager) List(ctx context.Context, projectID, projectPath string) ([]Command, error) {
	global, err := m.ListScope(ctx, ScopeGlobal, "", "")
	if err != nil {
		return nil, err
	}
	var project []Command
	if projectID != "" && projectPath != "" {
		project, err = m.ListScope(ctx, ScopeProject, projectID, projectPath)
		if err != nil {
			return nil, err
		}
	}

	effective := make(map[string]Command)
	for _, item := range global {
		effective[item.Name] = item
	}
	for _, item := range project {
		effective[item.Name] = item
	}
	for _, item := range Builtins() {
		effective[item.Name] = item
	}
	out := make([]Command, 0, len(effective))
	for _, item := range effective {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return scopeRank(out[i].Scope) > scopeRank(out[j].Scope)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (m *Manager) Create(ctx context.Context, input CreateInput, projectPath string) (Command, error) {
	name, err := normalizeName(input.Name)
	if err != nil {
		return Command{}, err
	}
	if isBuiltin(name) {
		return Command{}, ErrReservedName
	}
	root, projectID, err := m.rootFor(input.Scope, input.ProjectID, projectPath)
	if err != nil {
		return Command{}, err
	}
	if err := ensureNameAvailable(ctx, root, input.Scope, projectID, name, ""); err != nil {
		return Command{}, err
	}
	path, err := createPath(root, name)
	if err != nil {
		return Command{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return Command{}, ErrDuplicateName
	} else if !errors.Is(err, os.ErrNotExist) {
		return Command{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Command{}, err
	}
	body := strings.TrimSpace(input.Body)
	if body == "" {
		body = "Define what the AI should do when this command is triggered."
	}
	data := render(name, "", body)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		return Command{}, err
	}
	return commandFromFile(path, root, input.Scope, projectID)
}

func (m *Manager) Update(ctx context.Context, targetRef string, input UpdateInput, projectPath string) (Command, error) {
	root, projectID, err := m.rootFor(input.Scope, input.ProjectID, projectPath)
	if err != nil {
		return Command{}, err
	}
	item, err := findByRef(ctx, root, input.Scope, projectID, targetRef)
	if err != nil {
		return Command{}, err
	}
	name, err := normalizeName(input.Name)
	if err != nil {
		return Command{}, err
	}
	if isBuiltin(name) {
		return Command{}, ErrReservedName
	}
	if err := ensureNameAvailable(ctx, root, input.Scope, projectID, name, targetRef); err != nil {
		return Command{}, err
	}
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return Command{}, ErrInvalidContent
	}
	path, err := createPath(root, name)
	if err != nil {
		return Command{}, err
	}
	if path != item.Path {
		if _, err := os.Stat(path); err == nil {
			return Command{}, ErrDuplicateName
		} else if !errors.Is(err, os.ErrNotExist) {
			return Command{}, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return Command{}, err
		}
		if err := os.Rename(item.Path, path); err != nil {
			return Command{}, err
		}
		item.Path = path
	}
	if err := os.WriteFile(item.Path, []byte(render(name, strings.TrimSpace(input.Description), body)), 0o600); err != nil {
		return Command{}, err
	}
	return commandFromFile(item.Path, root, input.Scope, projectID)
}

func (m *Manager) Delete(ctx context.Context, scope Scope, projectID, projectPath, ref string) error {
	root, resolvedProjectID, err := m.rootFor(scope, projectID, projectPath)
	if err != nil {
		return err
	}
	item, err := findByRef(ctx, root, scope, resolvedProjectID, ref)
	if err != nil {
		return err
	}
	return os.Remove(item.Path)
}

// Resolve finds one effective custom or builtin command by name.
func (m *Manager) Resolve(ctx context.Context, projectID, projectPath, name string) (Command, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "/")
	items, err := m.List(ctx, projectID, projectPath)
	if err != nil {
		return Command{}, err
	}
	for _, item := range items {
		if item.Name == name {
			return item, nil
		}
	}
	return Command{}, ErrNotFound
}

// Expand substitutes the optional command argument. The body can use
// $ARGUMENTS, $@, or {{args}}; if none is present a nonempty argument is
// appended as a separate user-provided detail block.
func Expand(body, args string) string {
	args = strings.TrimSpace(args)
	replaced := body
	for _, marker := range []string{"$ARGUMENTS", "$@", "{{args}}"} {
		replaced = strings.ReplaceAll(replaced, marker, args)
	}
	if args != "" && replaced == body {
		replaced += "\n\nAdditional user arguments:\n" + args
	}
	return strings.TrimSpace(replaced)
}

func (m *Manager) rootFor(scope Scope, projectID, projectPath string) (string, string, error) {
	switch scope {
	case ScopeGlobal:
		return GlobalRoot(m.homeDir), "", nil
	case ScopeProject:
		if projectID == "" || projectPath == "" {
			return "", "", ErrInvalidScope
		}
		return ProjectRoot(projectPath), projectID, nil
	default:
		return "", "", ErrInvalidScope
	}
}

func scan(ctx context.Context, root string, scope Scope, projectID string) ([]Command, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	var out []Command
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		depth := len(strings.Split(filepath.Dir(rel), string(filepath.Separator)))
		if filepath.Dir(rel) == "." {
			depth = 0
		}
		if entry.IsDir() {
			if depth > maxCommandDirectoryDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > maxCommandDirectoryDepth || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		item, err := commandFromFile(path, root, scope, projectID)
		if err == nil {
			out = append(out, item)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func commandFromFile(path, root string, scope Scope, projectID string) (Command, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Command{}, err
	}
	if info.Size() > maxCommandFileBytes {
		return Command{}, ErrInvalidContent
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Command{}, err
	}
	meta, body, err := parseDocument(string(data))
	if err != nil {
		return Command{}, err
	}
	name := meta.Name
	if name == "" {
		rel, _ := filepath.Rel(root, path)
		name = strings.TrimSuffix(filepath.ToSlash(rel), filepath.Ext(rel))
		name = strings.ReplaceAll(name, "/", ":")
	}
	name, err = normalizeName(name)
	if err != nil || isBuiltin(name) {
		return Command{}, ErrInvalidContent
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Command{}, ErrInvalidContent
	}
	return Command{
		Ref:         ref(scope, projectID, name),
		Name:        name,
		Description: strings.TrimSpace(meta.Description),
		Scope:       scope,
		ProjectID:   projectID,
		Kind:        KindPrompt,
		Path:        path,
		Body:        body,
		UpdatedAt:   info.ModTime(),
	}, nil
}

func findByRef(ctx context.Context, root string, scope Scope, projectID, target string) (Command, error) {
	items, err := scan(ctx, root, scope, projectID)
	if err != nil {
		return Command{}, err
	}
	for _, item := range items {
		if item.Ref == target {
			return item, nil
		}
	}
	return Command{}, ErrNotFound
}

func ensureNameAvailable(
	ctx context.Context,
	root string,
	scope Scope,
	projectID, name, exceptRef string,
) error {
	items, err := scan(ctx, root, scope, projectID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Name == name && item.Ref != exceptRef {
			return ErrDuplicateName
		}
	}
	return nil
}

func parseDocument(content string) (frontmatter, string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return frontmatter{}, content, nil
	}
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return frontmatter{}, "", ErrInvalidContent
	}
	end += 4
	var meta frontmatter
	if err := yaml.Unmarshal([]byte(content[4:end]), &meta); err != nil {
		return frontmatter{}, "", err
	}
	return meta, content[end+5:], nil
}

func render(name, description, body string) string {
	var builder strings.Builder
	builder.WriteString("---\nname: ")
	builder.WriteString(name)
	builder.WriteString("\n")
	if description != "" {
		builder.WriteString("description: ")
		builder.WriteString(fmt.Sprintf("%q", description))
		builder.WriteString("\n")
	}
	builder.WriteString("---\n\n")
	builder.WriteString(strings.TrimSpace(body))
	builder.WriteString("\n")
	return builder.String()
}

func createPath(root, name string) (string, error) {
	segments := strings.FieldsFunc(name, func(r rune) bool { return r == ':' || r == '/' })
	if len(segments) == 0 || len(segments) > maxCommandDirectoryDepth+1 {
		return "", ErrInvalidName
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", ErrInvalidName
		}
	}
	return filepath.Join(append([]string{root}, append(segments[:len(segments)-1], segments[len(segments)-1]+".md")...)...), nil
}

func normalizeName(value string) (string, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	value = strings.ReplaceAll(value, "/", ":")
	if !commandNamePattern.MatchString(value) {
		return "", ErrInvalidName
	}
	return value, nil
}

func ref(scope Scope, projectID, name string) string {
	if scope == ScopeProject {
		return "project:" + projectID + ":" + name
	}
	return string(scope) + ":" + name
}

func isBuiltin(name string) bool {
	for _, item := range Builtins() {
		if item.Name == name {
			return true
		}
	}
	return false
}

func scopeRank(scope Scope) int {
	switch scope {
	case ScopeBuiltin:
		return 3
	case ScopeProject:
		return 2
	case ScopeGlobal:
		return 1
	default:
		return 0
	}
}
