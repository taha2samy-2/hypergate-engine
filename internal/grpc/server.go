package grpc

import (
	"io"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"

	"github.com/taha/myprog/internal/config"
	"github.com/taha/myprog/internal/engine"
	mylogger "github.com/taha/myprog/internal/logger"
	"github.com/taha/myprog/internal/memory"
	"github.com/taha/myprog/internal/router"
)

type Server struct {
	pool     *memory.ContextPool
	router   *router.EngineRouter
	registry *engine.ChainRegistry
	executor *engine.ChainExecutor
	extprocv3.UnimplementedExternalProcessorServer
}

func NewGRPCServer(
	pool *memory.ContextPool,
	routerInst *router.EngineRouter,
	registry *engine.ChainRegistry,
	executor *engine.ChainExecutor,
) *grpc.Server {
	activeCfg := config.GlobalConfig.Load()

	kaParams := keepalive.ServerParameters{
		MaxConnectionIdle:     15 * time.Minute,
		MaxConnectionAge:      30 * time.Minute,
		MaxConnectionAgeGrace: 5 * time.Minute,
		Time:                  5 * time.Minute,
		Timeout:               1 * time.Second,
	}

	kaEnforcement := keepalive.EnforcementPolicy{
		MinTime:             5 * time.Minute,
		PermitWithoutStream: true,
	}

	var opts []grpc.ServerOption
	opts = append(opts, grpc.KeepaliveParams(kaParams))
	opts = append(opts, grpc.KeepaliveEnforcementPolicy(kaEnforcement))

	if activeCfg != nil && activeCfg.Server.MaxConcurrentStreams > 0 {
		opts = append(opts, grpc.MaxConcurrentStreams(activeCfg.Server.MaxConcurrentStreams))
	} else {
		opts = append(opts, grpc.MaxConcurrentStreams(10000))
	}

	grpcServer := grpc.NewServer(opts...)

	authServer := &Server{
		pool:     pool,
		router:   routerInst,
		registry: registry,
		executor: executor,
	}

	extprocv3.RegisterExternalProcessorServer(grpcServer, authServer)

	return grpcServer
}

func (s *Server) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	mylogger.Debug("ext_proc bidirectional stream opened")
	startTime := time.Now()

	reqCtx := s.pool.Acquire()
	reqCtx.Ctx = stream.Context()
	defer func() {
		mylogger.Debug("ext_proc stream closing, releasing context", zap.Duration("duration", time.Since(startTime)))
		s.pool.Release(reqCtx)
	}()

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			mylogger.Debug("ext_proc stream closed by Envoy (EOF)")
			return nil
		}
		if err != nil {
			if stream.Context().Err() != nil {
				mylogger.Debug("ext_proc stream context canceled gracefully")
				return nil
			}
			mylogger.Error("ext_proc stream receive error", zap.Error(err))
			return err
		}

		switch msg := req.Request.(type) {
		case *extprocv3.ProcessingRequest_RequestHeaders:
			mylogger.Debug("Received RequestHeaders phase")
			headers := msg.RequestHeaders.Headers
			if headers != nil {
				for _, h := range headers.Headers {
					key := h.Key
					var val string
					if len(h.RawValue) > 0 {
						val = string(h.RawValue)
					} else {
						val = h.Value
					}
					reqCtx.Headers[key] = val
				}
			}

			reqCtx.Path = reqCtx.Headers[":path"]
			reqCtx.Method = reqCtx.Headers[":method"]

			mylogger.Debug("Parsed RequestHeaders attributes",
				zap.String("path", reqCtx.Path),
				zap.String("method", reqCtx.Method),
			)

			targetChainName := s.router.Route(reqCtx)
			if targetChainName != "" {
				chain, exists := s.registry.Get(targetChainName)
				if !exists {
					mylogger.Warn("Target chain not found in registry", zap.String("chain", targetChainName))
				} else {
					if err := s.executor.Execute(reqCtx, chain); err != nil {
						mylogger.Error("Error executing chain", zap.String("chain", targetChainName), zap.Error(err))
					}
				}
			}

			if reqCtx.Blocked {
				mylogger.Info("Request blocked by filter chain, sending ImmediateResponse",
					zap.String("path", reqCtx.Path),
					zap.Int32("status_code", reqCtx.ResponseStatus),
				)

				resp := &extprocv3.ProcessingResponse{
					Response: &extprocv3.ProcessingResponse_ImmediateResponse{
						ImmediateResponse: &extprocv3.ImmediateResponse{
							Status: &typev3.HttpStatus{
								Code: typev3.StatusCode(reqCtx.ResponseStatus),
							},
							Headers: s.buildHeaderMutation(reqCtx.ResponseHeadersToAdd, nil),
							Body:    []byte(reqCtx.ResponseBody),
						},
					},
				}
				if err := stream.Send(resp); err != nil {
					mylogger.Error("Failed to send ImmediateResponse", zap.Error(err))
					return err
				}
				return nil
			}

			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extprocv3.HeadersResponse{
						Response: &extprocv3.CommonResponse{
							HeaderMutation: s.buildHeaderMutation(reqCtx.HeadersToAdd, reqCtx.HeadersToRemove),
						},
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				mylogger.Error("Failed to send RequestHeaders response", zap.Error(err))
				return err
			}

		case *extprocv3.ProcessingRequest_ResponseHeaders:
			mylogger.Debug("Received ResponseHeaders phase")
			headers := msg.ResponseHeaders.Headers
			if headers != nil {
				for _, h := range headers.Headers {
					key := h.Key
					var val string
					if len(h.RawValue) > 0 {
						val = string(h.RawValue)
					} else {
						val = h.Value
					}
					reqCtx.Headers[key] = val
				}
			}

			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ResponseHeaders{
					ResponseHeaders: &extprocv3.HeadersResponse{
						Response: &extprocv3.CommonResponse{
							HeaderMutation: s.buildHeaderMutation(reqCtx.ResponseHeadersToAdd, nil),
						},
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				mylogger.Error("Failed to send ResponseHeaders response", zap.Error(err))
				return err
			}
		}
	}
}

func (s *Server) buildHeaderMutation(headers []engine.Header, removes []string) *extprocv3.HeaderMutation {
	var setHeaders []*corev3.HeaderValueOption
	for _, h := range headers {
		setHeaders = append(setHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:      h.Key,
				RawValue: []byte(h.Value),
			},
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		})
	}
	return &extprocv3.HeaderMutation{
		SetHeaders:    setHeaders,
		RemoveHeaders: removes,
	}
}
