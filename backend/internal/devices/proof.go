package devices

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"time"
)

type PublicKey struct {
	Curve string `json:"crv"`
	Type  string `json:"kty"`
	X     string `json:"x"`
	Y     string `json:"y"`
}

func (k PublicKey) verificationKey() *ecdsa.PublicKey {
	x, xerr := base64.RawURLEncoding.Strict().DecodeString(k.X)
	y, yerr := base64.RawURLEncoding.Strict().DecodeString(k.Y)
	if k.Type != "EC" || k.Curve != "P-256" || len(k.X) != 43 || len(k.Y) != 43 ||
		xerr != nil || yerr != nil || len(x) != 32 || len(y) != 32 {
		return nil
	}
	point := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	if !point.Curve.IsOnCurve(point.X, point.Y) {
		return nil
	}
	return point
}

type proofClaims struct {
	Purpose        string    `json:"purpose"`
	PairingID      string    `json:"pairing_id"`
	Nonce          string    `json:"nonce"`
	ExpiresAt      time.Time `json:"expires_at"`
	IdempotencyKey string    `json:"idempotency_key"`
}

func verifyProof(key PublicKey, compact, pairing, nonce, receipt string, expiry time.Time) bool {
	payload := signedPayload(key, compact)
	var claims proofClaims
	return json.Unmarshal(payload, &claims) == nil &&
		claims.Purpose == "hive.device.pair.redeem.v1" && claims.PairingID == pairing && claims.Nonce == nonce &&
		claims.IdempotencyKey == receipt && claims.ExpiresAt.Equal(expiry)
}

func signedPayload(key PublicKey, compact string) []byte {
	parts := strings.Split(compact, ".")
	if len(parts) != 3 || len(compact) > 2048 {
		return nil
	}
	// One fixed protected header. There are no optional algorithms, key selectors or JOSE extensions.
	if parts[0] != "eyJhbGciOiJFUzI1NiJ9" {
		return nil
	}
	payload, payloadErr := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	sig, sigErr := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if payloadErr != nil || sigErr != nil || len(sig) != 64 {
		return nil
	}
	point := key.verificationKey()
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if point == nil || !ecdsa.Verify(point, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil
	}
	return payload
}
