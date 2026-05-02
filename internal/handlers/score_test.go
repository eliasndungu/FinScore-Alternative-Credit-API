package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/eliasndungu/finscore/internal/handlers"
	"github.com/eliasndungu/finscore/internal/logging"
	pb "github.com/eliasndungu/finscore/proto/scoring"
)

func init() {
	gin.SetMode(gin.TestMode)
	logging.Init("development")
}

// stubGRPC is a test double for the gRPC client.
type stubGRPC struct {
	score     float64
	riskBand  string
	returnErr error
}

func (s *stubGRPC) ComputeScore(_ context.Context, _ *pb.ScoreRequest) (*pb.ScoreResponse, error) {
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return &pb.ScoreResponse{
		Score:       s.score,
		RiskBand:    s.riskBand,
		Explanation: "test explanation",
	}, nil
}

// scorerInterface matches the method used by ScoreHandler so we can inject a stub.
// (We define an identical interface here rather than exporting one from handlers,
// keeping the handler package free of test-only abstractions.)
type scorerInterface interface {
	ComputeScore(context.Context, *pb.ScoreRequest) (*pb.ScoreResponse, error)
}

func TestHealthCheck(t *testing.T) {
	r := gin.New()
	r.GET("/health", handlers.HealthCheck)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("unexpected health body: %v", body)
	}
}

// validScorePayload returns a minimal valid /v1/score JSON payload.
func validScorePayload() map[string]interface{} {
	return map[string]interface{}{
		"transactions": []map[string]interface{}{
			{
				"transaction_id": "TXN001",
				"type":           "RECEIVE_MONEY",
				"amount":         5000.0,
				"timestamp":      "2024-01-15T10:30:00Z",
				"counterparty":   "+254700000000",
			},
		},
		"phone_metadata": map[string]interface{}{
			"msisdn":            "+254712345678",
			"account_age_days":  730,
			"network_provider":  "Safaricom",
		},
	}
}

func TestScore_InvalidJSON(t *testing.T) {
	r := gin.New()
	// Inject a fake client_id so Middleware is bypassed in unit test.
	r.Use(func(c *gin.Context) { c.Set("client_id", "test"); c.Next() })
	r.POST("/v1/score", handlers.NewScoreHandler(nil).Score)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/score", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestScore_MissingRequiredFields(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("client_id", "test"); c.Next() })
	r.POST("/v1/score", handlers.NewScoreHandler(nil).Score)

	// Empty transactions array should fail binding.
	payload := map[string]interface{}{
		"transactions": []interface{}{},
		"phone_metadata": map[string]interface{}{
			"msisdn": "+254712345678", "account_age_days": 100, "network_provider": "Safaricom",
		},
	}
	body, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/score", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty transactions, got %d", w.Code)
	}
}
