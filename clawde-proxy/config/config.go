// Package config parses and exposes all CLAWDE_* and relevant NSELF_* environment variables.
// Purpose: Centralise env-var parsing for clawde-proxy; apply defaults per spec §11.
// Inputs:  Environment variables at process startup.
// Outputs: ClawdeConfig struct with all settings; loaded via New() at startup.
// Constraints: All defaults match spec §11 exactly. No panic on missing optional vars.
// SPORT: F08-SERVICE-INVENTORY.md clawde-proxy row
package config

import (
	"os"
	"strconv"
)

// ClawdeConfig holds all runtime configuration for clawde-proxy.
// Fields map 1:1 to CLAWDE_* and NSELF_* env vars documented in spec §11.
type ClawdeConfig struct {
	// ProxyPort is the TCP port clawde-proxy listens on. Default: 3780.
	ProxyPort string
	// ProxyBind is the bind address. Default: 127.0.0.1.
	ProxyBind string
	// DataDir is the root directory for SQLite DB and PID file. Default: ~/.clawde.
	DataDir string
	// DBPath is the full path to the SQLite database. Default: DataDir/clawde.db.
	DBPath string
	// LogLevel controls log verbosity. Default: info.
	LogLevel string
	// OllamaURL is the local Ollama base URL for the fallback lane. Default: http://localhost:11434.
	OllamaURL string
	// GatewayURL is the nself-ai-gateway base URL. Default: http://localhost:3760.
	GatewayURL string
	// RetrievalURL is the plugin-retrieval base URL for BGE-M3 embed calls. Default: http://localhost:3771.
	RetrievalURL string
	// MaxPoolSize is the max number of concurrent lane requests. Default: 4.
	MaxPoolSize int
	// ShutdownTimeoutS is graceful-shutdown deadline in seconds. Default: 5.
	ShutdownTimeoutS int
	// NselfSourceAccount is the source account ID forwarded to upstream plugins. Default: primary.
	NselfSourceAccount string
	// NselfAPIToken is the nSelf JWT for plugin auth. Default: empty (unauthenticated dev mode).
	NselfAPIToken string
}

// New reads all CLAWDE_* and NSELF_* env vars and returns a ClawdeConfig with defaults.
func New() ClawdeConfig {
	dataDir := getEnv("CLAWDE_DATA_DIR", homeDir()+"/.clawde")
	return ClawdeConfig{
		ProxyPort:          getEnv("CLAWDE_PROXY_PORT", "3780"),
		ProxyBind:          getEnv("CLAWDE_PROXY_BIND", "127.0.0.1"),
		DataDir:            dataDir,
		DBPath:             getEnv("CLAWDE_DB_PATH", dataDir+"/clawde.db"),
		LogLevel:           getEnv("CLAWDE_LOG_LEVEL", "info"),
		OllamaURL:          getEnv("CLAWDE_OLLAMA_URL", "http://localhost:11434"),
		GatewayURL:         getEnv("CLAWDE_GATEWAY_URL", "http://localhost:3760"),
		RetrievalURL:       getEnv("CLAWDE_RETRIEVAL_URL", "http://localhost:3771"),
		MaxPoolSize:        getEnvInt("CLAWDE_MAX_POOL_SIZE", 4),
		ShutdownTimeoutS:   getEnvInt("CLAWDE_SHUTDOWN_TIMEOUT_S", 5),
		NselfSourceAccount: getEnv("NSELF_SOURCE_ACCOUNT", "primary"),
		NselfAPIToken:      getEnv("NSELF_API_TOKEN", ""),
	}
}

// getEnv returns the value of key or fallback if unset or empty.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvInt returns the integer value of key or fallback if unset, empty, or unparseable.
func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// homeDir returns os.UserHomeDir() or "/root" on error.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/root"
}
