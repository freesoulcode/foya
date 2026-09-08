package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func normalizeSource(raw string) (kind, location, label string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", ErrInvalidSource
	}
	if !strings.Contains(raw, "://") {
		path, pathErr := filepath.Abs(raw)
		if pathErr == nil {
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				return "local", path, path, nil
			}
		}
	}
	if githubRepoPattern.MatchString(raw) {
		return "git", "https://github.com/" + raw + ".git", raw, nil
	}
	if gitSSHPattern.MatchString(raw) {
		return "git", raw, raw, nil
	}
	if parsed, parseErr := url.Parse(raw); parseErr == nil && parsed.Scheme != "" {
		if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", "", fmt.Errorf("%w: only credential-free HTTPS Git URLs are allowed", ErrInvalidSource)
		}
		return "git", raw, raw, nil
	}
	return "", "", "", fmt.Errorf(
		"%w: expected a local directory, owner/repo, HTTPS Git URL, or Git SSH URL",
		ErrInvalidSource,
	)
}

func cloneRepository(ctx context.Context, source, destination string) error {
	cloneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(cloneCtx, "git", "clone", "--depth", "1", "--", source, destination)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("clone plugin: %s", message)
	}
	return nil
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are not accepted during installation", ErrInvalidPlugin)
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: package contains a non-regular file", ErrInvalidPlugin)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(source, destination string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()&0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func validateTree(root string) error {
	var files int
	var bytes int64
	return filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are not accepted during installation", ErrInvalidPlugin)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		bytes += info.Size()
		if files > maxPackageFiles || bytes > maxPackageBytes {
			return fmt.Errorf("%w: package exceeds installation limits", ErrInvalidPlugin)
		}
		return nil
	})
}

func replaceDirectory(source, target string) error {
	backup := target + ".backup"
	_ = os.RemoveAll(backup)
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		_ = os.Rename(backup, target)
		return err
	}
	return os.RemoveAll(backup)
}

func readFileLimit(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("file is not regular or exceeds size limit")
	}
	return io.ReadAll(io.LimitReader(file, limit+1))
}

func jsonObject(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func jsonString(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return false
	}
	var text string
	return json.Unmarshal([]byte(trimmed), &text) == nil
}

func isJSONNull(value json.RawMessage) bool {
	return strings.TrimSpace(string(value)) == "null"
}

func resolvedInside(root, path string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return pathInside(resolvedRoot, resolvedPath)
}

func pathInside(root, child string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbs, childAbs)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func validPluginName(value string) bool {
	return len(value) >= 1 && len(value) <= 64 &&
		!strings.Contains(value, "--") && !strings.Contains(value, "..") &&
		pluginNamePattern.MatchString(value)
}

func hasErrors(items []Diagnostic) bool {
	for _, item := range items {
		if item.Severity == "error" {
			return true
		}
	}
	return false
}

func errorDiagnostics(items []Diagnostic) string {
	var messages []string
	for _, item := range items {
		if item.Severity == "error" {
			messages = append(messages, item.Message)
		}
	}
	return strings.Join(messages, "; ")
}

func diag(component, path, code, severity, message string) Diagnostic {
	return Diagnostic{Component: component, Path: path, Code: code, Severity: severity, Message: message}
}

func cloneBoolMap(input map[string]bool) map[string]bool {
	out := make(map[string]bool, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
