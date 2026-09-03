package main

import (
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	RedisURL           string
	DatabaseURL        string
	MLDSAPrivateKeyEnv string
	MLDSAPublicKeyEnv  string
	TicketTTLSeconds   int
	ListenAddr         string
	AllowedOrigins     []string

	// Evidence uploads are stored in a private Supabase Storage bucket. The
	// service role key is required to create the bucket and issue short-lived
	// signed URLs; without it the upload endpoints answer 503.
	SupabaseURL           string
	SupabaseServiceKey    string
	MaxUploadSizeMB       int64
	MalwareScanEnabled    bool
}

// loadConfig reads all required configuration from the environment.
func loadConfig() *Config {
	cfg := &Config{
		RedisURL:           os.Getenv("UPSTASH_REDIS_URL"),
		DatabaseURL:        os.Getenv("SUPABASE_DATABASE_URL"),
		MLDSAPrivateKeyEnv: os.Getenv("MLDSA_SERVER_PRIVATE_KEY"),
		MLDSAPublicKeyEnv:  os.Getenv("MLDSA_SERVER_PUBLIC_KEY"),
		TicketTTLSeconds:   600,
		ListenAddr:         ":8080",
		SupabaseURL:        strings.TrimSpace(os.Getenv("SUPABASE_URL")),
		SupabaseServiceKey: strings.TrimSpace(os.Getenv("SUPABASE_SERVICE_ROLE_KEY")),
		MaxUploadSizeMB:    50,
		MalwareScanEnabled: os.Getenv("MALWARE_SCAN_ENABLED") == "true",
	}
	if v := os.Getenv("TICKET_TTL_SECONDS"); v != "" {
		if n, err := parseIntDefault(v, 600); err == nil {
			cfg.TicketTTLSeconds = n
		}
	}
	if v := os.Getenv("MAX_UPLOAD_SIZE_MB"); v != "" {
		if n, err := parseInt64Default(v, 100); err == nil && n > 0 {
			cfg.MaxUploadSizeMB = n
		}
	}
	if cfg.RedisURL == "" {
		log.Fatal("UPSTASH_REDIS_URL is required")
	}
	if cfg.DatabaseURL == "" {
		log.Fatal("SUPABASE_DATABASE_URL is required")
	}

	// The Storage REST URL defaults to the Supabase project derived from the
	// database URL host (postgres.<ref>.supabase.co -> https://<ref>.supabase.co).
	if cfg.SupabaseURL == "" {
		cfg.SupabaseURL = inferSupabaseURL(cfg.DatabaseURL)
	}

	// Parse the comma-separated origin allowlist. Origins must match exactly
	// (no wildcards). When unset, defaults to the Tauri v2 webview origins for
	// local development — the dev server origin plus the production webview
	// scheme for each supported platform (see .env.example). Fail closed: any
	// origin not on this list is rejected.
	rawOrigins := os.Getenv("ALLOWED_ORIGINS")
	if rawOrigins == "" {
		rawOrigins = "http://localhost:3000,http://tauri.localhost,tauri://localhost"
	}
	cfg.AllowedOrigins = parseAllowedOrigins(rawOrigins)

	return cfg
}

func parseInt64Default(s string, def int64) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def, err
	}
	return n, nil
}

// inferSupabaseURL derives the project REST URL from the Postgres connection
// string. Accepted shapes (prefer an explicit SUPABASE_URL in .env — inference
// is a convenience fallback only):
//
//	<ref>.supabase.co
//	postgres.<ref>.supabase.co
//	postgres.<ref>.pooler.supabase.com
//	postgres.<ref>.<region>.pooler.supabase.com
//	<region>.pooler.supabase.com            -> ref taken from username postgres.<ref>
//
// -> https://<ref>.supabase.co. Falls back to an empty string when the host
// does not match any expected pattern (local Postgres in dev, for example).
func inferSupabaseURL(dbURL string) string {
	u, err := url.Parse(dbURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if !strings.Contains(host, ".supabase.co") && !strings.Contains(host, ".supabase.com") {
		return ""
	}

	var ref string
	switch {
	case strings.HasPrefix(host, "postgres."):
		ref = strings.TrimPrefix(host, "postgres.")
		ref = strings.SplitN(ref, ".", 2)[0]
	case strings.Contains(host, ".pooler.supabase.com"):
		// Region pooler hosts don't contain the ref; it sits in the username
		// (postgres.<ref> or <ref>).
		if user := u.User.Username(); user != "" {
			ref = strings.TrimPrefix(user, "postgres.")
			ref = strings.SplitN(ref, ".", 2)[0]
		}
	default:
		ref = strings.SplitN(host, ".", 2)[0]
	}
	if ref == "" {
		return ""
	}
	return "https://" + ref + ".supabase.co"
}

// parseAllowedOrigins splits a comma-separated origin list into trimmed,
// non-empty entries.
func parseAllowedOrigins(raw string) []string {
	var out []string
	for _, part := range splitCSV(raw) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// splitCSV splits on commas and trims surrounding whitespace from each entry.
func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := trimSpace(s[start:i])
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}

// trimSpace removes leading and trailing ASCII whitespace.
func trimSpace(s string) string {
	start := 0
	for start < len(s) {
		c := s[start]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		start++
	}
	end := len(s)
	for end > start {
		c := s[end-1]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		end--
	}
	return s[start:end]
}
