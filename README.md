# FinScore — Alternative Credit Scoring API

A high-performance, secure B2B REST API for alternative credit scoring in Kenya,
built to comply with the **Kenya Data Protection Act 2019** and the guidance of
the **Office of the Data Protection Commissioner (ODPC)**.

---

## Architecture overview

```
                ┌─────────────────────────────────────────────────┐
                │              Client / Partner Bank               │
                └────────────────────┬────────────────────────────┘
                                     │ HTTPS + Bearer JWT (OAuth2)
                ┌────────────────────▼────────────────────────────┐
                │           Go REST API Gateway (Gin)             │
                │  POST /oauth/token   →  issues JWT              │
                │  POST /v1/score      →  requires valid JWT      │
                │  GET  /health        →  liveness probe          │
                │                                                 │
                │  ┌─── Structured JSON Logging (zap) ────────┐  │
                │  │  Audit events tagged log_type=audit       │  │
                │  └───────────────────────────────────────────┘  │
                └────────────────────┬────────────────────────────┘
                                     │ gRPC (proto/scoring/scoring.proto)
                ┌────────────────────▼────────────────────────────┐
                │        Python Microservice (gRPC server)        │
                │  ┌── FastAPI health check (/health) ─────────┐  │
                │  └────────────────────────────────────────────┘  │
                │  XGBoost model → credit score + risk band       │
                └─────────────────────────────────────────────────┘
                                     │
                         stdout (JSON) │
                ┌────────────────────▼────────────────────────────┐
                │  ELK Stack  (Elasticsearch + Logstash + Kibana)  │
                │  finscore-logs-*      general application logs   │
                │  finscore-audit-*     ODPC audit trail           │
                └─────────────────────────────────────────────────┘
```

---

## Project structure

```
.
├── cmd/api/main.go                  # Go API entry point
├── internal/
│   ├── auth/middleware.go           # OAuth2 / JWT middleware & token issuer
│   ├── grpc/client.go               # gRPC client to Python scorer
│   ├── handlers/score.go            # POST /v1/score handler
│   └── logging/logger.go            # Structured JSON logging (zap / ELK)
├── proto/scoring/
│   ├── scoring.proto                # gRPC service & message definitions ★
│   ├── scoring.pb.go                # Generated Go protobuf types
│   └── scoring_grpc.pb.go           # Generated Go gRPC stubs
├── python-scorer/
│   ├── scorer.py                    # XGBoost feature engineering & model
│   ├── server.py                    # gRPC server + FastAPI health check
│   ├── scoring/                     # Generated Python protobuf stubs
│   ├── requirements.txt
│   └── Dockerfile
├── elk/logstash.conf                # Logstash pipeline (ELK audit trail)
├── Dockerfile.api                   # Multi-stage Docker image for Go API
├── docker-compose.yml               # Full stack (API + scorer + ELK)
├── Makefile
└── go.mod
```

---

## Quick start

### Prerequisites

| Tool          | Version   |
|---------------|-----------|
| Go            | ≥ 1.22    |
| Python        | ≥ 3.11    |
| Docker        | ≥ 24      |
| Docker Compose| ≥ 2.x     |
| protoc        | ≥ 3.21    |

### Run locally with Docker Compose

```bash
# 1. Clone and enter the repo
git clone https://github.com/eliasndungu/FinScore-Alternative-Credit-API.git
cd FinScore-Alternative-Credit-API

# 2. Set required secrets (or export them in your shell)
export JWT_SECRET="replace-with-a-strong-secret"
export OAUTH_CLIENT_ID="your-client-id"
export OAUTH_CLIENT_SECRET="your-client-secret"

# 3. Start everything
docker compose up --build -d

# 4. Obtain an OAuth2 access token
curl -s -X POST http://localhost:8080/oauth/token \
  -H "Content-Type: application/json" \
  -d '{"grant_type":"client_credentials","client_id":"your-client-id","client_secret":"your-client-secret"}' \
  | jq .

# 5. Call the /v1/score endpoint
TOKEN="<access_token from step 4>"
curl -s -X POST http://localhost:8080/v1/score \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "transactions": [
      {
        "transaction_id": "TXN001",
        "type": "RECEIVE_MONEY",
        "amount": 15000,
        "timestamp": "2024-06-01T08:00:00Z",
        "counterparty": "+254700000001"
      },
      {
        "transaction_id": "TXN002",
        "type": "PAY_BILL",
        "amount": 3500,
        "timestamp": "2024-06-05T09:30:00Z",
        "counterparty": "888880"
      }
    ],
    "phone_metadata": {
      "msisdn": "+254712345678",
      "account_age_days": 730,
      "network_provider": "Safaricom"
    }
  }' | jq .
```

### Example response

```json
{
  "request_id": "f3a2e1d0-...",
  "score": 0.8412,
  "risk_band": "LOW",
  "explanation": "Score: 0.841. Positive net cash flow (KES 11,500). Long-standing account (730 days)."
}
```

Risk bands:

| Band      | Score range | Interpretation        |
|-----------|-------------|------------------------|
| LOW       | ≥ 0.75      | High creditworthiness  |
| MEDIUM    | 0.50 – 0.75 | Moderate risk          |
| HIGH      | 0.25 – 0.50 | Elevated risk          |
| VERY_HIGH | < 0.25      | Very high risk         |

---

## Development

### Build & test (Go)

```bash
# Build the API binary
make build

# Run tests
go test -race ./...

# Regenerate protobuf stubs after editing scoring.proto
make proto
```

### Run the Python scorer locally

```bash
cd python-scorer
pip install -r requirements.txt
python server.py
```

The scorer listens on:
- `localhost:50051` (gRPC)
- `localhost:8000` (FastAPI health check at `/health`)

### Environment variables

#### Go API

| Variable            | Default            | Description                               |
|---------------------|--------------------|-------------------------------------------|
| `APP_ENV`           | `development`      | `production` enables Gin release mode     |
| `JWT_SECRET`        | *required*         | HMAC-SHA256 key for signing JWTs          |
| `OAUTH_CLIENT_ID`   | `finscore-demo`    | Demo OAuth2 client ID                     |
| `OAUTH_CLIENT_SECRET` | `change-me`      | Demo OAuth2 client secret                 |
| `SCORER_ADDR`       | `localhost:50051`  | gRPC address of the Python scorer         |
| `GRPC_TIMEOUT`      | `10s`              | Per-request gRPC call timeout             |
| `HTTP_ADDR`         | `:8080`            | HTTP listen address                       |

#### Python scorer

| Variable     | Default   | Description                                            |
|--------------|-----------|--------------------------------------------------------|
| `GRPC_PORT`  | `50051`   | gRPC server port                                       |
| `HTTP_PORT`  | `8000`    | FastAPI health-check port                              |
| `MODEL_PATH` | *(unset)* | Path to a persisted XGBoost `.ubj` model file          |
| `LOG_LEVEL`  | `INFO`    | Python logging level                                   |

---

## gRPC protocol definition

See [`proto/scoring/scoring.proto`](proto/scoring/scoring.proto).

Key messages:

```protobuf
message ScoreRequest {
  repeated MpesaTransaction transactions = 1;
  PhoneMetadata             phone_metadata = 2;
}

message ScoreResponse {
  double score       = 1;  // probability in [0, 1]
  string risk_band   = 2;  // LOW | MEDIUM | HIGH | VERY_HIGH
  string explanation = 3;
}
```

---

## Logging & ODPC compliance

All services emit **structured JSON** logs to stdout, consumed by the ELK stack:

- **General logs** → `finscore-logs-*` Elasticsearch index.
- **Audit events** (`log_type: audit`) → additionally routed to the
  `finscore-audit-*` index for long-term retention (ODPC requires ≥ 5 years).

MSISDN and other PII values are **SHA-256 hashed** before they appear in any
audit log, satisfying Data Protection Act 2019 pseudonymisation requirements.

Access Kibana at [http://localhost:5601](http://localhost:5601) to visualise
audit dashboards.

---

## Security notes

- **JWT_SECRET** must be a random string of ≥ 32 characters. Rotate regularly.
- In production, replace the in-memory client store in `auth.IssueToken` with
  a database-backed lookup.
- Replace `insecure.NewCredentials()` in `internal/grpc/client.go` with mutual
  TLS when the scorer runs in a different network segment.
- The `Dockerfile.api` uses a `scratch` base image and runs as a non-root user
  to minimise the attack surface.

---

## License

MIT © FinScore Africa
