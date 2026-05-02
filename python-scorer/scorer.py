"""
scorer.py – XGBoost-backed credit scoring logic for the FinScore API.

Feature engineering
-------------------
The model ingests M-Pesa transaction history and phone-level metadata and
produces a creditworthiness probability in [0, 1].

Training data
~~~~~~~~~~~~~
In production, replace the *dummy* model instantiation below with a
``model = xgb.Booster(); model.load_model("model.ubj")`` call once you have
trained and persisted a real XGBoost model.

Features used
~~~~~~~~~~~~~
+--------------------------------+-------------------------------------------+
| Feature                        | Description                               |
+================================+===========================================+
| num_transactions               | Total number of transactions              |
| total_inflow                   | Sum of inbound amounts (KES)              |
| total_outflow                  | Sum of outbound amounts (KES)             |
| net_flow                       | total_inflow - total_outflow              |
| avg_transaction_amount         | Mean transaction amount                   |
| max_transaction_amount         | Maximum single transaction                |
| num_unique_counterparties      | Distinct counterparties contacted         |
| fuliza_count                   | Number of Fuliza (overdraft) usages       |
| bill_payment_ratio             | Bill payments / total transactions        |
| account_age_days               | Subscriber account age in days            |
+--------------------------------+-------------------------------------------+
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from typing import List

import numpy as np
import xgboost as xgb

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Data containers (mirror the proto messages)
# ---------------------------------------------------------------------------

@dataclass
class Transaction:
    transaction_id: str
    type: str          # e.g. SEND_MONEY, RECEIVE_MONEY, PAY_BILL, FULIZA …
    amount: float
    timestamp: str
    counterparty: str


@dataclass
class PhoneMetadata:
    msisdn: str
    account_age_days: int
    network_provider: str


@dataclass
class ScoreResult:
    score: float       # probability in [0, 1]
    risk_band: str     # LOW | MEDIUM | HIGH | VERY_HIGH
    explanation: str


# ---------------------------------------------------------------------------
# Feature engineering
# ---------------------------------------------------------------------------

_INBOUND_TYPES = {"RECEIVE_MONEY", "DEPOSIT"}
_OUTBOUND_TYPES = {"SEND_MONEY", "WITHDRAW", "BUY_AIRTIME", "PAY_BILL", "BUY_GOODS"}


def _extract_features(transactions: List[Transaction], meta: PhoneMetadata) -> np.ndarray:
    """Return a (1, n_features) numpy array for the model."""
    if not transactions:
        # Sparse profile: return zeros (will score very low).
        return np.zeros((1, 10), dtype=np.float32)

    amounts = np.array([t.amount for t in transactions], dtype=np.float64)
    total_inflow = sum(t.amount for t in transactions if t.type in _INBOUND_TYPES)
    total_outflow = sum(t.amount for t in transactions if t.type in _OUTBOUND_TYPES)
    net_flow = total_inflow - total_outflow
    fuliza_count = sum(1 for t in transactions if t.type == "FULIZA")
    bill_payments = sum(1 for t in transactions if t.type == "PAY_BILL")
    bill_payment_ratio = bill_payments / len(transactions)
    unique_counterparties = len({t.counterparty for t in transactions if t.counterparty})

    features = np.array([[
        len(transactions),
        total_inflow,
        total_outflow,
        net_flow,
        float(amounts.mean()),
        float(amounts.max()),
        unique_counterparties,
        fuliza_count,
        bill_payment_ratio,
        meta.account_age_days,
    ]], dtype=np.float32)

    return features


# ---------------------------------------------------------------------------
# Risk banding
# ---------------------------------------------------------------------------

def _band(score: float) -> str:
    if score >= 0.75:
        return "LOW"
    if score >= 0.50:
        return "MEDIUM"
    if score >= 0.25:
        return "HIGH"
    return "VERY_HIGH"


def _explain(score: float, features: np.ndarray) -> str:
    (
        num_txn, inflow, outflow, net, avg_amt, max_amt,
        n_counterparties, fuliza, bill_ratio, age
    ) = features[0].tolist()

    parts = [f"Score: {score:.3f}."]

    if net > 0:
        parts.append(f"Positive net cash flow (KES {net:,.0f}).")
    else:
        parts.append(f"Negative net cash flow (KES {net:,.0f}).")

    if fuliza > 0:
        parts.append(f"Fuliza overdraft used {int(fuliza)} time(s) — negative signal.")

    if age >= 365:
        parts.append(f"Long-standing account ({int(age)} days).")
    else:
        parts.append(f"Relatively new account ({int(age)} days).")

    return " ".join(parts)


# ---------------------------------------------------------------------------
# Model loader / scorer
# ---------------------------------------------------------------------------

class CreditScorer:
    """Loads (or builds a dummy) XGBoost model and scores applicants."""

    def __init__(self, model_path: str | None = None):
        if model_path:
            self._model = xgb.Booster()
            self._model.load_model(model_path)
            logger.info("Loaded XGBoost model from %s", model_path)
        else:
            logger.warning(
                "No model_path provided — using a dummy XGBoost model. "
                "Train and supply a real model for production use."
            )
            self._model = self._build_dummy_model()

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def score(self, transactions: List[Transaction], meta: PhoneMetadata) -> ScoreResult:
        features = _extract_features(transactions, meta)
        dmatrix = xgb.DMatrix(features)
        raw = self._model.predict(dmatrix)
        probability = float(np.clip(raw[0], 0.0, 1.0))
        band = _band(probability)
        explanation = _explain(probability, features)
        logger.debug("Scored msisdn_hash=%s score=%.4f band=%s",
                     meta.msisdn[:6] + "***", probability, band)
        return ScoreResult(score=probability, risk_band=band, explanation=explanation)

    # ------------------------------------------------------------------
    # Dummy model for smoke-testing without training data
    # ------------------------------------------------------------------

    @staticmethod
    def _build_dummy_model() -> xgb.Booster:
        """
        Train a trivial XGBoost model on synthetic data so the service
        starts up cleanly even without a persisted model file.
        """
        rng = np.random.default_rng(42)
        X = rng.random((200, 10)).astype(np.float32)
        # Simple rule: high net-flow (feature index 3) → creditworthy.
        y = (X[:, 3] > 0.5).astype(np.float32)
        dtrain = xgb.DMatrix(X, label=y)
        params = {
            "objective": "binary:logistic",
            "eval_metric": "logloss",
            "max_depth": 3,
            "eta": 0.1,
            "seed": 42,
        }
        booster = xgb.train(params, dtrain, num_boost_round=20, verbose_eval=False)
        return booster
