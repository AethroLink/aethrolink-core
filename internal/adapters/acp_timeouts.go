package adapters

import "time"

const (
	defaultACPInitializeTimeout   = 30 * time.Second
	defaultACPSessionSetupTimeout = 90 * time.Second
)

func acpInitializeTimeout(runtimeOptions map[string]any) time.Duration {
	return durationOption(runtimeOptions, "initialize_timeout_ms", defaultACPInitializeTimeout)
}

func acpSessionSetupTimeout(runtimeOptions map[string]any) time.Duration {
	return durationOption(runtimeOptions, "session_setup_timeout_ms", defaultACPSessionSetupTimeout)
}

func durationOption(runtimeOptions map[string]any, key string, fallback time.Duration) time.Duration {
	if runtimeOptions == nil {
		return fallback
	}
	raw, ok := runtimeOptions[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case int:
		if value > 0 {
			return time.Duration(value) * time.Millisecond
		}
	case int64:
		if value > 0 {
			return time.Duration(value) * time.Millisecond
		}
	case float64:
		if value > 0 {
			return time.Duration(value) * time.Millisecond
		}
	}
	return fallback
}
