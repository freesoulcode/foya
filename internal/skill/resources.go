package skill

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func collectResources(root, mainPath string) []Resource {
	var out []Resource
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if len(out) >= maxSkillResources {
			return filepath.SkipAll
		}
		if samePath(path, mainPath) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, Resource{
			Path:      filepath.ToSlash(rel),
			Size:      info.Size(),
			MediaType: mediaTypeForPath(path),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && aa == bb
}

func mediaTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".txt", ".log":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".sh", ".bash", ".zsh":
		return "text/x-shellscript"
	case ".py":
		return "text/x-python"
	case ".js", ".jsx":
		return "text/javascript"
	case ".ts", ".tsx":
		return "text/typescript"
	case ".go":
		return "text/x-go"
	case ".rs":
		return "text/rust"
	case ".toml":
		return "application/toml"
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	default:
		return "application/octet-stream"
	}
}

func readableResource(path string) bool {
	switch mediaTypeForPath(path) {
	case "text/markdown", "text/plain", "application/json", "application/yaml",
		"text/x-shellscript", "text/x-python", "text/javascript",
		"text/typescript", "text/x-go", "text/rust", "application/toml",
		"text/html", "text/css":
		return true
	default:
		return false
	}
}

func cleanResourcePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	if path == "" {
		return "", errors.New("resource path is required")
	}
	if strings.HasPrefix(path, "/") {
		return "", errors.New("resource path must be relative")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", errors.New("resource path must stay inside the skill package")
	}
	return clean, nil
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
	rel, err := filepath.Rel(rootAbs, childAbs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}
