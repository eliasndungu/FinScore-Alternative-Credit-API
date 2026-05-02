"""
server.py – gRPC server for the FinScore Python scoring microservice.

Exposes the ScoringService defined in proto/scoring/scoring.proto and
delegates to the XGBoost-backed CreditScorer.

Also optionally starts a lightweight FastAPI HTTP health-check endpoint on a
separate thread so that Kubernetes readiness / liveness probes can verify
the service is up without a gRPC client.

Environment variables
~~~~~~~~~~~~~~~~~~~~~
GRPC_PORT      Port for the gRPC server         (default: 50051)
HTTP_PORT      Port for the FastAPI health check (default: 8000)
MODEL_PATH     Path to a serialised XGBoost model file (optional)
LOG_LEVEL      Python logging level              (default: INFO)
"""

from __future__ import annotations

import logging
import os
import signal
import sys
import threading
from concurrent import futures

import grpc
import uvicorn
from fastapi import FastAPI

from scorer import CreditScorer, Transaction, PhoneMetadata as ScorerPhoneMetadata
from scoring import scoring_pb2, scoring_pb2_grpc

# ---------------------------------------------------------------------------
# Logging – JSON-friendly structured output for ELK / Filebeat ingestion
# ---------------------------------------------------------------------------

logging.basicConfig(
    level=os.getenv("LOG_LEVEL", "INFO").upper(),
    format='{"timestamp":"%(asctime)s","level":"%(levelname)s","logger":"%(name)s","message":"%(message)s"}',
    datefmt="%Y-%m-%dT%H:%M:%SZ",
    stream=sys.stdout,
)
logger = logging.getLogger("finscore.scorer")

# ---------------------------------------------------------------------------
# gRPC service implementation
# ---------------------------------------------------------------------------

class ScoringServicer(scoring_pb2_grpc.ScoringServiceServicer):
    def __init__(self, scorer: CreditScorer):
        self._scorer = scorer

    def ComputeScore(
        self,
        request: scoring_pb2.ScoreRequest,
        context: grpc.ServicerContext,
    ) -> scoring_pb2.ScoreResponse:
        logger.info(
            "ComputeScore called: num_transactions=%d msisdn_prefix=%s",
            len(request.transactions),
            request.phone_metadata.msisdn[:6] if request.phone_metadata.msisdn else "N/A",
        )

        transactions = [
            Transaction(
                transaction_id=t.transaction_id,
                type=t.type,
                amount=t.amount,
                timestamp=t.timestamp,
                counterparty=t.counterparty,
            )
            for t in request.transactions
        ]

        meta = ScorerPhoneMetadata(
            msisdn=request.phone_metadata.msisdn,
            account_age_days=request.phone_metadata.account_age_days,
            network_provider=request.phone_metadata.network_provider,
        )

        try:
            result = self._scorer.score(transactions, meta)
        except Exception as exc:  # pylint: disable=broad-except
            logger.exception("Scoring failed: %s", exc)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Internal scoring error: {exc}")
            return scoring_pb2.ScoreResponse()

        logger.info(
            "ComputeScore result: score=%.4f risk_band=%s",
            result.score,
            result.risk_band,
        )
        return scoring_pb2.ScoreResponse(
            score=result.score,
            risk_band=result.risk_band,
            explanation=result.explanation,
        )


# ---------------------------------------------------------------------------
# FastAPI health check
# ---------------------------------------------------------------------------

app = FastAPI(title="FinScore Scorer", docs_url=None, redoc_url=None)


@app.get("/health")
def health() -> dict:
    return {"status": "ok", "service": "finscore-scorer"}


def _start_http_server(port: int) -> None:
    """Run FastAPI/uvicorn in a daemon thread."""
    uvicorn.run(app, host="0.0.0.0", port=port, log_level="warning")


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def serve() -> None:
    grpc_port = int(os.getenv("GRPC_PORT", "50051"))
    http_port = int(os.getenv("HTTP_PORT", "8000"))
    model_path = os.getenv("MODEL_PATH")  # None → use dummy model

    scorer = CreditScorer(model_path=model_path)

    # Start FastAPI health-check in a background thread.
    http_thread = threading.Thread(
        target=_start_http_server,
        args=(http_port,),
        daemon=True,
        name="http-health",
    )
    http_thread.start()
    logger.info("FastAPI health server started on port %d", http_port)

    # Start gRPC server.
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    scoring_pb2_grpc.add_ScoringServiceServicer_to_server(
        ScoringServicer(scorer), server
    )
    server.add_insecure_port(f"[::]:{grpc_port}")
    server.start()
    logger.info("gRPC ScoringService listening on port %d", grpc_port)

    # Graceful shutdown on SIGTERM / SIGINT.
    def _handle_signal(signum, _frame):
        logger.info("Received signal %d — shutting down…", signum)
        server.stop(grace=5)

    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)
    server.wait_for_termination()
    logger.info("gRPC server stopped")


if __name__ == "__main__":
    serve()
