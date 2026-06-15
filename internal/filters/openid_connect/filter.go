package openid_connect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"go.uber.org/zap"

	"github.com/taha/myprog/internal/config"
	"github.com/taha/myprog/internal/engine"
	mylogger "github.com/taha/myprog/internal/logger"
)

// OpenIDConnectFilter validates incoming bearer tokens (JWT or opaque) against
// a configured OIDC provider and injects verified claims as upstream headers.
// It implements engine.Filter.
type OpenIDConnectFilter struct {
	cfg config.OIDCFilterConfig

	// jwksCache is the live background-refreshing JWKS cache used for JWT
	// verification. Nil when skip_discovery is true and a static key set is used.
	jwksCache *jwk.Cache

	// staticJWKS is used when a static inline key set is supplied instead of a
	// remote endpoint. Currently constructed from jwks_url for skip_discovery mode.
	jwksURL string

	// Parsed durations stored once at construction time.
	jwksCacheDuration time.Duration
	jwksTimeout       time.Duration
	userInfoTimeout   time.Duration
	clockSkew         time.Duration
}

// NewOpenIDConnectFilter constructs and initialises an OpenIDConnectFilter.
// It parses all duration fields and, unless skip_discovery is true, registers
// the JWKS URI with a background-refreshing jwk.Cache.
func NewOpenIDConnectFilter(cfg config.OIDCFilterConfig) (*OpenIDConnectFilter, error) {
	f := &OpenIDConnectFilter{cfg: cfg}

	// --- Duration parsing ---
	var err error
	if f.jwksCacheDuration, err = time.ParseDuration(cfg.JwksCacheDuration); err != nil {
		return nil, fmt.Errorf("openid-connect: invalid jwks_cache_duration %q: %w", cfg.JwksCacheDuration, err)
	}
	if f.jwksTimeout, err = time.ParseDuration(cfg.JwksTimeout); err != nil {
		return nil, fmt.Errorf("openid-connect: invalid jwks_timeout %q: %w", cfg.JwksTimeout, err)
	}
	if f.userInfoTimeout, err = time.ParseDuration(cfg.UserInfoTimeout); err != nil {
		return nil, fmt.Errorf("openid-connect: invalid userinfo_timeout %q: %w", cfg.UserInfoTimeout, err)
	}
	if f.clockSkew, err = time.ParseDuration(cfg.ClockSkew); err != nil {
		return nil, fmt.Errorf("openid-connect: invalid clock_skew %q: %w", cfg.ClockSkew, err)
	}

	// --- Resolve the JWKS URL ---
	jwksURL := cfg.JwksURL
	if jwksURL == "" {
		// Construct the default Keycloak/standard OIDC JWKS endpoint from the issuer URL.
		// For Keycloak: <issuer>/protocol/openid-connect/certs
		// For standard OIDC providers: <issuer>/jwks (handled by discovery, but we fall back here)
		base := strings.TrimSuffix(cfg.IssuerURL, "/")
		if strings.Contains(base, "/realms/") || strings.Contains(base, "/auth/") {
			// Keycloak-style issuer
			jwksURL = base + "/protocol/openid-connect/certs"
		} else {
			// Generic OIDC: /.well-known/openid-configuration would be preferred, but
			// since skip_discovery == false we construct a sensible default.
			jwksURL = base + "/.well-known/jwks.json"
		}
	}
	f.jwksURL = jwksURL

	// --- Initialise JWKS cache ---
	// We always set up a background cache; it is populated lazily on the first
	// Execute call, so construction never blocks on a network call.
	httpClient := &http.Client{Timeout: f.jwksTimeout}
	cache := jwk.NewCache(context.Background(),
		jwk.WithErrSink(errSinkFunc(func(err error) {
			mylogger.Warn("JWKS cache background refresh error",
				zap.String("provider", cfg.ProviderName),
				zap.String("jwks_url", jwksURL),
				zap.Error(err),
			)
		})),
	)
	if err := cache.Register(jwksURL,
		jwk.WithMinRefreshInterval(f.jwksCacheDuration),
		jwk.WithHTTPClient(httpClient),
	); err != nil {
		return nil, fmt.Errorf("openid-connect: failed to register JWKS URL %q: %w", jwksURL, err)
	}
	f.jwksCache = cache

	mylogger.Info("OpenID Connect filter initialised",
		zap.String("provider", cfg.ProviderName),
		zap.String("issuer_url", cfg.IssuerURL),
		zap.String("jwks_url", jwksURL),
	)
	return f, nil
}

// Execute validates the bearer token in the request and either blocks the
// request (401) or injects verified claims as upstream headers.
func (f *OpenIDConnectFilter) Execute(ctx *engine.RequestContext) error {
	token := f.extractToken(ctx)
	if token == "" {
		mylogger.Debug("openid-connect: no bearer token found, blocking request",
			zap.String("provider", f.cfg.ProviderName),
		)
		f.block(ctx)
		return nil
	}

	// Dispatch to JWT or opaque-token path.
	var claims map[string]interface{}
	var err error

	if strings.Count(token, ".") == 2 {
		claims, err = f.validateJWT(ctx.Ctx, token)
	} else {
		claims, err = f.validateOpaque(ctx.Ctx, token)
	}

	if err != nil {
		mylogger.Warn("openid-connect: token validation failed",
			zap.String("provider", f.cfg.ProviderName),
			zap.Error(err),
		)
		f.block(ctx)
		return nil
	}

	// --- Email-verified enforcement ---
	if !f.cfg.AllowUnverifiedEmail {
		if v, ok := claims["email_verified"]; ok {
			// email_verified can be a bool or a string depending on the provider.
			switch verified := v.(type) {
			case bool:
				if !verified {
					mylogger.Warn("openid-connect: email not verified, blocking request",
						zap.String("provider", f.cfg.ProviderName),
					)
					f.block(ctx)
					return nil
				}
			case string:
				if verified == "false" {
					mylogger.Warn("openid-connect: email not verified (string), blocking request",
						zap.String("provider", f.cfg.ProviderName),
					)
					f.block(ctx)
					return nil
				}
			}
		}
	}

	// --- Claim-to-header injection ---
	for _, mapping := range f.cfg.ClaimToHeaders {
		val, ok := claims[mapping.ClaimName]
		if !ok {
			continue
		}
		var headerVal string
		switch v := val.(type) {
		case string:
			headerVal = v
		case []interface{}:
			// Encode slices (e.g. groups) as a comma-separated list.
			parts := make([]string, 0, len(v))
			for _, item := range v {
				parts = append(parts, fmt.Sprintf("%v", item))
			}
			headerVal = strings.Join(parts, ",")
		default:
			headerVal = fmt.Sprintf("%v", v)
		}
		ctx.SetHeaderUpstream(mapping.HeaderName, headerVal)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Token extraction
// ---------------------------------------------------------------------------

// extractToken searches for a bearer token in the standard locations:
//  1. Authorization header with "Bearer " prefix.
//  2. "access_token" query parameter (passed via Envoy's :path pseudo-header).
func (f *OpenIDConnectFilter) extractToken(ctx *engine.RequestContext) string {
	// 1. Authorization header
	if authHeader := ctx.GetHeader("authorization"); authHeader != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(authHeader, prefix) {
			return strings.TrimPrefix(authHeader, prefix)
		}
	}

	// 2. access_token query parameter – Envoy forwards the full URL in :path.
	if path := ctx.GetHeader(":path"); path != "" {
		if idx := strings.Index(path, "access_token="); idx != -1 {
			rest := path[idx+len("access_token="):]
			if end := strings.IndexAny(rest, "&# "); end != -1 {
				return rest[:end]
			}
			return rest
		}
	}

	return ""
}

// ---------------------------------------------------------------------------
// JWT validation (stateless)
// ---------------------------------------------------------------------------

func (f *OpenIDConnectFilter) validateJWT(reqCtx context.Context, rawToken string) (map[string]interface{}, error) {
	// Retrieve the live key set from the background cache.
	fetchCtx, cancel := context.WithTimeout(reqCtx, f.jwksTimeout)
	defer cancel()

	keySet, err := f.jwksCache.Get(fetchCtx, f.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS: %w", err)
	}

	// Build JWT parse options.
	parseOpts := []jwt.ParseOption{
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
		jwt.WithAcceptableSkew(f.clockSkew),
	}
	if f.cfg.IssuerURL != "" {
		parseOpts = append(parseOpts, jwt.WithIssuer(f.cfg.IssuerURL))
	}

	token, err := jwt.Parse([]byte(rawToken), parseOpts...)
	if err != nil {
		return nil, fmt.Errorf("JWT parse/verify: %w", err)
	}

	// Validate audience if client_id is configured.
	if f.cfg.ClientID != "" {
		foundAud := false
		for _, aud := range token.Audience() {
			if aud == f.cfg.ClientID {
				foundAud = true
				break
			}
		}
		if !foundAud {
			return nil, fmt.Errorf("JWT audience mismatch: expected %q", f.cfg.ClientID)
		}
	}

	// Flatten token claims into a plain map for uniform downstream processing.
	m, err := token.AsMap(context.Background())
	if err != nil {
		return nil, fmt.Errorf("extracting JWT claims: %w", err)
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Opaque token validation (stateful – UserInfo endpoint)
// ---------------------------------------------------------------------------

func (f *OpenIDConnectFilter) validateOpaque(reqCtx context.Context, token string) (map[string]interface{}, error) {
	if f.cfg.UserInfoURL == "" {
		return nil, fmt.Errorf("opaque token received but userinfo_url is not configured")
	}

	reqCtx, cancel := context.WithTimeout(reqCtx, f.userInfoTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, f.cfg.UserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building UserInfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling UserInfo endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("UserInfo endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("reading UserInfo response body: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, fmt.Errorf("decoding UserInfo JSON: %w", err)
	}
	return claims, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (f *OpenIDConnectFilter) block(ctx *engine.RequestContext) {
	ctx.Blocked = true
	ctx.ResponseStatus = 401
	ctx.ResponseBody = "Unauthorized"
}

// errSinkFunc is a local function adapter that satisfies the jwk.ErrSink
// interface (which requires a single Error(error) method). This avoids
// importing the internal httprc package directly.
type errSinkFunc func(err error)

func (fn errSinkFunc) Error(err error) { fn(err) }
