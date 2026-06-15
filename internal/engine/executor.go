package engine

import (
	"go.uber.org/zap"

	mylogger "github.com/taha/myprog/internal/logger"
)

// ChainExecutor is responsible for executing a chain of filters against a request context.
type ChainExecutor struct{}

// NewChainExecutor creates a new ChainExecutor.
func NewChainExecutor() *ChainExecutor {
	return &ChainExecutor{}
}

// Execute processes the RequestContext through the provided filter chain sequentially.
// It executes with 0 allocs/op and fast-fails if the context becomes blocked.
func (e *ChainExecutor) Execute(ctx *RequestContext, chain Chain) error {
	mylogger.Debug("Executing filter chain", zap.Int("filters_count", len(chain)))

	for i := 0; i < len(chain); i++ {
		// Fast-Fail / Circuit Break if previously blocked (just in case)
		if ctx.Blocked {
			mylogger.Info("Request blocked by filter chain",
				zap.Int32("status_code", int32(ctx.ResponseStatus)),
				zap.String("response_body", ctx.ResponseBody),
				zap.String("path", ctx.Path),
			)
			break
		}

		mylogger.Debug("Executing filter in chain", zap.Int("filter_index", i))

		filter := chain[i]
		if err := filter.Execute(ctx); err != nil {
			mylogger.Error("Filter execution failed with internal error", zap.String("error", err.Error()), zap.Int("filter_index", i))

			// Block the request gracefully due to internal server error
			ctx.Blocked = true
			ctx.ResponseStatus = 500
			ctx.ResponseBody = "Internal Server Error"

			return err
		}

		// Fast-Fail / Circuit Break after filter execution
		if ctx.Blocked {
			mylogger.Info("Request blocked by filter chain",
				zap.Int32("status_code", int32(ctx.ResponseStatus)),
				zap.String("response_body", ctx.ResponseBody),
				zap.String("path", ctx.Path),
			)
			break
		}
	}

	return nil
}
