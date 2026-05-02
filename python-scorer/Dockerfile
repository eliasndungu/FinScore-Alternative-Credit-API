FROM python:3.12-slim

LABEL maintainer="FinScore <dev@finscore.africa>"
LABEL description="XGBoost credit-scoring gRPC microservice"

# Keeps Python from generating .pyc files and enables unbuffered stdout/stderr
# so logs appear immediately in container logs / ELK Filebeat.
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    GRPC_PORT=50051 \
    HTTP_PORT=8000 \
    LOG_LEVEL=INFO

WORKDIR /app

# Install dependencies first to leverage Docker layer caching.
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application source.
COPY . .

EXPOSE ${GRPC_PORT} ${HTTP_PORT}

# Run as a non-root user for container security best practices.
RUN adduser --disabled-password --gecos "" appuser && chown -R appuser /app
USER appuser

CMD ["python", "server.py"]
