package kernel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	model "github.com/freesoulcode/foya/internal/model"
)

// Connections returns the configured Connection catalog in its user-defined order.
func (b *Service) Connections() []config.Connection {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]config.Connection, 0, len(b.connections))
	for _, connection := range b.connections {
		out = append(out, connection)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (b *Service) Connection(id string) (config.Connection, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	connection, ok := b.connections[id]
	return connection, ok
}

func (b *Service) DefaultModels() (config.DefaultModels, error) {
	return config.LoadDefaultModels(b.dataDir)
}

func (b *Service) UpdateDefaultModels(defaults config.DefaultModels) (config.DefaultModels, error) {
	refs := []struct {
		name string
		kind string
		ref  config.ModelRef
	}{
		{"language", config.ConnectionTypeLanguage, defaults.Language},
		{"fast", config.ConnectionTypeLanguage, defaults.Fast},
		{"image", config.ConnectionTypeImage, defaults.Image},
		{"video", config.ConnectionTypeVideo, defaults.Video},
	}
	for _, item := range refs {
		if item.ref.ConnectionID == "" && item.ref.Model == "" {
			continue
		}
		if item.ref.ConnectionID == "" || item.ref.Model == "" {
			return config.DefaultModels{}, fmt.Errorf("default %s model requires connection_id and model", item.name)
		}
		connection, ok := b.Connection(item.ref.ConnectionID)
		if !ok {
			return config.DefaultModels{}, fmt.Errorf("%w: %q", ErrConnectionNotFound, item.ref.ConnectionID)
		}
		if connection.Type != item.kind {
			return config.DefaultModels{}, fmt.Errorf("default %s model requires a %s connection", item.name, item.kind)
		}
		if item.kind == config.ConnectionTypeVideo {
			if err := validateVideoProtocol(connection.Type, connection.VideoProtocol); err != nil {
				return config.DefaultModels{}, fmt.Errorf("default video model connection: %w", err)
			}
		}
		found := false
		for _, model := range connection.Models {
			if model == item.ref.Model {
				found = true
				break
			}
		}
		if !found {
			return config.DefaultModels{}, fmt.Errorf("model %q is not imported by connection %q", item.ref.Model, item.ref.ConnectionID)
		}
	}
	if err := config.SaveDefaultModels(b.dataDir, defaults); err != nil {
		return config.DefaultModels{}, err
	}
	return defaults, nil
}

// ListModels lists the catalog from one Connection's provider.
func (b *Service) ListModels(ctx context.Context, connectionID string, refresh bool) ([]model.ModelInfo, error) {
	connection, exists := b.Connection(connectionID)
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID)
	}
	if !refresh {
		models := make([]model.ModelInfo, 0, len(connection.Models))
		for _, id := range connection.Models {
			settings := connection.ModelSettings[id]
			models = append(models, model.ModelInfo{ID: id, ContextWindow: settings.ContextWindow})
		}
		return models, nil
	}
	if connection.Type == config.ConnectionTypeVideo &&
		connection.VideoProtocol == config.VideoProtocolMiniMaxH3 {
		return []model.ModelInfo{
			{ID: "MiniMax-H3"},
			{ID: "MiniMax-H3-Max"},
		}, nil
	}
	b.mu.RLock()
	prov, ok := b.providers[connectionID]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID)
	}
	lister, ok := prov.(model.ModelLister)
	if !ok {
		return nil, fmt.Errorf("connection %q does not support model discovery", connectionID)
	}
	models, err := lister.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return models, nil
}

// Usage returns token usage for the latest model request.
func (b *Service) Usage(ctx context.Context, sessionID string) (*model.Usage, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, conversation.ErrNotFound
	}
	events, err := b.log.Read(ctx, sessionID, 0)
	if err != nil {
		return nil, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != conversation.KindUsageUpdated {
			continue
		}
		switch usage := events[i].Payload.(type) {
		case model.Usage:
			return &usage, nil
		case *model.Usage:
			return usage, nil
		}
	}
	return nil, nil
}

// SetConnections replaces the Connection catalog and rebuilds provider clients.
// Existing Sessions keep their connection_id and therefore remain pinned to
// their original endpoint as long as that Connection remains configured.
func (b *Service) SetConnections(connections []config.Connection) {
	nextConnections := make(map[string]config.Connection, len(connections))
	nextProviders := make(map[string]model.Provider, len(connections))
	normalized := make([]config.Connection, 0, len(connections))
	sort.SliceStable(connections, func(i, j int) bool {
		if connections[i].LegacyDefault != connections[j].LegacyDefault {
			return connections[i].LegacyDefault
		}
		return connections[i].SortOrder < connections[j].SortOrder
	})
	for _, connection := range connections {
		if connection.ID == "" {
			continue
		}
		if connection.Kind == "" {
			connection.Kind = "openai"
		}
		if connection.AuthKind == "" {
			connection.AuthKind = "api_key"
		}
		if connection.Name == "" {
			connection.Name = connection.ID
		}
		normalized = append(normalized, connection)
	}
	for index := range normalized {
		connection := normalized[index]
		connection.SortOrder = index
		connection.LegacyDefault = false
		normalized[index] = connection
		nextConnections[connection.ID] = connection
		if b.buildProvider != nil {
			prov, _ := b.buildProvider(connection.Provider())
			nextProviders[connection.ID] = prov
		}
	}
	b.mu.Lock()
	b.connections = nextConnections
	b.providers = nextProviders
	b.firstConnectionID = ""
	for _, connection := range normalized {
		if connection.Type == config.ConnectionTypeLanguage {
			b.firstConnectionID = connection.ID
			break
		}
	}
	b.mu.Unlock()
	b.engine.SetProviderResolver(b.resolveSessionProvider)
	b.engine.SetTitleResolver(b.resolveDefaultFastModel)
	b.engine.SetImageCapabilityResolver(b.resolveSessionImageCapability)
	b.engine.SetContextWindowResolver(b.resolveSessionContextWindow)
	b.engine.SetModelTokenLimitsResolver(b.resolveSessionModelTokenLimits)
	b.engine.SetModelRouteResolver(b.resolveSessionModelRoute)
	for _, current := range b.sessions.List() {
		b.engine.InvalidateHistoryEstimate(current.ID)
	}
	if err := config.SaveConnections(b.dataDir, normalized); err != nil {
		fmt.Fprintf(os.Stderr, "persist connections config failed: %v\n", err)
	}
	b.reconcileDefaultModels()
}

func (b *Service) resolveDefaultFastModel() (model.Provider, string) {
	defaults, err := b.DefaultModels()
	if err != nil || defaults.Fast.ConnectionID == "" || defaults.Fast.Model == "" {
		return nil, ""
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.providers[defaults.Fast.ConnectionID], defaults.Fast.Model
}

func (b *Service) reconcileDefaultModels() {
	defaults, err := b.DefaultModels()
	if err != nil {
		return
	}
	changed := false
	items := []struct {
		ref  *config.ModelRef
		kind string
	}{
		{&defaults.Language, config.ConnectionTypeLanguage},
		{&defaults.Fast, config.ConnectionTypeLanguage},
		{&defaults.Image, config.ConnectionTypeImage},
		{&defaults.Video, config.ConnectionTypeVideo},
	}
	for _, item := range items {
		if item.ref.ConnectionID == "" {
			continue
		}
		connection, ok := b.Connection(item.ref.ConnectionID)
		valid := ok && connection.Type == item.kind
		if valid {
			valid = false
			for _, model := range connection.Models {
				if model == item.ref.Model {
					valid = true
					break
				}
			}
		}
		if !valid {
			*item.ref = config.ModelRef{}
			changed = true
		}
	}
	if changed {
		_ = config.SaveDefaultModels(b.dataDir, defaults)
	}
}

func (b *Service) CreateConnection(input config.Connection) (config.Connection, error) {
	if err := validateConnectionModelSettings(input.ModelSettings); err != nil {
		return config.Connection{}, err
	}
	if input.ID == "" {
		input.ID = newConnectionID()
	}
	if input.Name == "" {
		input.Name = input.ID
	}
	if input.Kind == "" {
		input.Kind = "openai"
	}
	if input.AuthKind == "" {
		input.AuthKind = "api_key"
	}
	if input.AuthKind != "api_key" {
		return config.Connection{}, fmt.Errorf("%w: %q", ErrUnsupportedAuth, input.AuthKind)
	}
	if err := validateConnectionType(input.Type); err != nil {
		return config.Connection{}, err
	}
	if err := validateVideoProtocol(input.Type, input.VideoProtocol); err != nil {
		return config.Connection{}, err
	}
	b.mu.Lock()
	if _, exists := b.connections[input.ID]; exists {
		b.mu.Unlock()
		return config.Connection{}, fmt.Errorf("connection %q already exists", input.ID)
	}
	b.mu.Unlock()
	connections := b.Connections()
	input.SortOrder = len(connections)
	connections = append(connections, input)
	b.SetConnections(connections)
	created, _ := b.Connection(input.ID)
	return created, nil
}

func (b *Service) UpdateConnection(id string, patch config.Connection) (config.Connection, error) {
	current, exists := b.Connection(id)
	if !exists {
		return config.Connection{}, fmt.Errorf("%w: %q", ErrConnectionNotFound, id)
	}
	if err := validateConnectionModelSettings(patch.ModelSettings); err != nil {
		return config.Connection{}, err
	}
	if patch.Type == "" {
		patch.Type = current.Type
	}
	if err := validateConnectionType(patch.Type); err != nil {
		return config.Connection{}, err
	}
	if err := validateVideoProtocol(patch.Type, patch.VideoProtocol); err != nil {
		return config.Connection{}, err
	}
	patch.ID = id
	if patch.Name == "" {
		patch.Name = current.Name
	}
	if patch.Kind == "" {
		patch.Kind = current.Kind
	}
	if patch.AuthKind == "" {
		patch.AuthKind = current.AuthKind
	}
	if patch.AuthKind != "api_key" {
		return config.Connection{}, fmt.Errorf("%w: %q", ErrUnsupportedAuth, patch.AuthKind)
	}
	if patch.BaseURL == "" {
		patch.BaseURL = current.BaseURL
	}
	if patch.APIKey == "" {
		patch.APIKey = current.APIKey
	}
	connections := b.Connections()
	targetIndex := patch.SortOrder
	if targetIndex < 0 {
		targetIndex = 0
	}
	if targetIndex >= len(connections) {
		targetIndex = len(connections) - 1
	}
	var updated config.Connection
	found := -1
	for index := range connections {
		if connections[index].ID == id {
			updated = patch
			found = index
			break
		}
	}
	if found >= 0 {
		connections = append(connections[:found], connections[found+1:]...)
		connections = append(connections, config.Connection{})
		copy(connections[targetIndex+1:], connections[targetIndex:])
		connections[targetIndex] = updated
	}
	b.SetConnections(connections)
	updated, _ = b.Connection(id)
	return updated, nil
}

func validateConnectionType(value string) error {
	switch value {
	case config.ConnectionTypeLanguage, config.ConnectionTypeImage, config.ConnectionTypeVideo:
		return nil
	default:
		return fmt.Errorf("unsupported connection type %q", value)
	}
}

func validateVideoProtocol(connectionType, protocol string) error {
	if connectionType != config.ConnectionTypeVideo {
		if protocol != "" {
			return errors.New("video protocol is only valid for video connections")
		}
		return nil
	}
	switch protocol {
	case config.VideoProtocolSeedance, config.VideoProtocolMiniMaxH3:
		return nil
	default:
		return fmt.Errorf("unsupported video protocol %q", protocol)
	}
}

func validateConnectionModelSettings(settings map[string]config.ModelSettings) error {
	for model, setting := range settings {
		if setting.ContextWindow < 0 || setting.MaxInputTokens < 0 || setting.MaxOutputTokens < 0 {
			return fmt.Errorf("model %q token limits must be zero or greater", model)
		}
		if setting.ContextWindow > 0 && setting.MaxInputTokens > setting.ContextWindow {
			return fmt.Errorf("model %q max_input_tokens cannot exceed context_window", model)
		}
		if setting.ContextWindow > 0 && setting.MaxOutputTokens > setting.ContextWindow {
			return fmt.Errorf("model %q max_output_tokens cannot exceed context_window", model)
		}
	}
	return nil
}

func (b *Service) DeleteConnection(id string) error {
	ctx := context.Background()
	var updated []*conversation.Session
	for _, sess := range b.sessions.List() {
		if sess.ConnectionID != id {
			continue
		}
		empty := ""
		next, err := b.sessions.Update(sess.ID, &empty, nil, nil, nil, nil)
		if err != nil {
			return fmt.Errorf("detach session %s connection %q: %w", sess.ID, id, err)
		}
		updated = append(updated, next)
	}
	connections := b.Connections()
	next := make([]config.Connection, 0, len(connections))
	found := false
	for _, connection := range connections {
		if connection.ID == id {
			found = true
			continue
		}
		next = append(next, connection)
	}
	if !found {
		return fmt.Errorf("%w: %q", ErrConnectionNotFound, id)
	}
	b.SetConnections(next)
	for _, s := range updated {
		b.broadcastSession(ctx, s)
	}
	return nil
}

func (b *Service) resolveSessionProvider(sessionID string) model.Provider {
	session, ok := b.sessions.Get(sessionID)
	if !ok || session.ConnectionID == "" {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.providers[session.ConnectionID]
}

func (b *Service) resolveSessionImageCapability(sessionID, model string) *bool {
	item, ok := b.sessions.Get(sessionID)
	if !ok || item.ConnectionID == "" {
		return nil
	}
	connection, ok := b.Connection(item.ConnectionID)
	if !ok {
		return nil
	}
	settings, configured := connection.ModelSettings[model]
	supported := !configured || settings.ImageInputSupported()
	return &supported
}

func (b *Service) resolveSessionContextWindow(sessionID, model string) *int64 {
	item, ok := b.sessions.Get(sessionID)
	if !ok || item.ConnectionID == "" {
		return nil
	}
	connection, ok := b.Connection(item.ConnectionID)
	if !ok {
		return nil
	}
	window := connection.ModelSettings[model].ContextWindow
	if window <= 0 {
		return nil
	}
	return &window
}

func (b *Service) resolveSessionModelTokenLimits(sessionID, model string) *agent.ModelTokenLimits {
	item, ok := b.sessions.Get(sessionID)
	if !ok || item.ConnectionID == "" {
		return nil
	}
	connection, ok := b.Connection(item.ConnectionID)
	if !ok {
		return nil
	}
	settings, configured := connection.ModelSettings[model]
	if !configured || (settings.MaxInputTokens <= 0 && settings.MaxOutputTokens <= 0) {
		return nil
	}
	return &agent.ModelTokenLimits{
		MaxInputTokens:  settings.MaxInputTokens,
		MaxOutputTokens: settings.MaxOutputTokens,
	}
}

func (b *Service) resolveSessionModelRoute(sessionID, model string) string {
	item, ok := b.sessions.Get(sessionID)
	if !ok || item.ConnectionID == "" {
		return ""
	}
	connection, ok := b.Connection(item.ConnectionID)
	if !ok {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		connection.ID,
		connection.Type,
		connection.Kind,
		connection.AuthKind,
		connection.BaseURL,
		model,
	}, "\x00")))
	return connection.ID + ":" + hex.EncodeToString(sum[:])
}

// MemoryCompleter returns the source session's configured model for the
// background memory worker.
func (b *Service) MemoryCompleter(sessionID string) (model.Completer, string, string, bool) {
	item, ok := b.sessions.Get(sessionID)
	if !ok {
		return nil, "", "", false
	}
	prov := b.resolveSessionProvider(sessionID)
	completer, ok := prov.(model.Completer)
	if !ok {
		return nil, "", "", false
	}
	if item.Model == "" {
		return nil, "", "", false
	}
	return completer, item.Model, string(item.ReasoningEffort), true
}

func newConnectionID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "conn_" + hex.EncodeToString(buf)
}
