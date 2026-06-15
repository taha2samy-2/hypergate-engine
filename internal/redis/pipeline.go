// Package redis — pipeline.go
//
// pipeline.go implements the PipeAppend and PipeDo methods of clientImpl.
//
// # Design Rationale
//
// Redis Cluster imposes a constraint that all keys in a single command (or
// pipeline) must reside in the same hash slot. A naïve pipeline that mixes
// keys from different slots will receive MOVED/CROSSSLOT errors. The solution
// is Grouped Pipelining:
//
//  1. Group PipelineActions by the hash slot of their key, preserving the
//     first-appearance order of each group so the caller sees results in a
//     deterministic order.
//  2. Execute each per-slot group as an independent radix.Pipeline, either
//     serially (parallelism == 1) or concurrently bounded by a semaphore
//     (parallelism > 1 or == 0 for unbounded).
//
// For SINGLE and SENTINEL topologies a plain radix.Pipeline is used — the
// cluster grouping logic is completely bypassed.
//
// # Concurrency Invariants
//
//   - PipeAppend is pure (no shared mutable state) and safe to call from any
//     goroutine.
//   - PipeDo is safe to call concurrently; each invocation builds its own
//     radix.Pipeline objects on the stack and borrows a connection from the
//     underlying pool independently.
//   - The per-slot goroutines spawned by the concurrent cluster path share no
//     state with each other; each operates on a disjoint slice of actions.
package redis

import (
	"context"
	"fmt"

	"github.com/mediocregopher/radix/v4"
	"golang.org/x/sync/errgroup"
)

// ---------------------------------------------------------------------------
// PipeAppend
// ---------------------------------------------------------------------------

// PipeAppend constructs a radix.FlatCmd action for the given Redis command and
// appends it — together with its routing key — to the supplied Pipeline slice.
//
// FlatCmd is used (rather than Cmd) so that callers can pass native Go values
// (ints, structs, slices) as args without pre-serialising them to strings. The
// key is included as the first argument so radix can derive the correct cluster
// hash slot from the action's Properties().Keys field.
//
// No network I/O is performed by this method. Call PipeDo to flush.
func (c *clientImpl) PipeAppend(pipeline Pipeline, rcv interface{}, cmd, key string, args ...interface{}) Pipeline {
	// Prepend key to args so FlatCmd receives: cmd key arg1 arg2 ...
	// This matches Redis's wire format and allows radix to extract Keys from
	// the action's Properties for cluster slot routing.
	allArgs := make([]interface{}, 0, 1+len(args))
	if key != "" {
		allArgs = append(allArgs, key)
	}
	allArgs = append(allArgs, args...)

	action := radix.FlatCmd(rcv, cmd, allArgs...)
	return append(pipeline, PipelineAction{Action: action, Key: key})
}

// ---------------------------------------------------------------------------
// PipeDo
// ---------------------------------------------------------------------------

// PipeDo executes all actions accumulated in pipeline as a single (or
// per-slot) network operation.
//
// # SINGLE / SENTINEL mode
//
// All actions are appended to one radix.Pipeline and flushed with a single
// pool.Do call — one network round-trip.
//
// # CLUSTER mode
//
// Actions are grouped by their routing key. Each group is flushed as an
// independent radix.Pipeline on the correct shard. Groups are executed:
//
//   - Serially     when c.clusterPipelineParallelism == 1.
//   - Concurrently when c.clusterPipelineParallelism == 0 (unbounded) or > 1
//     (bounded by a semaphore channel of that capacity).
//
// The first error from any group is returned; the errgroup cancels remaining
// goroutines so in-flight RPCs drain quickly.
func (c *clientImpl) PipeDo(ctx context.Context, pipeline Pipeline) error {
	if len(pipeline) == 0 {
		return nil
	}

	if !c.isCluster {
		return c.pipeDoFlat(ctx, pipeline)
	}
	return c.pipeDoCluster(ctx, pipeline)
}

// pipeDoFlat flushes all actions in a single radix.Pipeline — used for SINGLE
// and SENTINEL topologies where cross-slot restrictions do not apply.
func (c *clientImpl) pipeDoFlat(ctx context.Context, pipeline Pipeline) error {
	p := radix.NewPipeline()
	for _, pa := range pipeline {
		p.Append(pa.Action)
	}
	if err := c.client.Do(ctx, p); err != nil {
		return fmt.Errorf("redis[%s].PipeDo(flat): pipeline flush failed (%d actions): %w",
			c.serviceName, len(pipeline), err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Cluster grouped-pipeline internals
// ---------------------------------------------------------------------------

// slotGroup holds all actions that share the same routing key. firstIndex is
// the position of the first action from this group in the original Pipeline
// slice and is used only for stable ordering of group execution.
type slotGroup struct {
	key        string
	firstIndex int
	actions    []radix.Action
}

// pipeDoCluster implements the grouped-pipeline pattern for Redis Cluster.
//
// Step 1 — GROUP: Iterate the Pipeline in order. For each PipelineAction,
// look up (or create) a slotGroup keyed by PipelineAction.Key. Append the
// Action. Because we iterate in order and use firstIndex for sorting, the
// execution order of groups mirrors the first appearance of each unique key in
// the original pipeline.
//
// Step 2 — EXECUTE: Flush each group either serially or concurrently depending
// on c.clusterPipelineParallelism.
func (c *clientImpl) pipeDoCluster(ctx context.Context, pipeline Pipeline) error {
	// --- Step 1: Group actions by routing key ---
	// groupOrder preserves insertion order of unique keys.
	groupOrder := make([]string, 0, len(pipeline))
	groups := make(map[string]*slotGroup, len(pipeline))

	for i, pa := range pipeline {
		g, exists := groups[pa.Key]
		if !exists {
			g = &slotGroup{
				key:        pa.Key,
				firstIndex: i,
				actions:    make([]radix.Action, 0, 4),
			}
			groups[pa.Key] = g
			groupOrder = append(groupOrder, pa.Key)
		}
		g.actions = append(g.actions, pa.Action)
	}

	// Build an ordered slice of groups for deterministic execution.
	ordered := make([]*slotGroup, 0, len(groupOrder))
	for _, k := range groupOrder {
		ordered = append(ordered, groups[k])
	}

	// --- Step 2: Execute groups ---
	parallelism := c.clusterPipelineParallelism

	if parallelism == 1 {
		// Serial path — no goroutines, no overhead.
		return c.execGroupsSerial(ctx, ordered)
	}
	// Concurrent path (parallelism == 0 → unbounded; > 1 → bounded).
	return c.execGroupsConcurrent(ctx, ordered, parallelism)
}

// execGroupsSerial flushes each slotGroup one at a time using a single
// goroutine. This is the lowest-overhead path and is preferred when
// clusterPipelineParallelism == 1.
func (c *clientImpl) execGroupsSerial(ctx context.Context, groups []*slotGroup) error {
	for _, g := range groups {
		if err := c.flushGroup(ctx, g); err != nil {
			return err
		}
	}
	return nil
}

// execGroupsConcurrent flushes slot groups concurrently using errgroup.
//
// When maxParallel == 0 all groups are launched simultaneously. When
// maxParallel > 1 a buffered semaphore channel limits the number of goroutines
// that hold a pool connection at the same time, preventing pool exhaustion on
// large pipelines.
func (c *clientImpl) execGroupsConcurrent(ctx context.Context, groups []*slotGroup, maxParallel int) error {
	eg, egCtx := errgroup.WithContext(ctx)

	// sem is a counting semaphore: a nil channel means "no limit".
	var sem chan struct{}
	if maxParallel > 0 {
		sem = make(chan struct{}, maxParallel)
	}

	for _, g := range groups {
		g := g // capture for goroutine

		eg.Go(func() error {
			// Acquire semaphore slot if bounded.
			if sem != nil {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-egCtx.Done():
					return fmt.Errorf("redis[%s].PipeDo(cluster): context cancelled waiting for semaphore: %w",
						c.serviceName, egCtx.Err())
				}
			}
			return c.flushGroup(egCtx, g)
		})
	}

	return eg.Wait()
}

// flushGroup sends all actions in a single slotGroup as one radix.Pipeline.
// Because all actions share the same routing key they hash to the same cluster
// slot and radix routes the pipeline to the correct shard automatically.
func (c *clientImpl) flushGroup(ctx context.Context, g *slotGroup) error {
	p := radix.NewPipeline()
	for _, a := range g.actions {
		p.Append(a)
	}
	if err := c.client.Do(ctx, p); err != nil {
		return fmt.Errorf("redis[%s].PipeDo(cluster, key=%q, %d actions): %w",
			c.serviceName, g.key, len(g.actions), err)
	}
	return nil
}
