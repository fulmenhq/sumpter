package logging

import "testing"

// TestGetLoggerNilSafe pins the nil-safety contract: GetLogger never returns nil, so
// callers can invoke methods on the result (GetLogger().Info(...) or
// `l := GetLogger(); l.Warn(...)`) without a nil check even when no global logger has
// been configured (the unconfigured/test default).
func TestGetLoggerNilSafe(t *testing.T) {
	saved := globalLogger
	t.Cleanup(func() { globalLogger = saved })

	globalLogger = nil
	l := GetLogger()
	if l == nil {
		t.Fatal("GetLogger() returned nil with no global logger configured; want a no-op logger")
	}
	// Calling methods on the no-op logger must not panic.
	l.Info("no-op")
	l.Warn("no-op")
	l.Error("no-op")
}
