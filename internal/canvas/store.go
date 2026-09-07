// Package canvas owns persistent visual workspaces and their original media assets.
package canvas

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
)

const MaxAssetBytes int64 = 500 << 20

var (
	ErrNotFound         = errors.New("canvas not found")
	ErrAssetNotFound    = errors.New("canvas asset not found")
	ErrRevisionConflict = errors.New("canvas revision conflict")
	ErrUnsupportedMedia = errors.New("unsupported canvas media type")
	ErrAssetTooLarge    = errors.New("canvas asset exceeds size limit")
)

type Node struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Title      string          `json:"title,omitempty"`
	AssetID    string          `json:"asset_id,omitempty"`
	Text       string          `json:"text,omitempty"`
	Prompt     string          `json:"prompt,omitempty"`
	ParentID   string          `json:"parent_id,omitempty"`
	Status     string          `json:"status,omitempty"`
	Error      string          `json:"error,omitempty"`
	Generation *GenerationSpec `json:"generation,omitempty"`
	X          float64         `json:"x"`
	Y          float64         `json:"y"`
	Width      float64         `json:"width"`
	Height     float64         `json:"height"`
	Rotation   float64         `json:"rotation,omitempty"`
	ZIndex     int             `json:"z_index"`
}

type GenerationSpec struct {
	ConnectionID string `json:"connection_id,omitempty"`
	Mode         string `json:"mode"`
	Model        string `json:"model,omitempty"`
	AspectRatio  string `json:"aspect_ratio,omitempty"`
	Quality      string `json:"quality,omitempty"`
	Count        int    `json:"count,omitempty"`
	Duration     int    `json:"duration,omitempty"`
}

type Edge struct {
	ID         string `json:"id"`
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Kind       string `json:"kind,omitempty"`
}

type Viewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}

type Asset struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	MediaType string    `json:"media_type"`
	Bytes     int64     `json:"bytes"`
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type Document struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	SessionID  string    `json:"session_id,omitempty"`
	ProjectID  string    `json:"project_id,omitempty"`
	Revision   uint64    `json:"revision"`
	Nodes      []Node    `json:"nodes"`
	Edges      []Edge    `json:"edges"`
	Assets     []Asset   `json:"assets"`
	Viewport   Viewport  `json:"viewport"`
	Background string    `json:"background"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CreateInput struct {
	Title     string
	SessionID string
	ProjectID string
}

type UpdateInput struct {
	ExpectedRevision uint64
	Title            *string
	Nodes            *[]Node
	Edges            *[]Edge
	Viewport         *Viewport
	Background       *string
}

type GenerateImageInput struct {
	ExpectedRevision uint64
	ConfigNodeID     string
	OutputNodeID     string
	ConnectionID     string
}

type GenerateVideoInput struct {
	ExpectedRevision uint64
	ConfigNodeID     string
	OutputNodeID     string
	ConnectionID     string
}

type Store struct {
	mu        sync.RWMutex
	root      string
	path      string
	documents map[string]Document
}

func NewStore(dataDir string) (*Store, error) {
	root := filepath.Join(dataDir, "canvas-assets")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	s := &Store{root: root, path: filepath.Join(dataDir, "canvases.json"), documents: make(map[string]Document)}
	data, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(data) > 0 {
		var items []Document
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		recovered := false
		for _, item := range items {
			if item.ID != "" {
				for index := range item.Nodes {
					if item.Nodes[index].Status == "running" {
						item.Nodes[index].Status = "error"
						item.Nodes[index].Error = "Generation was interrupted by an application restart"
						recovered = true
					}
				}
				s.documents[item.ID] = cloneDocument(item)
			}
		}
		if recovered {
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *Store) Create(input CreateInput) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := randomID()
	if err != nil {
		return Document{}, err
	}
	now := time.Now()
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = "Untitled canvas"
	}
	doc := Document{
		ID: id, Title: title, SessionID: input.SessionID, ProjectID: input.ProjectID,
		Revision: 1, Nodes: []Node{}, Edges: []Edge{}, Assets: []Asset{},
		Viewport: Viewport{X: 160, Y: 120, Zoom: 1}, Background: "dots",
		CreatedAt: now, UpdatedAt: now,
	}
	s.documents[id] = doc
	if err := s.saveLocked(); err != nil {
		delete(s.documents, id)
		return Document{}, err
	}
	return cloneDocument(doc), nil
}

func (s *Store) List(sessionID string) []Document {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Document, 0, len(s.documents))
	for _, item := range s.documents {
		if sessionID == "" || item.SessionID == sessionID {
			items = append(items, cloneDocument(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items
}

func (s *Store) Get(id string) (Document, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, ok := s.documents[id]
	return cloneDocument(doc), ok
}

func (s *Store) Update(id string, input UpdateInput) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.documents[id]
	if !ok {
		return Document{}, ErrNotFound
	}
	if input.ExpectedRevision != doc.Revision {
		return cloneDocument(doc), ErrRevisionConflict
	}
	previous := cloneDocument(doc)
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" {
			return Document{}, errors.New("canvas title is required")
		}
		doc.Title = title
	}
	if input.Nodes != nil {
		if err := validateNodes(*input.Nodes, doc.Assets); err != nil {
			return Document{}, err
		}
		doc.Nodes = append([]Node(nil), (*input.Nodes)...)
	}
	if input.Edges != nil {
		if err := validateEdges(*input.Edges, doc.Nodes); err != nil {
			return Document{}, err
		}
		doc.Edges = append([]Edge(nil), (*input.Edges)...)
	}
	if input.Viewport != nil {
		if err := validateViewport(*input.Viewport); err != nil {
			return Document{}, err
		}
		doc.Viewport = *input.Viewport
	}
	if input.Background != nil {
		switch *input.Background {
		case "dots", "grid", "blank":
			doc.Background = *input.Background
		default:
			return Document{}, errors.New("unsupported canvas background")
		}
	}
	doc.Revision++
	doc.UpdatedAt = time.Now()
	s.documents[id] = doc
	if err := s.saveLocked(); err != nil {
		s.documents[id] = previous
		return Document{}, err
	}
	return cloneDocument(doc), nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.documents[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.documents, id)
	if err := s.saveLocked(); err != nil {
		s.documents[id] = doc
		return err
	}
	return os.RemoveAll(filepath.Join(s.root, id))
}

func (s *Store) PutAsset(ctx context.Context, canvasID, name, declaredType string, src io.Reader) (Asset, error) {
	if err := ctx.Err(); err != nil {
		return Asset{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(src, MaxAssetBytes+1))
	if err != nil {
		return Asset{}, err
	}
	if int64(len(raw)) > MaxAssetBytes {
		return Asset{}, ErrAssetTooLarge
	}
	mediaType := strings.TrimSpace(strings.Split(declaredType, ";")[0])
	detected := strings.TrimSpace(strings.Split(http.DetectContentType(raw), ";")[0])
	if detected != "application/octet-stream" {
		mediaType = detected
	}
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	}
	kind := ""
	if strings.HasPrefix(mediaType, "image/") {
		kind = "image"
	} else if strings.HasPrefix(mediaType, "video/") {
		kind = "video"
	}
	if kind == "" {
		return Asset{}, ErrUnsupportedMedia
	}
	id, err := randomID()
	if err != nil {
		return Asset{}, err
	}
	asset := Asset{ID: id, Name: filepath.Base(strings.TrimSpace(name)), Kind: kind, MediaType: mediaType, Bytes: int64(len(raw)), CreatedAt: time.Now()}
	if asset.Name == "" || asset.Name == "." {
		asset.Name = id
	}
	if kind == "image" {
		if cfg, _, decodeErr := image.DecodeConfig(bytes.NewReader(raw)); decodeErr == nil {
			asset.Width, asset.Height = cfg.Width, cfg.Height
		}
	}
	sum := sha256.Sum256(raw)
	asset.SHA256 = hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.documents[canvasID]
	if !ok {
		return Asset{}, ErrNotFound
	}
	dir := filepath.Join(s.root, canvasID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Asset{}, err
	}
	path := filepath.Join(dir, id+".bin")
	if err := writeAtomic(path, raw, 0o600); err != nil {
		return Asset{}, err
	}
	previous := cloneDocument(doc)
	doc.Assets = append(doc.Assets, asset)
	doc.Revision++
	doc.UpdatedAt = time.Now()
	s.documents[canvasID] = doc
	if err := s.saveLocked(); err != nil {
		s.documents[canvasID] = previous
		_ = os.Remove(path)
		return Asset{}, err
	}
	return asset, nil
}

func (s *Store) ReadAsset(ctx context.Context, canvasID, assetID string) ([]byte, Asset, error) {
	if err := ctx.Err(); err != nil {
		return nil, Asset{}, err
	}
	s.mu.RLock()
	doc, ok := s.documents[canvasID]
	if !ok {
		s.mu.RUnlock()
		return nil, Asset{}, ErrNotFound
	}
	var asset Asset
	for _, candidate := range doc.Assets {
		if candidate.ID == assetID {
			asset = candidate
			break
		}
	}
	s.mu.RUnlock()
	if asset.ID == "" {
		return nil, Asset{}, ErrAssetNotFound
	}
	data, err := os.ReadFile(filepath.Join(s.root, canvasID, assetID+".bin"))
	if err != nil {
		return nil, Asset{}, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != asset.SHA256 {
		return nil, Asset{}, errors.New("canvas asset checksum mismatch")
	}
	return data, asset, nil
}

func validateNodes(nodes []Node, assets []Asset) error {
	assetIDs := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		assetIDs[asset.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node.ID == "" {
			return errors.New("canvas node id is required")
		}
		if _, ok := seen[node.ID]; ok {
			return fmt.Errorf("duplicate canvas node id %q", node.ID)
		}
		seen[node.ID] = struct{}{}
		if node.Type != "image" && node.Type != "video" && node.Type != "text" && node.Type != "generation" {
			return fmt.Errorf("unsupported canvas node type %q", node.Type)
		}
		if node.AssetID != "" {
			if _, ok := assetIDs[node.AssetID]; !ok {
				return fmt.Errorf("canvas asset %q does not exist", node.AssetID)
			}
		}
		for _, value := range []float64{node.X, node.Y, node.Width, node.Height, node.Rotation} {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return errors.New("canvas node geometry must be finite")
			}
		}
		if node.Width < 24 || node.Height < 24 || node.Width > 100_000 || node.Height > 100_000 {
			return errors.New("canvas node dimensions are out of range")
		}
		if node.Generation != nil {
			if node.Type != "generation" {
				return errors.New("generation settings require a generation node")
			}
			if node.Generation.Mode != "image" && node.Generation.Mode != "video" {
				return errors.New("unsupported generation mode")
			}
			if node.Generation.Count < 0 || node.Generation.Count > 16 {
				return errors.New("generation count is out of range")
			}
			if node.Generation.Duration < 0 || node.Generation.Duration > 15 {
				return errors.New("video generation duration is out of range")
			}
		}
	}
	return nil
}

func validateEdges(edges []Edge, nodes []Node) error {
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		nodeIDs[node.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if edge.ID == "" || edge.FromNodeID == "" || edge.ToNodeID == "" {
			return errors.New("canvas edge id and endpoints are required")
		}
		if edge.FromNodeID == edge.ToNodeID {
			return errors.New("canvas edge cannot connect a node to itself")
		}
		if _, ok := seen[edge.ID]; ok {
			return fmt.Errorf("duplicate canvas edge id %q", edge.ID)
		}
		seen[edge.ID] = struct{}{}
		if _, ok := nodeIDs[edge.FromNodeID]; !ok {
			return fmt.Errorf("canvas edge source %q does not exist", edge.FromNodeID)
		}
		if _, ok := nodeIDs[edge.ToNodeID]; !ok {
			return fmt.Errorf("canvas edge target %q does not exist", edge.ToNodeID)
		}
	}
	return nil
}

func validateViewport(viewport Viewport) error {
	for _, value := range []float64{viewport.X, viewport.Y, viewport.Zoom} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("canvas viewport must be finite")
		}
	}
	if viewport.Zoom < 0.1 || viewport.Zoom > 8 {
		return errors.New("canvas viewport zoom is out of range")
	}
	return nil
}

func cloneDocument(doc Document) Document {
	if doc.Nodes == nil {
		doc.Nodes = []Node{}
	} else {
		doc.Nodes = append([]Node{}, doc.Nodes...)
	}
	if doc.Edges == nil {
		doc.Edges = []Edge{}
	} else {
		doc.Edges = append([]Edge{}, doc.Edges...)
	}
	if doc.Assets == nil {
		doc.Assets = []Asset{}
	} else {
		doc.Assets = append([]Asset{}, doc.Assets...)
	}
	if doc.Viewport.Zoom == 0 {
		doc.Viewport = Viewport{X: 160, Y: 120, Zoom: 1}
	}
	if doc.Background == "" {
		doc.Background = "dots"
	}
	return doc
}

func (s *Store) saveLocked() error {
	items := make([]Document, 0, len(s.documents))
	for _, item := range s.documents {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.path, data, 0o600)
}

func randomID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".canvas-*")
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
