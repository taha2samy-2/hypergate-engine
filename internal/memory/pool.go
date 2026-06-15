package memory

import (
	"sync"

	"github.com/taha/myprog/internal/engine"
)

type ContextPool struct {
	pool *sync.Pool
}

func NewContextPool() *ContextPool {
	return &ContextPool{
		pool: &sync.Pool{
			New: func() interface{} {
				return &engine.RequestContext{
					Headers:              make(map[string]string, 20),
					HeadersToAdd:         make([]engine.Header, 0, 20),
					ResponseHeadersToAdd: make([]engine.Header, 0, 20),
					HeadersToRemove:      make([]string, 0, 10),
					UpstreamShadow:       make(map[string]string, 30),
					DownstreamShadow:     make(map[string]string, 30),
				}
			},
		},
	}
}

func (cp *ContextPool) Acquire() *engine.RequestContext {
	return cp.pool.Get().(*engine.RequestContext)
}

func (cp *ContextPool) Release(ctx *engine.RequestContext) {
	ctx.Reset()
	cp.pool.Put(ctx)
}
