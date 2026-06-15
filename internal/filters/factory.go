package filters

import (
	"fmt"

	"github.com/taha/myprog/internal/config"
	"github.com/taha/myprog/internal/engine"
	"github.com/taha/myprog/internal/filters/correlation_id"
	"github.com/taha/myprog/internal/filters/deny"
	"github.com/taha/myprog/internal/filters/header_modifier"
	"github.com/taha/myprog/internal/filters/openid_connect"
	"github.com/taha/myprog/internal/filters/rate_limiter"
	"github.com/taha/myprog/internal/redis"
	"gopkg.in/yaml.v3"
)

func CreateFilter(filterType string, rawOptions interface{}) (engine.Filter, error) {
	optsBytes, err := yaml.Marshal(rawOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal raw options for filter type %q: %w", filterType, err)
	}

	switch filterType {
	case "embedded_rate_limiter":
		var cfg rate_limiter.FilterOptions
		if err := yaml.Unmarshal(optsBytes, &cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config for embedded_rate_limiter: %w", err)
		}

		// Enforce default values for response headers
		cfg.ApplyDefaults()

		// Resolve the K8s Redis Client
		if redis.GlobalManager == nil {
			return nil, fmt.Errorf("global redis manager is not initialized")
		}

		// Compile and instantiate the selected polymorphic strategy
		executor, err := rate_limiter.ResolveExecutor(cfg.Algorithm, cfg.RedisService, redis.GlobalManager, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve rate limiter executor: %w", err)
		}

		// Construct and return the RateLimiterFilter instance
		return rate_limiter.NewRateLimiterFilter("embedded_rate_limiter", cfg, executor), nil

	case "header_modifier":
		var cfg header_modifier.HeaderModifierConfig
		if err := yaml.Unmarshal(optsBytes, &cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config for header_modifier: %w", err)
		}
		return header_modifier.NewHeaderModifierFilter(cfg), nil

	case "deny":
		var cfg deny.DenyFilterConfig
		if err := yaml.Unmarshal(optsBytes, &cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config for deny: %w", err)
		}
		f, err := deny.NewDenyFilter(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize deny filter: %w", err)
		}
		return f, nil

	case "correlation_id":
		cfg := correlation_id.CorrelationConfig{
			PropagateToUpstream:   true,
			PropagateToDownstream: true,
		}
		if err := yaml.Unmarshal(optsBytes, &cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config for correlation_id: %w", err)
		}
		f, err := correlation_id.NewCorrelationFilter(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize correlation_id filter: %w", err)
		}
		return f, nil

	case "openid-connect":
		cfg, err := config.ParseOIDCFilterConfig(rawOptions)
		if err != nil {
			return nil, fmt.Errorf("failed to parse config for openid-connect: %w", err)
		}
		f, err := openid_connect.NewOpenIDConnectFilter(*cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize openid-connect filter: %w", err)
		}
		return f, nil

	default:
		return nil, fmt.Errorf("unknown filter type: %s", filterType)
	}
}
