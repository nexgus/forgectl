//go:build !darwin

package selfinstall

// clearQuarantine 在非 macOS 平台為 no-op (僅 macOS 有 com.apple.quarantine 隔離機制).
func clearQuarantine(string) error { return nil }
