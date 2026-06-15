package engine

import "sync"

type ChainRegistry struct {
	mu     sync.RWMutex
	chains map[string]Chain
}

func NewChainRegistry() *ChainRegistry {
	return &ChainRegistry{
		chains: make(map[string]Chain),
	}
}

func (r *ChainRegistry) Register(name string, chain Chain) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chains[name] = chain
}

func (r *ChainRegistry) Get(name string) (Chain, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain, exists := r.chains[name]
	return chain, exists
}

func (r *ChainRegistry) ReplaceAll(newChains map[string]Chain) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chains = newChains
}
