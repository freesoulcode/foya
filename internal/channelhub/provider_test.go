package channelhub

import (
	"errors"
	"testing"
)

func TestProviderRegistryRejectsDuplicateKindsAndOrdersProviders(t *testing.T) {
	registry, err := NewProviderRegistry(fakeProvider{kind: "telegram"}, fakeProvider{kind: "feishu"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewProviderRegistry(fakeProvider{kind: "feishu"}, fakeProvider{kind: "feishu"}); err == nil {
		t.Fatal("duplicate provider kind was accepted")
	}
	providers := registry.All()
	if len(providers) != 2 || providers[0].Kind() != "feishu" || providers[1].Kind() != "telegram" {
		t.Fatalf("providers = %#v", providers)
	}
}

type fakeProvider struct{ kind string }

func (p fakeProvider) Kind() string                    { return p.kind }
func (fakeProvider) List() []State                     { return nil }
func (fakeProvider) Get(string) (State, bool)          { return State{}, false }
func (fakeProvider) Create(UpdateInput) (State, error) { return State{}, errors.New("not implemented") }
func (fakeProvider) Update(string, UpdateInput) (State, error) {
	return State{}, errors.New("not implemented")
}
func (fakeProvider) Delete(string) error { return errors.New("not implemented") }
func (fakeProvider) Close()              {}
