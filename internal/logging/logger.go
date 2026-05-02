package logging

import (
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the package-level structured logger.
var Logger *zap.Logger

// AuditLogger is dedicated to ODPC audit trail events and always
// writes JSON to stdout so an ELK Filebeat/Logstash shipper can
// consume it without additional parsing.
var AuditLogger *zap.Logger

// Init initialises both loggers.  Call once from main().
// env should be "production" or "development".
func Init(env string) {
	encoderCfg := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     rfc3339TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	jsonEncoder := zapcore.NewJSONEncoder(encoderCfg)
	stdout := zapcore.Lock(os.Stdout)

	var level zapcore.Level
	if env == "production" {
		level = zapcore.InfoLevel
	} else {
		level = zapcore.DebugLevel
	}

	core := zapcore.NewCore(jsonEncoder, stdout, level)
	Logger = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	// Audit logger always emits INFO+ and never discards records.
	auditCore := zapcore.NewCore(jsonEncoder, stdout, zapcore.InfoLevel)
	AuditLogger = zap.New(auditCore, zap.AddCaller()).
		With(zap.String("log_type", "audit"))
}

// rfc3339TimeEncoder writes timestamps in RFC-3339 / ISO-8601 so that
// Elasticsearch can auto-detect and parse them without a custom ingest pipeline.
func rfc3339TimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.UTC().Format(time.RFC3339Nano))
}

// AuditScoreRequest records an incoming scoring request for the ODPC audit trail.
func AuditScoreRequest(requestID, msisdn, clientID, remoteAddr string) {
	AuditLogger.Info("score_request",
		zap.String("event", "score_request"),
		zap.String("request_id", requestID),
		zap.String("msisdn_hash", msisdn), // caller must hash PII before passing
		zap.String("client_id", clientID),
		zap.String("remote_addr", remoteAddr),
	)
}

// AuditScoreResponse records the result returned to the caller.
func AuditScoreResponse(requestID string, score float64, riskBand string) {
	AuditLogger.Info("score_response",
		zap.String("event", "score_response"),
		zap.String("request_id", requestID),
		zap.Float64("score", score),
		zap.String("risk_band", riskBand),
	)
}

// AuditAuthFailure records an authentication failure.
func AuditAuthFailure(remoteAddr, reason string) {
	AuditLogger.Warn("auth_failure",
		zap.String("event", "auth_failure"),
		zap.String("remote_addr", remoteAddr),
		zap.String("reason", reason),
	)
}
