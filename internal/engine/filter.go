package engine

// Filter defines the interface for individual policy checks or modifications.
// It is designed to evaluate and mutate the RequestContext.
type Filter interface {
	Execute(ctx *RequestContext) error
}

// Chain is a sequential slice of Filter objects representing a complete policy pipeline.
type Chain []Filter
