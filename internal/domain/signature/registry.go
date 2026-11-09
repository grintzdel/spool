package signature

import (
	"strings"
	"sync"

	"github.com/grintzdel/spool/internal/domain/webhook"
)

type Registry struct {
	mu        sync.RWMutex
	providers []Provider
}

var Default = &Registry{}

func Register(p Provider) { Default.Register(p) }

func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.providers {
		if strings.EqualFold(existing.Name(), p.Name()) {
			r.providers[i] = p
			return
		}
	}
	r.providers = append(r.providers, p)
}

func (r *Registry) Lookup(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if strings.EqualFold(p.Name(), name) {
			return p, true
		}
	}
	return nil, false
}

func (r *Registry) Detect(h webhook.Headers) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Detect(h) {
			return p, true
		}
	}
	return nil, false
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.providers))
	for i, p := range r.providers {
		names[i] = p.Name()
	}
	return names
}
