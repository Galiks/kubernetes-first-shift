package helmtest

import "log/slog"

func logInfo(msg string, attrs map[string]any) {
	a := make([]any, 0, len(attrs)*2)
	for k, v := range attrs {
		a = append(a, k, v)
	}
	slog.Info(msg, a...)
}

func logError(msg string, attrs map[string]any) {
	a := make([]any, 0, len(attrs)*2)
	for k, v := range attrs {
		a = append(a, k, v)
	}
	slog.Error(msg, a...)
}
