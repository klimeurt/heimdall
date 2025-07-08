package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
)

type contextKey string

const (
	workerIDKey      contextKey = "worker_id"
	correlationIDKey contextKey = "correlation_id"
)

// Config holds the logging configuration
type Config struct {
	Level     string
	Format    string
	Output    string
	AddSource bool
	Service   string
	Version   string
}

// NewLogger creates a new slog logger with the given configuration
func NewLogger(cfg Config) *slog.Logger {
	// Parse log level
	level := parseLogLevel(cfg.Level)
	
	// Create handler options
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: cfg.AddSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize time format if needed
			if a.Key == slog.TimeKey {
				return a
			}
			return a
		},
	}
	
	// Get output writer
	output := getOutput(cfg.Output)
	
	// Create handler based on format
	var handler slog.Handler
	if strings.ToLower(cfg.Format) == "json" {
		handler = slog.NewJSONHandler(output, opts)
	} else {
		handler = slog.NewTextHandler(output, opts)
	}
	
	// Add default attributes
	attrs := []slog.Attr{
		slog.String("service", cfg.Service),
	}
	
	// Add version if available
	if cfg.Version != "" {
		attrs = append(attrs, slog.String("version", cfg.Version))
	} else if info, ok := debug.ReadBuildInfo(); ok {
		// Try to get version from build info
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				attrs = append(attrs, slog.String("version", setting.Value[:8])) // First 8 chars of commit
				break
			}
		}
	}
	
	handler = handler.WithAttrs(attrs)
	
	return slog.New(handler)
}

// parseLogLevel converts a string log level to slog.Level
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// getOutput returns the appropriate output writer based on configuration
func getOutput(output string) io.Writer {
	switch strings.ToLower(output) {
	case "stderr":
		return os.Stderr
	case "stdout", "":
		return os.Stdout
	default:
		// Assume it's a file path
		file, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			// Fall back to stdout if file cannot be opened
			fmt.Fprintf(os.Stderr, "Failed to open log file %s: %v, falling back to stdout\n", output, err)
			return os.Stdout
		}
		return file
	}
}

// WithWorker adds a worker ID to the context
func WithWorker(ctx context.Context, workerID int) context.Context {
	return context.WithValue(ctx, workerIDKey, workerID)
}

// WithCorrelationID adds a correlation ID to the context
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, correlationIDKey, correlationID)
}

// LoggerFromContext creates a logger with context values as attributes
func LoggerFromContext(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	
	var attrs []slog.Attr
	
	// Add worker ID if present
	if workerID, ok := ctx.Value(workerIDKey).(int); ok {
		attrs = append(attrs, slog.Int("worker_id", workerID))
	}
	
	// Add correlation ID if present
	if correlationID, ok := ctx.Value(correlationIDKey).(string); ok {
		attrs = append(attrs, slog.String("correlation_id", correlationID))
	}
	
	if len(attrs) > 0 {
		// Convert attrs to []any
		args := make([]any, len(attrs))
		for i, attr := range attrs {
			args[i] = attr
		}
		return logger.With(slog.Group("context", args...))
	}
	
	return logger
}

// DefaultConfig returns a default logging configuration
func DefaultConfig(service string) Config {
	return Config{
		Level:     getEnv("LOG_LEVEL", "info"),
		Format:    getEnv("LOG_FORMAT", "json"),
		Output:    getEnv("LOG_OUTPUT", "stdout"),
		AddSource: getEnvBool("LOG_ADD_SOURCE", false),
		Service:   service,
		Version:   getEnv("SERVICE_VERSION", ""),
	}
}

// Helper functions to read environment variables
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return strings.ToLower(value) == "true" || value == "1"
	}
	return defaultValue
}