// FinScore Alternative Credit Scoring API
//
// Entry point for the Go REST API gateway.  The service:
//   - Exposes POST /v1/score behind OAuth2 Bearer authentication.
//   - Communicates with the Python XGBoost microservice over gRPC.
//   - Emits structured JSON logs suitable for an ELK (Elasticsearch /
//     Logstash / Kibana) audit trail as required by Kenya's Office of the
//     Data Protection Commissioner (ODPC).
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/eliasndungu/finscore/internal/auth"
	grpcclient "github.com/eliasndungu/finscore/internal/grpc"
	"github.com/eliasndungu/finscore/internal/handlers"
	"github.com/eliasndungu/finscore/internal/logging"
)

func main() {
	// ------------------------------------------------------------------ //
	// 1. Initialise structured logging                                     //
	// ------------------------------------------------------------------ //
	env := getEnv("APP_ENV", "development")
	logging.Init(env)
	defer logging.Logger.Sync() //nolint:errcheck

	log := logging.Logger

	// ------------------------------------------------------------------ //
	// 2. Load configuration from environment variables                    //
	// ------------------------------------------------------------------ //
	jwtSecret := requireEnv("JWT_SECRET", log)

	// In production the allowed clients would be stored in a database.
	// For bootstrapping, a single client_id / client_secret pair may be
	// injected via environment variables.
	allowedClients := map[string]string{
		getEnv("OAUTH_CLIENT_ID", "finscore-demo"): getEnv("OAUTH_CLIENT_SECRET", "change-me"),
	}

	scorerAddr := getEnv("SCORER_ADDR", "localhost:50051")
	grpcTimeout := parseDuration(getEnv("GRPC_TIMEOUT", "10s"))
	httpAddr := getEnv("HTTP_ADDR", ":8080")

	// ------------------------------------------------------------------ //
	// 3. Create gRPC client to the Python scorer microservice             //
	// ------------------------------------------------------------------ //
	grpcClient, err := grpcclient.New(scorerAddr, grpcTimeout)
	if err != nil {
		log.Fatal("failed to connect to scoring service", zap.Error(err))
	}
	defer grpcClient.Close()

	// ------------------------------------------------------------------ //
	// 4. Set up Gin router with middleware                                //
	// ------------------------------------------------------------------ //
	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	// Recovery middleware: returns 500 on panics instead of crashing.
	router.Use(gin.Recovery())

	// Request-level structured logging.
	router.Use(requestLogger(log))

	authCfg := auth.Config{
		JWTSecret:     jwtSecret,
		TokenEndpoint: "/oauth/token",
	}

	// OAuth2 token endpoint (no auth required to obtain a token).
	router.POST("/oauth/token", auth.IssueToken(authCfg, allowedClients))

	// Health probe (unauthenticated — used by load-balancers / k8s).
	router.GET("/health", handlers.HealthCheck)

	// Authenticated scoring API.
	scoreHandler := handlers.NewScoreHandler(grpcClient)
	v1 := router.Group("/v1", auth.Middleware(authCfg))
	{
		v1.POST("/score", scoreHandler.Score)
	}

	// ------------------------------------------------------------------ //
	// 5. Start HTTP server with graceful shutdown                         //
	// ------------------------------------------------------------------ //
	srv := &http.Server{
		Addr:         httpAddr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("HTTP server listening", zap.String("addr", httpAddr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down server…")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("server shutdown error", zap.Error(err))
	}
	log.Info("server stopped")
}

// ---- helpers ----

func requestLogger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http_request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.FullPath()),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency_ms", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string, log *zap.Logger) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatal("required environment variable not set", zap.String("key", key))
	}
	return v
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 10 * time.Second
	}
	return d
}
