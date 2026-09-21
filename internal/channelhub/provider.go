package channelhub

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// Provider owns the configuration and transport lifecycle for one messaging platform.
// ChannelCore owns the shared conversation lifecycle used by provider bots.
type Provider interface {
	Kind() string
	List() []State
	Get(string) (State, bool)
	Create(UpdateInput) (State, error)
	Update(string, UpdateInput) (State, error)
	Delete(string) error
	Close()
}

type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewProviderRegistry(providers ...Provider) (*ProviderRegistry, error) {
	registry := &ProviderRegistry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if err := registry.Register(provider); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *ProviderRegistry) Register(provider Provider) error {
	if provider == nil {
		return errors.New("channel provider is required")
	}
	kind := strings.TrimSpace(provider.Kind())
	if kind == "" {
		return errors.New("channel provider kind is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[kind]; exists {
		return errors.New("duplicate channel provider kind")
	}
	r.providers[kind] = provider
	return nil
}

func (r *ProviderRegistry) Get(kind string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[strings.TrimSpace(kind)]
	return provider, ok
}

func (r *ProviderRegistry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]string, 0, len(r.providers))
	for kind := range r.providers {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	providers := make([]Provider, 0, len(kinds))
	for _, kind := range kinds {
		providers = append(providers, r.providers[kind])
	}
	return providers
}
