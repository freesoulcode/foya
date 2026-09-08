package contextdata

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

func validate(scope Scope, projectID, content string, maxRunes int) (string, error) {
	if scope != ScopeGlobal && scope != ScopeProject {
		return "", ErrInvalidScope
	}
	if scope == ScopeProject && strings.TrimSpace(projectID) == "" {
		return "", fmt.Errorf("%w: project_id is required", ErrInvalidScope)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", ErrEmptyContent
	}
	if utf8.RuneCountInString(content) > maxRunes {
		return "", fmt.Errorf("%w: maximum is %d characters", ErrTooLarge, maxRunes)
	}
	return content, nil
}

func normalizedProjectID(scope Scope, projectID string) string {
	if scope == ScopeGlobal {
		return ""
	}
	return strings.TrimSpace(projectID)
}

func matches(itemScope Scope, itemProjectID string, scope Scope, projectID string) bool {
	if scope != "" {
		return itemScope == scope && (scope != ScopeProject || itemProjectID == projectID)
	}
	return itemScope == ScopeGlobal || (projectID != "" && itemProjectID == projectID)
}

func newID() string {
	value := make([]byte, 12)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func stableID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
