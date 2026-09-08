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
	"os"
	"path/filepath"
	"regexp"
	"strings"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

const (
	MaxImageBytes  int64 = 20 << 20
	MaxTurnBytes   int64 = 50 << 20
	MaxAttachments       = 8
	MaxImagePixels int64 = 40_000_000
	MaxImageEdge         = 2048
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

var (
	ErrInvalidID       = errors.New("invalid artifact identifier")
	ErrUnsupportedType = errors.New("unsupported image type")
	ErrImageTooLarge   = errors.New("image exceeds size limit")
	ErrTooManyPixels   = errors.New("image exceeds pixel limit")
	ErrCommitted       = errors.New("artifact is already attached to a message")
)

type metadata struct {
	conversation.AttachmentRef
	Committed bool `json:"committed,omitempty"`
}

type Store interface {
	PutImage(ctx context.Context, sessionID, name string, src io.Reader) (conversation.AttachmentRef, error)
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
		Name:      filepath.Base(strings.TrimSpace(name)),
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
	dir := filepath.Join(s.root, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return conversation.AttachmentRef{}, err
	}
	if err := writeAtomic(filepath.Join(dir, id+".bin"), normalized, 0o600); err != nil {
		return conversation.AttachmentRef{}, err
	}
	meta, err := json.Marshal(metadata{AttachmentRef: ref})
	if err != nil {
		_ = os.Remove(filepath.Join(dir, id+".bin"))
		return conversation.AttachmentRef{}, err
	}
	if err := writeAtomic(filepath.Join(dir, id+".json"), meta, 0o600); err != nil {
		_ = os.Remove(filepath.Join(dir, id+".bin"))
		return conversation.AttachmentRef{}, err
	}
	return ref, nil
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

var (
	_ = gif.GIF{}
	_ = webp.Decode
)
