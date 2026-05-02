package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/eliasndungu/finscore/internal/auth"
	"github.com/eliasndungu/finscore/internal/logging"
)

func init() {
	gin.SetMode(gin.TestMode)
	logging.Init("development")
}

const testSecret = "test-jwt-secret-32-characters-ok"

func newRouter() *gin.Engine {
	cfg := auth.Config{JWTSecret: testSecret}
	clients := map[string]string{"test-client": "test-secret"}

	r := gin.New()
	r.POST("/oauth/token", auth.IssueToken(cfg, clients))
	r.GET("/protected", auth.Middleware(cfg), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// TestIssueToken_ValidCredentials checks that a valid client receives a JWT.
func TestIssueToken_ValidCredentials(t *testing.T) {
	r := newRouter()

	body, _ := json.Marshal(map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     "test-client",
		"client_secret": "test-secret",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if _, ok := resp["access_token"]; !ok {
		t.Errorf("access_token missing from response: %v", resp)
	}
	if resp["token_type"] != "Bearer" {
		t.Errorf("unexpected token_type: %v", resp["token_type"])
	}
}

// TestIssueToken_BadCredentials checks that wrong credentials return 401.
func TestIssueToken_BadCredentials(t *testing.T) {
	r := newRouter()

	body, _ := json.Marshal(map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     "test-client",
		"client_secret": "wrong",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// TestMiddleware_MissingToken checks that protected routes reject missing tokens.
func TestMiddleware_MissingToken(t *testing.T) {
	r := newRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// TestMiddleware_ValidToken checks that a valid JWT grants access.
func TestMiddleware_ValidToken(t *testing.T) {
	r := newRouter()

	// Mint a valid token using the internal helper by going through the token endpoint.
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     "test-client",
		"client_secret": "test-secret",
	})
	wt := httptest.NewRecorder()
	reqt := httptest.NewRequest(http.MethodPost, "/oauth/token", bytes.NewReader(body))
	reqt.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wt, reqt)

	var tokenResp map[string]interface{}
	json.Unmarshal(wt.Body.Bytes(), &tokenResp)
	token := tokenResp["access_token"].(string)

	// Now access the protected route.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestMiddleware_ExpiredToken checks that an expired JWT is rejected.
func TestMiddleware_ExpiredToken(t *testing.T) {
	r := newRouter()

	claims := auth.Claims{
		ClientID: "test-client",
		Scope:    "score:read",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "test-client",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			Issuer:    "finscore-api",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := tok.SignedString([]byte(testSecret))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d", w.Code)
	}
}

// TestHashPII ensures the same input always produces the same hash.
func TestHashPII(t *testing.T) {
	h1 := auth.HashPII("+254712345678")
	h2 := auth.HashPII("+254712345678")
	if h1 != h2 {
		t.Errorf("HashPII not deterministic: %s != %s", h1, h2)
	}
	if h1 == "+254712345678" {
		t.Error("HashPII returned the raw value — no hashing performed")
	}
}
