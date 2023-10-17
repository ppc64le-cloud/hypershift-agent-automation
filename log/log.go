package log

import "go.uber.org/zap"

var (
	zapLogger, _ = zap.NewProduction()
	Logger       = zapLogger.Sugar()
)
