// Package grpcclient provides a thin client wrapper around the gRPC
// ScoringService stub so that handlers do not need to deal with
// connection management or context plumbing.
package grpcclient

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/eliasndungu/finscore/internal/logging"
	pb "github.com/eliasndungu/finscore/proto/scoring"
)

// Client wraps the generated gRPC stub and manages the connection lifecycle.
type Client struct {
	conn   *grpc.ClientConn
	stub   pb.ScoringServiceClient
	timeout time.Duration
}

// New creates a gRPC client connected to addr (e.g. "localhost:50051").
// The connection uses insecure transport by default; replace with TLS in
// production by swapping grpc.WithTransportCredentials.
func New(addr string, timeout time.Duration) (*Client, error) {
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", addr, err)
	}

	logging.Logger.Info("gRPC connection established", zap.String("addr", addr))
	return &Client{
		conn:    conn,
		stub:    pb.NewScoringServiceClient(conn),
		timeout: timeout,
	}, nil
}

// ComputeScore forwards the scoring request to the Python microservice.
func (c *Client) ComputeScore(ctx context.Context, req *pb.ScoreRequest) (*pb.ScoreResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()
	resp, err := c.stub.ComputeScore(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		logging.Logger.Error("gRPC ComputeScore failed",
			zap.Duration("elapsed_ms", elapsed),
			zap.Error(err),
		)
		return nil, fmt.Errorf("scoring service error: %w", err)
	}

	logging.Logger.Debug("gRPC ComputeScore ok",
		zap.Duration("elapsed_ms", elapsed),
		zap.Float64("score", resp.Score),
	)
	return resp, nil
}

// Close releases the underlying connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
