package grpc

import (
	"io"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocfilterv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
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

	var targetChainName string

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

			targetChainName = s.router.Route(reqCtx)
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
				ModeOverride: s.buildModeOverride(reqCtx),
			}
			if err := stream.Send(resp); err != nil {
				mylogger.Error("Failed to send RequestHeaders response", zap.Error(err))
				return err
			}

		case *extprocv3.ProcessingRequest_RequestBody:
			mylogger.Debug("Received RequestBody phase")
			reqCtx.RequestBody = msg.RequestBody.Body

			if targetChainName != "" {
				chain, exists := s.registry.Get(targetChainName)
				if exists {
					if err := s.executor.Execute(reqCtx, chain); err != nil {
						mylogger.Error("Error executing chain on body", zap.Error(err))
					}
				}
			}

			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestBody{
					RequestBody: &extprocv3.BodyResponse{
						Response: &extprocv3.CommonResponse{
							BodyMutation: s.buildBodyMutation(reqCtx.RequestBody, reqCtx.RequestBodyModified),
						},
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				mylogger.Error("Failed to send RequestBody response", zap.Error(err))
				return err
			}

		case *extprocv3.ProcessingRequest_RequestTrailers:
			mylogger.Debug("Received RequestTrailers phase")
			trailers := msg.RequestTrailers.Trailers
			if trailers != nil {
				for _, h := range trailers.Headers {
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

			if targetChainName != "" {
				chain, exists := s.registry.Get(targetChainName)
				if exists {
					if err := s.executor.Execute(reqCtx, chain); err != nil {
						mylogger.Error("Error executing chain on request trailers", zap.Error(err))
					}
				}
			}

			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_RequestTrailers{
					RequestTrailers: &extprocv3.TrailersResponse{
						HeaderMutation: s.buildHeaderMutation(reqCtx.RequestTrailersToAdd, reqCtx.RequestTrailersToRemove),
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				mylogger.Error("Failed to send RequestTrailers response", zap.Error(err))
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
				ModeOverride: s.buildModeOverride(reqCtx),
			}
			if err := stream.Send(resp); err != nil {
				mylogger.Error("Failed to send ResponseHeaders response", zap.Error(err))
				return err
			}

		case *extprocv3.ProcessingRequest_ResponseBody:
			mylogger.Debug("Received ResponseBody phase")
			reqCtx.ResponseBodyBytes = msg.ResponseBody.Body

			if targetChainName != "" {
				chain, exists := s.registry.Get(targetChainName)
				if exists {
					if err := s.executor.Execute(reqCtx, chain); err != nil {
						mylogger.Error("Error executing chain on response body", zap.Error(err))
					}
				}
			}

			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ResponseBody{
					ResponseBody: &extprocv3.BodyResponse{
						Response: &extprocv3.CommonResponse{
							BodyMutation: s.buildBodyMutation(reqCtx.ResponseBodyBytes, reqCtx.ResponseBodyModified),
						},
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				mylogger.Error("Failed to send ResponseBody response", zap.Error(err))
				return err
			}

		case *extprocv3.ProcessingRequest_ResponseTrailers:
			mylogger.Debug("Received ResponseTrailers phase")
			trailers := msg.ResponseTrailers.Trailers
			if trailers != nil {
				for _, h := range trailers.Headers {
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

			if targetChainName != "" {
				chain, exists := s.registry.Get(targetChainName)
				if exists {
					if err := s.executor.Execute(reqCtx, chain); err != nil {
						mylogger.Error("Error executing chain on response trailers", zap.Error(err))
					}
				}
			}

			resp := &extprocv3.ProcessingResponse{
				Response: &extprocv3.ProcessingResponse_ResponseTrailers{
					ResponseTrailers: &extprocv3.TrailersResponse{
						HeaderMutation: s.buildHeaderMutation(reqCtx.ResponseTrailersToAdd, reqCtx.ResponseTrailersToRemove),
					},
				},
			}
			if err := stream.Send(resp); err != nil {
				mylogger.Error("Failed to send ResponseTrailers response", zap.Error(err))
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

func (s *Server) buildBodyMutation(body []byte, modified bool) *extprocv3.BodyMutation {
	if !modified {
		return nil
	}
	if len(body) == 0 {
		return &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_ClearBody{
				ClearBody: true,
			},
		}
	}
	return &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{
			Body: body,
		},
	}
}

func (s *Server) buildModeOverride(reqCtx *engine.RequestContext) *extprocfilterv3.ProcessingMode {
	if !reqCtx.RequestBodyRequired && !reqCtx.ResponseBodyRequired && !reqCtx.RequestTrailersRequired && !reqCtx.ResponseTrailersRequired {
		return nil
	}

	mode := &extprocfilterv3.ProcessingMode{
		RequestHeaderMode:  extprocfilterv3.ProcessingMode_SEND,
		ResponseHeaderMode: extprocfilterv3.ProcessingMode_SEND,
	}

	if reqCtx.RequestBodyRequired {
		mode.RequestBodyMode = extprocfilterv3.ProcessingMode_BUFFERED
	} else {
		mode.RequestBodyMode = extprocfilterv3.ProcessingMode_NONE
	}

	if reqCtx.ResponseBodyRequired {
		mode.ResponseBodyMode = extprocfilterv3.ProcessingMode_BUFFERED
	} else {
		mode.ResponseBodyMode = extprocfilterv3.ProcessingMode_NONE
	}

	if reqCtx.RequestTrailersRequired {
		mode.RequestTrailerMode = extprocfilterv3.ProcessingMode_SEND
	} else {
		mode.RequestTrailerMode = extprocfilterv3.ProcessingMode_SKIP
	}

	if reqCtx.ResponseTrailersRequired {
		mode.ResponseTrailerMode = extprocfilterv3.ProcessingMode_SEND
	} else {
		mode.ResponseTrailerMode = extprocfilterv3.ProcessingMode_SKIP
	}

	return mode
}
