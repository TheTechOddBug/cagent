package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/docker/docker-agent/pkg/desktop"
)

// telemetryLogger wraps slog.Logger to automatically prepend "[Telemetry]" to all messages
type telemetryLogger struct {
	logger *slog.Logger
}

// NewTelemetryLogger creates a new telemetry logger that automatically prepends "[Telemetry]" to all messages
func NewTelemetryLogger(logger *slog.Logger) *telemetryLogger {
	return &telemetryLogger{logger: logger}
}

// sanitizeLogValue replaces newline and carriage-return characters in s to
// prevent log-injection when the value is forwarded to a text-format sink.
func sanitizeLogValue(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// sanitizeLogArgs replaces newline characters in any string values within
// the args slice to prevent log injection through structured fields.
// Named string types (e.g. EventType) are handled via reflect so they are
// also sanitized without requiring an explicit type assertion per type.
func sanitizeLogArgs(args []any) []any {
	if len(args) == 0 {
		return args
	}
	result := make([]any, len(args))
	for i, v := range args {
		if s, ok := v.(string); ok {
			result[i] = sanitizeLogValue(s)
		} else if rv := reflect.ValueOf(v); rv.IsValid() && rv.Kind() == reflect.String {
			result[i] = sanitizeLogValue(rv.String())
		} else {
			result[i] = v
		}
	}
	return result
}

// Debug logs a debug message with "[Telemetry]" prefix
func (tl *telemetryLogger) Debug(msg string, args ...any) {
	tl.logger.Debug("[Telemetry]", append([]any{"telemetry_msg", sanitizeLogValue(msg)}, sanitizeLogArgs(args)...)...)
}

// Info logs an info message with "[Telemetry]" prefix
func (tl *telemetryLogger) Info(msg string, args ...any) {
	tl.logger.Info("[Telemetry]", append([]any{"telemetry_msg", sanitizeLogValue(msg)}, sanitizeLogArgs(args)...)...)
}

// Warn logs a warning message with "[Telemetry]" prefix
func (tl *telemetryLogger) Warn(msg string, args ...any) {
	tl.logger.Warn("[Telemetry]", append([]any{"telemetry_msg", sanitizeLogValue(msg)}, sanitizeLogArgs(args)...)...)
}

// Error logs an error message with "[Telemetry]" prefix
func (tl *telemetryLogger) Error(msg string, args ...any) {
	tl.logger.Error("[Telemetry]", append([]any{"telemetry_msg", sanitizeLogValue(msg)}, sanitizeLogArgs(args)...)...)
}

// Enabled returns whether the logger is enabled for the given level
func (tl *telemetryLogger) Enabled(ctx context.Context, level slog.Level) bool {
	return tl.logger.Enabled(ctx, level)
}

func newClient(ctx context.Context, logger *slog.Logger, enabled, debugMode bool, version string, customHTTPClient ...*http.Client) *Client {
	telemetryLogger := NewTelemetryLogger(logger)

	if !enabled {
		return &Client{
			logger:    telemetryLogger,
			enabled:   false,
			debugMode: debugMode,
			version:   version,
		}
	}

	header := "x-api-key"

	endpoint := "https://api.docker.com/events/v1/track"
	apiKey := "Gxw1IjiDEP29dWm9DanuE2XhIKKzqDEY4iGlW1P0"

	// Use staging configuration in debug mode
	if debugMode {
		endpoint = "https://api-stage.docker.com/events/v1/track"
		apiKey = "z4sTQ8eDid2nJ53md8ptCaZlVxvIlhvf4AGR7oi5"
	}

	var httpClient *http.Client
	if len(customHTTPClient) > 0 && customHTTPClient[0] != nil {
		httpClient = customHTTPClient[0]
	} else {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	client := &Client{
		logger:      telemetryLogger,
		userUUID:    getUserUUID(),
		desktopUUID: desktop.GetUUID(ctx),
		enabled:     enabled,
		debugMode:   debugMode,
		httpClient:  httpClient,
		endpoint:    endpoint,
		apiKey:      apiKey,
		header:      header,
		version:     version,
	}

	telemetryLogger.Debug("Enabled:", enabled)

	return client
}
