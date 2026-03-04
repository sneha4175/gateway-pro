// Package middleware provides HTTP middleware for gateway-pro.
// auth.go implements JWT validation using RS256 (RSA + SHA-256).
// We parse and validate tokens manually using Go's stdlib crypto packages
// to keep the dependency count at zero — no jwt library needed.
package middleware

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// AuthConfig is populated from gateway.yaml under the `auth:` key.
type AuthConfig struct {
	Enabled       bool     `yaml:"enabled"`
	PublicKeyPath string   `yaml:"public_key_path"` // path to PEM-encoded RSA public key
	SkipPaths     []string `yaml:"skip_paths"`      // e.g. ["/health", "/metrics"]
}

// jwtHeader is the decoded first segment of a JWT.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// jwtClaims is the decoded second segment of a JWT.
// We only extract the fields gateway-pro needs — extra fields are ignored.
type jwtClaims struct {
	Sub string `json:"sub"` // forwarded as X-User-ID
	Exp int64  `json:"exp"` // Unix timestamp — reject if in the past
	Iat int64  `json:"iat"` // issued-at — sanity check only
}

// authMiddleware holds the parsed public key and config.
// Created once at startup via NewAuthMiddleware; shared across goroutines safely
// because rsa.PublicKey is read-only after construction.
type authMiddleware struct {
	cfg    AuthConfig
	pubKey *rsa.PublicKey
}

// NewAuthMiddleware loads the RSA public key from disk and returns a middleware
// function. Returns an error at startup rather than at request time so
// misconfiguration is caught immediately.
func NewAuthMiddleware(cfg AuthConfig) (func(http.Handler) http.Handler, error) {
	if !cfg.Enabled {
		// Return a no-op passthrough — cheaper than a branch on every request.
		return func(next http.Handler) http.Handler { return next }, nil
	}

	key, err := loadPublicKey(cfg.PublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("auth: load public key %q: %w", cfg.PublicKeyPath, err)
	}

	am := &authMiddleware{cfg: cfg, pubKey: key}
	return am.handler, nil
}

// handler is the actual middleware. It runs on every request that is not
// in SkipPaths and rejects requests with invalid or expired JWTs.
func (am *authMiddleware) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if am.isSkipped(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		token, err := extractBearer(r)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, err.Error())
			return
		}

		claims, err := am.validateToken(token)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, err.Error())
			return
		}

		// Inject the validated subject so upstream services don't need to
		// re-parse the JWT — they can trust X-User-ID because it passed our check.
		r.Header.Set("X-User-ID", claims.Sub)
		next.ServeHTTP(w, r)
	})
}

// isSkipped returns true if the path matches any of the configured skip paths.
// Matching is prefix-based so /health matches /healthz too.
func (am *authMiddleware) isSkipped(path string) bool {
	for _, skip := range am.cfg.SkipPaths {
		if strings.HasPrefix(path, skip) {
			return true
		}
	}
	return false
}

// validateToken parses and cryptographically verifies a JWT string.
// Format: base64url(header).base64url(claims).base64url(signature)
func (am *authMiddleware) validateToken(token string) (*jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed token: expected 3 parts, got %d", len(parts))
	}

	hdr, err := decodeHeader(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	// We only support RS256. Reject "none" and symmetric algos defensively —
	// accepting unknown algorithms is a common JWT vulnerability.
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm %q: only RS256 accepted", hdr.Alg)
	}

	claims, err := decodeClaims(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	// Check expiry before verifying the signature — cheap check first.
	if claims.Exp == 0 {
		return nil, fmt.Errorf("token missing exp claim")
	}
	if time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("token expired")
	}

	if err := am.verifySignature(parts[0]+"."+parts[1], parts[2]); err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	return claims, nil
}

// verifySignature checks the RSA-SHA256 signature over "header.claims".
// The signed message is the raw base64url string — NOT the decoded bytes.
func (am *authMiddleware) verifySignature(message, sigB64 string) error {
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	// SHA-256 hash of the signing input (header + "." + claims, base64url encoded)
	digest := sha256.Sum256([]byte(message))

	// rsa.VerifyPKCS1v15 returns nil on success, non-nil if signature is invalid.
	return rsa.VerifyPKCS1v15(am.pubKey, crypto.SHA256, digest[:], sig)
}

// ── helpers ──────────────────────────────────────────────────────────────────

// loadPublicKey reads a PEM file and returns a parsed RSA public key.
func loadPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %q", path)
	}

	// Support both PKIX (PUBLIC KEY) and legacy (RSA PUBLIC KEY) formats.
	switch block.Type {
	case "PUBLIC KEY":
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKIX public key: %w", err)
		}
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("key is not RSA")
		}
		return rsaKey, nil

	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)

	default:
		return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
	}
}

// extractBearer pulls the token string from "Authorization: Bearer <token>".
func extractBearer(r *http.Request) (string, error) {
	hdr := r.Header.Get("Authorization")
	if hdr == "" {
		return "", fmt.Errorf("missing Authorization header")
	}
	parts := strings.SplitN(hdr, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", fmt.Errorf("Authorization header must be 'Bearer <token>'")
	}
	return strings.TrimSpace(parts[1]), nil
}

func decodeHeader(s string) (*jwtHeader, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var h jwtHeader
	return &h, json.Unmarshal(b, &h)
}

func decodeClaims(s string) (*jwtClaims, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var c jwtClaims
	return &c, json.Unmarshal(b, &c)
}

// writeAuthError writes a JSON error response — consistent with gateway-pro's
// other error responses so clients can handle them uniformly.
func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}
