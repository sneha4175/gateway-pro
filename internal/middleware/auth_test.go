package middleware

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── test key generation ───────────────────────────────────────────────────────

// testKeyPair generates a throwaway RSA key pair for tests.
// We generate it fresh every test run to avoid committed secrets.
type testKeyPair struct {
	priv    *rsa.PrivateKey
	pubPath string // path to temp PEM file
}

func newTestKeyPair(t *testing.T) *testKeyPair {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "pub.pem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create PEM file: %v", err)
	}
	defer f.Close()

	if err := pem.Encode(f, &pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}); err != nil {
		t.Fatalf("encode PEM: %v", err)
	}

	return &testKeyPair{priv: priv, pubPath: path}
}

// makeToken builds a signed RS256 JWT with the given claims.
func (kp *testKeyPair) makeToken(sub string, exp int64) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"sub": sub,
		"exp": exp,
		"iat": time.Now().Unix(),
	})

	h64 := base64.RawURLEncoding.EncodeToString(hdr)
	c64 := base64.RawURLEncoding.EncodeToString(claims)
	msg := h64 + "." + c64

	digest := sha256.Sum256([]byte(msg))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, kp.priv, crypto.SHA256, digest[:])
	return msg + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// makeTokenAlg builds a token with a custom alg header (for negative tests).
func (kp *testKeyPair) makeTokenAlg(alg string) string {
	hdr, _ := json.Marshal(map[string]string{"alg": alg, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"sub": "x", "exp": time.Now().Add(time.Hour).Unix()})
	h64 := base64.RawURLEncoding.EncodeToString(hdr)
	c64 := base64.RawURLEncoding.EncodeToString(claims)
	return h64 + "." + c64 + ".invalidsig"
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestAuthMiddleware_Disabled(t *testing.T) {
	mw, err := NewAuthMiddleware(AuthConfig{Enabled: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("expected next handler to be called when auth is disabled")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	kp := newTestKeyPair(t)
	mw, err := NewAuthMiddleware(AuthConfig{
		Enabled:       true,
		PublicKeyPath: kp.pubPath,
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware: %v", err)
	}

	token := kp.makeToken("user-42", time.Now().Add(time.Hour).Unix())

	var gotUserID string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = r.Header.Get("X-User-ID")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("want 200, got %d — body: %s", rr.Code, rr.Body.String())
	}
	if gotUserID != "user-42" {
		t.Errorf("want X-User-ID=user-42, got %q", gotUserID)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	kp := newTestKeyPair(t)
	mw, err := NewAuthMiddleware(AuthConfig{
		Enabled:       true,
		PublicKeyPath: kp.pubPath,
	})
	if err != nil {
		t.Fatalf("NewAuthMiddleware: %v", err)
	}

	// Token expired 1 hour ago
	token := kp.makeToken("user-42", time.Now().Add(-time.Hour).Unix())

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called for expired token")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
	body := rr.Body.String()
	if body == "" {
		t.Error("expected JSON error body")
	}
}

func TestAuthMiddleware_MissingAuthHeader(t *testing.T) {
	kp := newTestKeyPair(t)
	mw, _ := NewAuthMiddleware(AuthConfig{Enabled: true, PublicKeyPath: kp.pubPath})

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_WrongAlgorithm(t *testing.T) {
	kp := newTestKeyPair(t)
	mw, _ := NewAuthMiddleware(AuthConfig{Enabled: true, PublicKeyPath: kp.pubPath})

	token := kp.makeTokenAlg("HS256")
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_TamperedSignature(t *testing.T) {
	kp := newTestKeyPair(t)
	mw, _ := NewAuthMiddleware(AuthConfig{Enabled: true, PublicKeyPath: kp.pubPath})

	token := kp.makeToken("user-42", time.Now().Add(time.Hour).Unix())
	// Flip the last character of the signature — any signature change must fail.
	tampered := token[:len(token)-1] + "X"

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called with tampered token")
	})).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_SkipPaths(t *testing.T) {
	kp := newTestKeyPair(t)
	mw, _ := NewAuthMiddleware(AuthConfig{
		Enabled:       true,
		PublicKeyPath: kp.pubPath,
		SkipPaths:     []string{"/health", "/metrics"},
	})

	for _, path := range []string{"/health", "/healthz", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			called := false
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rr, req)

			if !called {
				t.Errorf("expected skip for path %s", path)
			}
		})
	}
}

func TestAuthMiddleware_MalformedToken(t *testing.T) {
	kp := newTestKeyPair(t)
	mw, _ := NewAuthMiddleware(AuthConfig{Enabled: true, PublicKeyPath: kp.pubPath})

	for _, bad := range []string{"notajwt", "only.two", "", "a.b.c.d"} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
			req.Header.Set("Authorization", "Bearer "+bad)
			rr := httptest.NewRecorder()
			mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("want 401 for %q, got %d", bad, rr.Code)
			}
		})
	}
}
