package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		wantJSON bool
		wantAttr map[string]string
	}{
		{
			name: "JSON format with service name",
			config: Config{
				Level:   "info",
				Format:  "json",
				Service: "test-service",
				Version: "v1.0.0",
			},
			wantJSON: true,
			wantAttr: map[string]string{
				"service": "test-service",
				"version": "v1.0.0",
			},
		},
		{
			name: "Text format with debug level",
			config: Config{
				Level:   "debug",
				Format:  "text",
				Service: "test-service",
			},
			wantJSON: false,
			wantAttr: map[string]string{
				"service": "test-service",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.config.Output = "stdout" // Will be overridden in test
			
			logger := NewLogger(tt.config)
			
			// Override handler to write to buffer
			var handler slog.Handler
			opts := &slog.HandlerOptions{Level: parseLogLevel(tt.config.Level)}
			
			if tt.wantJSON {
				handler = slog.NewJSONHandler(&buf, opts)
			} else {
				handler = slog.NewTextHandler(&buf, opts)
			}
			
			// Add expected attributes
			var attrs []slog.Attr
			for k, v := range tt.wantAttr {
				attrs = append(attrs, slog.String(k, v))
			}
			handler = handler.WithAttrs(attrs)
			
			logger = slog.New(handler)
			logger.Info("test message", slog.String("key", "value"))
			
			output := buf.String()
			
			if tt.wantJSON {
				var logEntry map[string]interface{}
				if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
					t.Fatalf("Failed to parse JSON log: %v", err)
				}
				
				if msg, ok := logEntry["msg"].(string); !ok || msg != "test message" {
					t.Errorf("Expected message 'test message', got %v", logEntry["msg"])
				}
				
				for k, v := range tt.wantAttr {
					if logEntry[k] != v {
						t.Errorf("Expected %s=%s, got %v", k, v, logEntry[k])
					}
				}
			} else {
				if !strings.Contains(output, "test message") {
					t.Errorf("Expected output to contain 'test message'")
				}
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"invalid", slog.LevelInfo}, // Default
		{"", slog.LevelInfo},         // Default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLogLevel(tt.input)
			if got != tt.expected {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestContextFunctions(t *testing.T) {
	ctx := context.Background()
	
	// Test WithWorker
	ctx = WithWorker(ctx, 42)
	if id, ok := ctx.Value(workerIDKey).(int); !ok || id != 42 {
		t.Errorf("Expected worker ID 42, got %v", ctx.Value(workerIDKey))
	}
	
	// Test WithCorrelationID
	ctx = WithCorrelationID(ctx, "test-correlation-123")
	if id, ok := ctx.Value(correlationIDKey).(string); !ok || id != "test-correlation-123" {
		t.Errorf("Expected correlation ID 'test-correlation-123', got %v", ctx.Value(correlationIDKey))
	}
	
	// Test LoggerFromContext
	var buf bytes.Buffer
	baseLogger := slog.New(slog.NewJSONHandler(&buf, nil))
	
	contextLogger := LoggerFromContext(ctx, baseLogger)
	contextLogger.Info("test with context")
	
	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON log: %v", err)
	}
	
	// Check if context attributes are present
	if context, ok := logEntry["context"].(map[string]interface{}); ok {
		if context["worker_id"] != float64(42) {
			t.Errorf("Expected worker_id 42, got %v", context["worker_id"])
		}
		if context["correlation_id"] != "test-correlation-123" {
			t.Errorf("Expected correlation_id 'test-correlation-123', got %v", context["correlation_id"])
		}
	} else {
		t.Errorf("Expected context group in log entry")
	}
}

func TestDefaultConfig(t *testing.T) {
	// Test with no environment variables
	cfg := DefaultConfig("test-service")
	
	if cfg.Service != "test-service" {
		t.Errorf("Expected service 'test-service', got %s", cfg.Service)
	}
	if cfg.Level != "info" {
		t.Errorf("Expected default level 'info', got %s", cfg.Level)
	}
	if cfg.Format != "json" {
		t.Errorf("Expected default format 'json', got %s", cfg.Format)
	}
	if cfg.Output != "stdout" {
		t.Errorf("Expected default output 'stdout', got %s", cfg.Output)
	}
	if cfg.AddSource != false {
		t.Errorf("Expected AddSource false by default")
	}
}