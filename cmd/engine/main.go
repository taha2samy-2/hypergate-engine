package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/taha/myprog/internal/config"
	"github.com/taha/myprog/internal/engine"
	"github.com/taha/myprog/internal/filters"
	mygrpc "github.com/taha/myprog/internal/grpc"
	mylogger "github.com/taha/myprog/internal/logger"
	"github.com/taha/myprog/internal/memory"
	"github.com/taha/myprog/internal/redis"
	"github.com/taha/myprog/internal/router"
)

// Global map to track active health checkers across hot-reloads
type activeChecker struct {
	checker *redis.HealthChecker
	cancel  context.CancelFunc
}

var activeHealthCheckers = make(map[string]activeChecker)

func compileAndRegister(cfg *config.Config, registry *engine.ChainRegistry) error {
	newChains := make(map[string]engine.Chain)
	for name, chainConfig := range cfg.Chains {
		var compiledChain engine.Chain
		for _, filterCfg := range chainConfig {
			filter, err := filters.CreateFilter(filterCfg.Type, filterCfg.Options)
			if err != nil {
				mylogger.Error("Failed to compile filter", zap.String("chain", name), zap.Error(err))
				return err
			}
			compiledChain = append(compiledChain, filter)
		}
		newChains[name] = compiledChain
		mylogger.Info("Compiled filter chain", zap.String("chain_name", name), zap.Int("filters_count", len(compiledChain)))
	}
	registry.ReplaceAll(newChains)
	return nil
}

func startHealthCheckForService(parentCtx context.Context, name string, client redis.Client) {
	if existing, ok := activeHealthCheckers[name]; ok {
		existing.cancel()
	}

	childCtx, cancel := context.WithCancel(parentCtx)
	hc := redis.StartHealthCheckForClient(childCtx, client, name, 5*time.Second)

	activeHealthCheckers[name] = activeChecker{
		checker: hc,
		cancel:  cancel,
	}
}

// startRedisServices initializes the Redis manager and boots the background health-check loops.
func startRedisServices(ctx context.Context, cfg *config.Config) error {
	if len(cfg.Redis) == 0 {
		return nil
	}

	mylogger.Info("Initializing dynamic Redis manager for K8s services...")
	_, err := redis.NewManager(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize Redis manager: %w", err)
	}

	// Bootstrap active connection health checks for services that have it enabled
	for name, svcCfg := range cfg.Redis {
		if svcCfg.ActiveConnHealthCheck {
			client, ok := redis.GlobalManager.GetClient(name)
			if ok {
				mylogger.Info("Starting background active connection health check...", zap.String("service", name))
				// Register and spawn the background PING loop
				startHealthCheckForService(ctx, name, client)
			}
		}
	}

	return nil
}

func main() {
	// Create a global cancelable context to control background goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initialConfig, configPath, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error loading config: %v\n", err)
		os.Exit(1)
	}

	config.GlobalConfig.Store(initialConfig)

	if err := mylogger.InitLogger(&initialConfig.Telemetry.Logging); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer mylogger.Sync()

	mylogger.Info("Configuration loaded successfully", zap.String("version", initialConfig.Version))

	// Initialize the Redis Manager and Health Checkers at boot
	if err := startRedisServices(ctx, initialConfig); err != nil {
		mylogger.Fatal("Failed to bootstrap Redis services on boot", zap.Error(err))
	}

	registry := engine.NewChainRegistry()
	executor := engine.NewChainExecutor()
	pool := memory.NewContextPool()
	routerInst := router.NewEngineRouter()

	if err := compileAndRegister(initialConfig, registry); err != nil {
		mylogger.Fatal("Failed to compile chains on boot", zap.Error(err))
	}

	// Dynamic Hot-Reloading using fsnotify
	go config.WatchConfig(configPath, func(newConfig *config.Config) {
		mylogger.Info("Hot-reloading system connections and filter chains...")

		if len(newConfig.Redis) > 0 {
			if redis.GlobalManager == nil {
				// Handle cold-start transitioning from zero-Redis to multi-Redis dynamically
				if err := startRedisServices(ctx, newConfig); err != nil {
					mylogger.Error("Failed to dynamically bootstrap Redis manager on hot-reload", zap.Error(err))
				}
			} else {
				// Thread-safe atomic pointer swap of connection pools
				if err := redis.GlobalManager.Reload(newConfig); err != nil {
					mylogger.Error("Failed to gracefully reload Redis connection pools", zap.Error(err))
				} else {
					for name, svcCfg := range newConfig.Redis {
						client, ok := redis.GlobalManager.GetClient(name)
						if ok && svcCfg.ActiveConnHealthCheck {
							startHealthCheckForService(ctx, name, client)
						} else {
							if existing, ok := activeHealthCheckers[name]; ok {
								existing.cancel()
								delete(activeHealthCheckers, name)
							}
						}
					}
					for name, existing := range activeHealthCheckers {
						if _, ok := newConfig.Redis[name]; !ok {
							existing.cancel()
							delete(activeHealthCheckers, name)
						}
					}
				}
			}

		}

		_ = compileAndRegister(newConfig, registry)
	})

	grpcServer := mygrpc.NewGRPCServer(pool, routerInst, registry, executor)

	address := initialConfig.Server.Address
	if address == "" {
		address = ":9001"
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		mylogger.Fatal("Failed to bind network listener", zap.String("address", address), zap.Error(err))
	}

	mylogger.Info("Starting high-performance gRPC Server", zap.String("address", address))

	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			mylogger.Fatal("gRPC server encountered a fatal error", zap.Error(err))
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	sig := <-stop

	mylogger.Info("OS signal caught, initiating graceful shutdown...", zap.String("signal", sig.String()))

	// Cancel the context first to notify background health checks and timers to exit cleanly
	cancel()

	// Shutdown the gRPC server gracefully
	grpcServer.GracefulStop()

	// Safely close any remaining open Redis connections
	if redis.GlobalManager != nil {
		redis.GlobalManager.Close()
	}

	mylogger.Info("gRPC server stopped gracefully")
}
