// Package auth provides OAuth2 / JWT bearer-token middleware for the
// FinScore API.  Tokens are issued via a standard OAuth2 client-credentials
// flow; every incoming request must carry a valid, non-expired JWT signed
// with the configured HMAC-SHA256 secret (HS256) or RS256 public key.
package auth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/eliasndungu/finscore/internal/logging"
)

// Config holds the authentication configuration.
type Config struct {
	// JWTSecret is the HMAC-SHA256 key used to verify HS256 tokens.
	JWTSecret string
	// TokenEndpoint is the path at which the /token handler is mounted.
	TokenEndpoint string
}

// Claims is the JWT claims structure for FinScore B2B tokens.
type Claims struct {
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
	jwt.RegisteredClaims
}

// Middleware returns a Gin middleware that validates the Bearer token.
func Middleware(cfg Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := extractBearerToken(c.GetHeader("Authorization"))
		if err != nil {
			remoteAddr := c.ClientIP()
			logging.AuditAuthFailure(remoteAddr, err.Error())
			logging.Logger.Warn("auth rejected",
				zap.String("remote_addr", remoteAddr),
				zap.Error(err),
			)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":             "unauthorized",
				"error_description": err.Error(),
			})
			return
		}

		claims, err := validateJWT(token, cfg.JWTSecret)
		if err != nil {
			remoteAddr := c.ClientIP()
			logging.AuditAuthFailure(remoteAddr, err.Error())
			logging.Logger.Warn("jwt validation failed",
				zap.String("remote_addr", remoteAddr),
				zap.Error(err),
			)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":             "invalid_token",
				"error_description": err.Error(),
			})
			return
		}

		// Expose validated claims to downstream handlers.
		c.Set("client_id", claims.ClientID)
		c.Set("scope", claims.Scope)
		c.Next()
	}
}

// IssueToken is an HTTP handler that implements the OAuth2
// client_credentials grant.  Clients POST their client_id and
// client_secret; if valid a signed JWT is returned.
//
// In production replace the in-memory credential store with a database
// lookup and rotate the signing key via a secrets manager.
func IssueToken(cfg Config, clients map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			GrantType    string `form:"grant_type"    json:"grant_type"`
			ClientID     string `form:"client_id"     json:"client_id"`
			ClientSecret string `form:"client_secret" json:"client_secret"`
		}
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}

		if req.GrantType != "client_credentials" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_grant_type"})
			return
		}

		secret, ok := clients[req.ClientID]
		if !ok || secret != req.ClientSecret {
			logging.AuditAuthFailure(c.ClientIP(), "bad client credentials")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_client"})
			return
		}

		tokenStr, expiresIn, err := mintJWT(req.ClientID, "score:read", cfg.JWTSecret, time.Hour)
		if err != nil {
			logging.Logger.Error("failed to mint JWT", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"access_token": tokenStr,
			"token_type":   "Bearer",
			"expires_in":   int(expiresIn.Seconds()),
			"scope":        "score:read",
		})
	}
}

// --- helpers ---

func extractBearerToken(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errors.New("missing Authorization header")
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", errors.New("Authorization header must be 'Bearer <token>'")
	}
	return strings.TrimSpace(parts[1]), nil
}

func validateJWT(tokenStr, secret string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token is not valid")
	}
	return claims, nil
}

func mintJWT(clientID, scope, secret string, ttl time.Duration) (string, time.Duration, error) {
	now := time.Now()
	claims := Claims{
		ClientID: clientID,
		Scope:    scope,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   clientID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    "finscore-api",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	str, err := token.SignedString([]byte(secret))
	return str, ttl, err
}

// HashPII returns a one-way SHA-256 hex of a PII string (e.g. MSISDN) so
// that audit logs contain a pseudonymous identifier, as recommended by ODPC
// Data Protection Act 2019 guidance.
func HashPII(value string) string {
	h := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", h)
}
