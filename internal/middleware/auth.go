package middleware

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"
)

type Claims struct {
	Subject    string                 `json:"sub"`
	ExpiresAt  int64                  `json:"exp"`
	IssuedAt   int64                  `json:"iat"`
	Issuer     string                 `json:"iss"`
	Additional map[string]interface{} `json:"-"`
}

type Validator interface {
	Validate(tokenString string) (*Claims, error)
}

type RS256Validator struct {
	publicKey *rsa.PublicKey
}

func NewRS256Validator(publicKey *rsa.PublicKey) *RS256Validator {
	return &RS256Validator{publicKey: publicKey}
}

func LoadPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decode PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA public key")
	}

	return rsaPub, nil
}

func (v *RS256Validator) Validate(tokenString string) (*Claims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	header, err := decodePart(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}

	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(header, &hdr); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}

	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm: %s", hdr.Alg)
	}

	payload, err := decodePart(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	if claims.ExpiresAt > 0 && time.Now().Unix() > claims.ExpiresAt {
		return nil, fmt.Errorf("token expired")
	}

	signingInput := parts[0] + "." + parts[1]
	signature, err := decodePart(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	if err := verifyRS256(v.publicKey, []byte(signingInput), signature); err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	return &claims, nil
}

func Auth(validator Validator, skipPaths []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, path := range skipPaths {
				if r.URL.Path == path {
					next.ServeHTTP(w, r)
					return
				}
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")

			claims, err := validator.Validate(token)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusUnauthorized)
				return
			}

			r.Header.Set("X-User-ID", claims.Subject)

			next.ServeHTTP(w, r)
		})
	}
}

func decodePart(part string) ([]byte, error) {
	if l := len(part) % 4; l > 0 {
		part += strings.Repeat("=", 4-l)
	}

	return base64.URLEncoding.DecodeString(part)
}

func verifyRS256(pub *rsa.PublicKey, signingInput, sig []byte) error {
	h := sha256.Sum256(signingInput)

	sigInt := new(big.Int).SetBytes(sig)

	msgInt := new(big.Int).Exp(sigInt, big.NewInt(int64(pub.E)), pub.N)

	expected := new(big.Int).SetBytes(h[:])

	if msgInt.Cmp(expected) != 0 {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}
