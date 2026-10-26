// Package tokens provides JWT creation and verification using RS256.
//
// Design:
//   - auth-service holds the RSA private key → signs access + refresh tokens
//   - gateway fetches public key via gRPC GetJWKS → verifies access tokens
//   - Access tokens: short-lived (15m), stored in-memory by the SPA
//   - Refresh tokens: long-lived (7d), stored in HttpOnly cookie + Redis
package tokens

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 7 * 24 * time.Hour
)

// Token types carried in the "typ" claim; only access tokens authenticate API calls.
const (
	TypeAccess  = "access"
	TypeRefresh = "refresh"
)

// ErrWrongTokenType means a validly signed token was used for the wrong purpose.
var ErrWrongTokenType = errors.New("wrong token type")

// Claims are the fields embedded in every token.
type Claims struct {
	UserID    string `json:"sub"`
	Email     string `json:"email"`
	SessionID string `json:"sid"`
	Type      string `json:"typ"`
	jwt.RegisteredClaims
}

// Maker creates and verifies JWTs.
type Maker struct {
	privateKey *rsa.PrivateKey
	kid        string // key ID, embedded in token header
}

// NewMakerFromFile loads an RSA private key from a PEM file.
// Generate one with: openssl genrsa -out jwt_private.pem 2048
func NewMakerFromFile(pemPath string) (*Maker, error) {
	data, err := os.ReadFile(pemPath)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	return NewMakerFromPEM(data)
}

// NewMakerFromPEM loads an RSA private key from PEM bytes.
func NewMakerFromPEM(pemBytes []byte) (*Maker, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// fall back to PKCS1
		rsaKey, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		key = rsaKey
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not RSA")
	}

	// kid = first 8 bytes of the public modulus in hex — stable across restarts
	kid := fmt.Sprintf("%x", rsaKey.N.Bytes()[:8])
	return &Maker{privateKey: rsaKey, kid: kid}, nil
}

// GenerateKey creates a new 2048-bit RSA key pair and returns PEM-encoded bytes.
// Use this in tests or for initial key generation.
func GenerateKey() (privateKeyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	b, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b}), nil
}

// CreateAccessToken mints a 15-minute access JWT for userID + email + sessionID.
func (m *Maker) CreateAccessToken(userID, email, sessionID string) (string, error) {
	return m.create(TypeAccess, userID, email, sessionID, AccessTokenTTL)
}

// CreateRefreshToken mints a 7-day refresh JWT for userID + sessionID.
func (m *Maker) CreateRefreshToken(userID, email, sessionID string) (string, error) {
	return m.create(TypeRefresh, userID, email, sessionID, RefreshTokenTTL)
}

func (m *Maker) create(typ, userID, email, sessionID string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		Email:     email,
		SessionID: sessionID,
		Type:      typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	t := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	t.Header["kid"] = m.kid
	return t.SignedString(m.privateKey)
}

// Verify parses and validates a JWT, returning its claims.
func (m *Maker) Verify(tokenStr string) (*Claims, error) {
	t, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return &m.privateKey.PublicKey, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := t.Claims.(*Claims)
	if !ok || !t.Valid {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

// VerifyAccess is Verify plus a check that the token is an access token.
func (m *Maker) VerifyAccess(tokenStr string) (*Claims, error) {
	claims, err := m.Verify(tokenStr)
	if err != nil {
		return nil, err
	}
	if claims.Type != TypeAccess {
		return nil, ErrWrongTokenType
	}
	return claims, nil
}

// PublicKeyN returns the base64url-encoded modulus of the public key (for JWKS).
func (m *Maker) PublicKeyN() string {
	return base64.RawURLEncoding.EncodeToString(m.privateKey.N.Bytes())
}

// PublicKeyE returns the base64url-encoded exponent of the public key (for JWKS).
func (m *Maker) PublicKeyE() string {
	e := big.NewInt(int64(m.privateKey.E))
	return base64.RawURLEncoding.EncodeToString(e.Bytes())
}

// KID returns the key ID embedded in token headers.
func (m *Maker) KID() string {
	return m.kid
}
