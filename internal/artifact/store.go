// Package artifact stores validated binary artifacts outside the event log.
package artifact

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

const (
	MaxImageBytes  int64 = 20 << 20
	MaxFileBytes   int64 = 20 << 20
	MaxTurnBytes   int64 = 50 << 20
	MaxAttachments       = 8
	MaxImagePixels int64 = 40_000_000
	MaxImageEdge         = 2048
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

var (
	ErrInvalidID        = errors.New("invalid artifact identifier")
	ErrUnsupportedType  = errors.New("unsupported image type")
	ErrImageTooLarge    = errors.New("image exceeds size limit")
	ErrArtifactTooLarge = errors.New("artifact exceeds size limit")
	ErrTooManyPixels    = errors.New("image exceeds pixel limit")
	ErrCommitted        = errors.New("artifact is already attached to a message")
)

type metadata struct {
	conversation.AttachmentRef
	Committed bool `json:"committed,omitempty"`
}

type Store interface {
	WorkspaceDir(ctx context.Context, sessionID string) (string, error)
	CopyWorkspace(ctx context.Context, sourceID, targetID string) error
	PutImage(ctx context.Context, sessionID, name string, src io.Reader) (conversation.AttachmentRef, error)
	PutFile(ctx context.Context, sessionID, name, mediaType string, src io.Reader) (conversation.AttachmentRef, error)
	Read(ctx context.Context, sessionID, artifactID string) ([]byte, conversation.AttachmentRef, error)
	Commit(ctx context.Context, sessionID string, artifactIDs []string) error
	Delete(ctx context.Context, sessionID, artifactID string) error
	DeleteSession(ctx context.Context, sessionID string) error
}

type FileStore struct {
	root string
}

func NewFileStore(dataDir string) (*FileStore, error) {
	root := filepath.Join(dataDir, "artifacts")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &FileStore{root: root}, nil
}

func (s *FileStore) WorkspaceDir(ctx context.Context, sessionID string) (string, error) {
	if err := validateID(sessionID); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := filepath.Join(s.root, sessionID, "workspace")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *FileStore) CopyWorkspace(ctx context.Context, sourceID, targetID string) error {
	if err := validateID(sourceID); err != nil {
		return err
	}
	if err := validateID(targetID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	source := filepath.Join(s.root, sourceID, "workspace")
	target := filepath.Join(s.root, targetID, "workspace")
	info, err := os.Stat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("workspace path is not a directory")
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(src string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, src)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return copyWorkspaceSymlink(source, dst, src)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return copyFile(dst, src, info.Mode().Perm())
	})
}

func copyWorkspaceSymlink(sourceRoot, dst, src string) error {
	linkTarget, err := os.Readlink(src)
	if err != nil {
		return nil
	}
	if filepath.IsAbs(linkTarget) {
		return nil
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(src), linkTarget))
	relative, err := filepath.Rel(sourceRoot, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Symlink(linkTarget, dst); err != nil {
		return nil
	}
	return nil
}

func (s *FileStore) PutImage(
	ctx context.Context,
	sessionID, name string,
	src io.Reader,
) (conversation.AttachmentRef, error) {
	if err := validateID(sessionID); err != nil {
		return conversation.AttachmentRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return conversation.AttachmentRef{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(src, MaxImageBytes+1))
	if err != nil {
		return conversation.AttachmentRef{}, err
	}
	if int64(len(raw)) > MaxImageBytes {
		return conversation.AttachmentRef{}, ErrImageTooLarge
	}
	normalized, mediaType, width, height, err := normalizeImage(raw)
	if err != nil {
		return conversation.AttachmentRef{}, err
	}
	id, err := randomID()
	if err != nil {
		return conversation.AttachmentRef{}, err
	}
	sum := sha256.Sum256(normalized)
	ref := conversation.AttachmentRef{
		ID:        id,
		Name:      safeArtifactName(name),
		Kind:      "image",
		MediaType: mediaType,
		Bytes:     int64(len(normalized)),
		Width:     width,
		Height:    height,
		SHA256:    hex.EncodeToString(sum[:]),
	}
	if ref.Name == "." || ref.Name == "" {
		ref.Name = id
	}
	if err := s.putRaw(ctx, sessionID, id, normalized, ref); err != nil {
		return conversation.AttachmentRef{}, err
	}
	return ref, nil
}

func (s *FileStore) PutFile(
	ctx context.Context,
	sessionID, name, mediaType string,
	src io.Reader,
) (conversation.AttachmentRef, error) {
	if err := validateID(sessionID); err != nil {
		return conversation.AttachmentRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return conversation.AttachmentRef{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(src, MaxFileBytes+1))
	if err != nil {
		return conversation.AttachmentRef{}, err
	}
	if int64(len(raw)) > MaxFileBytes {
		return conversation.AttachmentRef{}, ErrArtifactTooLarge
	}
	id, err := randomID()
	if err != nil {
		return conversation.AttachmentRef{}, err
	}
	safeName := safeArtifactName(name)
	if safeName == "" || safeName == "." {
		safeName = id
	}
	sum := sha256.Sum256(raw)
	ref := conversation.AttachmentRef{
		ID:        id,
		Name:      safeName,
		Kind:      "file",
		MediaType: fileMediaType(safeName, mediaType, raw),
		Bytes:     int64(len(raw)),
		SHA256:    hex.EncodeToString(sum[:]),
	}
	if err := s.putRaw(ctx, sessionID, id, raw, ref); err != nil {
		return conversation.AttachmentRef{}, err
	}
	return ref, nil
}

func (s *FileStore) putRaw(
	ctx context.Context,
	sessionID, id string,
	data []byte,
	ref conversation.AttachmentRef,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(s.root, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, id+".bin"), data, 0o600); err != nil {
		return err
	}
	meta, err := json.Marshal(metadata{AttachmentRef: ref})
	if err != nil {
		_ = os.Remove(filepath.Join(dir, id+".bin"))
		return err
	}
	if err := writeAtomic(filepath.Join(dir, id+".json"), meta, 0o600); err != nil {
		_ = os.Remove(filepath.Join(dir, id+".bin"))
		return err
	}
	return nil
}

func (s *FileStore) Read(
	ctx context.Context,
	sessionID, artifactID string,
) ([]byte, conversation.AttachmentRef, error) {
	if err := validateID(sessionID); err != nil {
		return nil, conversation.AttachmentRef{}, err
	}
	if err := validateID(artifactID); err != nil {
		return nil, conversation.AttachmentRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, conversation.AttachmentRef{}, err
	}
	dir := filepath.Join(s.root, sessionID)
	meta, err := os.ReadFile(filepath.Join(dir, artifactID+".json"))
	if err != nil {
		return nil, conversation.AttachmentRef{}, err
	}
	var record metadata
	if err := json.Unmarshal(meta, &record); err != nil {
		return nil, conversation.AttachmentRef{}, err
	}
	ref := record.AttachmentRef
	if ref.ID != artifactID {
		return nil, conversation.AttachmentRef{}, ErrInvalidID
	}
	data, err := os.ReadFile(filepath.Join(dir, artifactID+".bin"))
	if err != nil {
		return nil, conversation.AttachmentRef{}, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != ref.SHA256 {
		return nil, conversation.AttachmentRef{}, errors.New("artifact checksum mismatch")
	}
	return data, ref, nil
}

func (s *FileStore) Commit(ctx context.Context, sessionID string, artifactIDs []string) error {
	if err := validateID(sessionID); err != nil {
		return err
	}
	dir := filepath.Join(s.root, sessionID)
	for _, artifactID := range artifactIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateID(artifactID); err != nil {
			return err
		}
		path := filepath.Join(dir, artifactID+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var record metadata
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if record.ID != artifactID {
			return ErrInvalidID
		}
		if record.Committed {
			continue
		}
		record.Committed = true
		updated, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if err := writeAtomic(path, updated, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileStore) Delete(_ context.Context, sessionID, artifactID string) error {
	if err := validateID(sessionID); err != nil {
		return err
	}
	if err := validateID(artifactID); err != nil {
		return err
	}
	dir := filepath.Join(s.root, sessionID)
	metaPath := filepath.Join(dir, artifactID+".json")
	if data, err := os.ReadFile(metaPath); err == nil {
		var record metadata
		if json.Unmarshal(data, &record) == nil && record.Committed {
			return ErrCommitted
		}
	}
	var result error
	for _, suffix := range []string{".bin", ".json"} {
		if err := os.Remove(filepath.Join(dir, artifactID+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (s *FileStore) DeleteSession(_ context.Context, sessionID string) error {
	if err := validateID(sessionID); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.root, sessionID))
}

func normalizeImage(raw []byte) ([]byte, string, int, int, error) {
	mediaType := sniffImageType(raw)
	if mediaType == "" {
		return nil, "", 0, 0, ErrUnsupportedType
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", 0, 0, ErrUnsupportedType
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return nil, "", 0, 0, ErrTooManyPixels
	}
	if cfg.Width <= MaxImageEdge && cfg.Height <= MaxImageEdge {
		return raw, mediaType, cfg.Width, cfg.Height, nil
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", 0, 0, ErrUnsupportedType
	}
	width, height := scaledDimensions(cfg.Width, cfg.Height, MaxImageEdge)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if mediaType == "image/jpeg" {
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 90})
	} else {
		mediaType = "image/png"
		err = png.Encode(&out, dst)
	}
	if err != nil {
		return nil, "", 0, 0, err
	}
	if int64(out.Len()) > MaxImageBytes {
		return nil, "", 0, 0, ErrImageTooLarge
	}
	return out.Bytes(), mediaType, width, height, nil
}

func sniffImageType(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return "image/png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

func scaledDimensions(width, height, maxEdge int) (int, int) {
	if width >= height {
		return maxEdge, max(1, height*maxEdge/width)
	}
	return max(1, width*maxEdge/height), maxEdge
}

func validateID(value string) error {
	if value == "" || value == "." || value == ".." ||
		filepath.Base(value) != value || !safeID.MatchString(value) {
		return ErrInvalidID
	}
	return nil
}

func safeArtifactName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return ""
	}
	return path.Base(name)
}

func fileMediaType(name, provided string, data []byte) string {
	if mediaType := normalizedMediaType(provided); mediaType != "" {
		return mediaType
	}
	if ext := filepath.Ext(name); ext != "" {
		if mediaType := normalizedMediaType(mime.TypeByExtension(ext)); mediaType != "" {
			return mediaType
		}
	}
	if mediaType := normalizedMediaType(http.DetectContentType(data)); mediaType != "" {
		return mediaType
	}
	return "application/octet-stream"
}

func normalizedMediaType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || mediaType == "" {
		return ""
	}
	if charset := params["charset"]; charset != "" {
		return mediaType + "; charset=" + charset
	}
	return mediaType
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func copyFile(dst, src string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}

var (
	_ = gif.GIF{}
	_ = webp.Decode
)
