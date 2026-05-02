// Package handlers contains the HTTP request handlers for the FinScore API.
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/eliasndungu/finscore/internal/auth"
	"github.com/eliasndungu/finscore/internal/logging"
	pb "github.com/eliasndungu/finscore/proto/scoring"
)

// ScoringClient is the interface the handler uses to communicate with the
// gRPC scoring microservice.  Accepting an interface (rather than the
// concrete grpcclient.Client) makes the handler easy to test with stubs.
type ScoringClient interface {
	ComputeScore(ctx context.Context, req *pb.ScoreRequest) (*pb.ScoreResponse, error)
}

// ScoreHandler holds the dependencies for the /v1/score endpoint.
type ScoreHandler struct {
	grpc ScoringClient
}

// NewScoreHandler constructs a ScoreHandler.
func NewScoreHandler(grpc ScoringClient) *ScoreHandler {
	return &ScoreHandler{grpc: grpc}
}

// ---- request / response DTOs ----

// MpesaTransactionDTO mirrors the proto MpesaTransaction for JSON binding.
type MpesaTransactionDTO struct {
	TransactionID string  `json:"transaction_id" binding:"required"`
	Type          string  `json:"type"           binding:"required"`
	Amount        float64 `json:"amount"         binding:"required,gt=0"`
	Timestamp     string  `json:"timestamp"      binding:"required"`
	Counterparty  string  `json:"counterparty"`
}

// PhoneMetadataDTO mirrors the proto PhoneMetadata for JSON binding.
type PhoneMetadataDTO struct {
	MSISDN           string `json:"msisdn"             binding:"required"`
	AccountAgeDays   int32  `json:"account_age_days"   binding:"required,gte=0"`
	NetworkProvider  string `json:"network_provider"   binding:"required"`
}

// ScoreRequestDTO is the JSON body expected at POST /v1/score.
type ScoreRequestDTO struct {
	Transactions  []MpesaTransactionDTO `json:"transactions"   binding:"required,min=1"`
	PhoneMetadata PhoneMetadataDTO      `json:"phone_metadata" binding:"required"`
}

// ScoreResponseDTO is the JSON body returned by POST /v1/score.
type ScoreResponseDTO struct {
	RequestID   string  `json:"request_id"`
	Score       float64 `json:"score"`
	RiskBand    string  `json:"risk_band"`
	Explanation string  `json:"explanation"`
}

// Score handles POST /v1/score.
//
// @Summary     Compute alternative credit score
// @Description Accepts M-Pesa transaction history and phone metadata and
//              returns an XGBoost-derived credit score and risk band.
// @Tags        scoring
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body ScoreRequestDTO true "Scoring payload"
// @Success     200  {object} ScoreResponseDTO
// @Failure     400  {object} map[string]string
// @Failure     401  {object} map[string]string
// @Failure     502  {object} map[string]string
// @Router      /v1/score [post]
func (h *ScoreHandler) Score(c *gin.Context) {
	requestID := uuid.New().String()

	var req ScoreRequestDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		logging.Logger.Warn("invalid score request",
			zap.String("request_id", requestID),
			zap.Error(err),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "invalid_request",
			"details":    err.Error(),
			"request_id": requestID,
		})
		return
	}

	// Emit ODPC audit trail entry.  MSISDN is hashed to pseudonymise PII.
	clientID, _ := c.Get("client_id")
	logging.AuditScoreRequest(
		requestID,
		auth.HashPII(req.PhoneMetadata.MSISDN),
		clientID.(string),
		c.ClientIP(),
	)

	// Map DTO → proto.
	pbReq := toProtoRequest(&req)

	resp, err := h.grpc.ComputeScore(c.Request.Context(), pbReq)
	if err != nil {
		logging.Logger.Error("scoring service unavailable",
			zap.String("request_id", requestID),
			zap.Error(err),
		)
		c.JSON(http.StatusBadGateway, gin.H{
			"error":      "scoring_service_unavailable",
			"request_id": requestID,
		})
		return
	}

	// Emit response audit log.
	logging.AuditScoreResponse(requestID, resp.Score, resp.RiskBand)

	c.JSON(http.StatusOK, ScoreResponseDTO{
		RequestID:   requestID,
		Score:       resp.Score,
		RiskBand:    resp.RiskBand,
		Explanation: resp.Explanation,
	})
}

// HealthCheck handles GET /health.
func HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// toProtoRequest converts the validated DTO into the gRPC proto message.
func toProtoRequest(dto *ScoreRequestDTO) *pb.ScoreRequest {
	txns := make([]*pb.MpesaTransaction, len(dto.Transactions))
	for i, t := range dto.Transactions {
		txns[i] = &pb.MpesaTransaction{
			TransactionId: t.TransactionID,
			Type:          t.Type,
			Amount:        t.Amount,
			Timestamp:     t.Timestamp,
			Counterparty:  t.Counterparty,
		}
	}
	return &pb.ScoreRequest{
		Transactions: txns,
		PhoneMetadata: &pb.PhoneMetadata{
			Msisdn:           dto.PhoneMetadata.MSISDN,
			AccountAgeDays:   dto.PhoneMetadata.AccountAgeDays,
			NetworkProvider:  dto.PhoneMetadata.NetworkProvider,
		},
	}
}
