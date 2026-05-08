package sqlite

import (
	"context"
	"time"

	"go.uber.org/zap"
	gormLibLogger "gorm.io/gorm/logger"
)

// GormLogger is a logger for GORM.
type gormLogger struct {
	// logger schemas.Logger
	logger zap.Logger
}

// LogMode sets the log mode for the logger.
func (l *gormLogger) LogMode(level gormLibLogger.LogLevel) gormLibLogger.Interface {
	// NOOP
	return l
}

// Info logs an info message.
func (l *gormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	l.logger.Sugar().Infof(msg, data...)
}

// Warn logs a warning message.
func (l *gormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	l.logger.Sugar().Warnf(msg, data...)
}

// Error logs an error message.
func (l *gormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	l.logger.Sugar().Errorf(msg, data...)
}

// Trace logs a trace message.
func (l *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	// NOOP
}

// newGormLogger creates a new GormLogger.
func newGormLogger(l zap.Logger) *gormLogger {
	return &gormLogger{logger: l}
}
