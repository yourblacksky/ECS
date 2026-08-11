package browsernode

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"
)

func TestVerifyBrowserIdentity(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rawKey := elliptic.Marshal(elliptic.P256(), privateKey.X, privateKey.Y)
	fingerprint := sha256.Sum256(rawKey)
	nodeID := "ecs_" + hex.EncodeToString(fingerprint[:16])
	challenge := "test-challenge"
	digest := sha256.Sum256([]byte(challengeMessage(challenge, nodeID)))
	r, s, err := ecdsa.Sign(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(paddedInteger(r), paddedInteger(s)...)
	jwk, err := json.Marshal(publicJWK{
		KTY: "EC",
		CRV: "P-256",
		X:   base64.RawURLEncoding.EncodeToString(privateKey.X.FillBytes(make([]byte, 32))),
		Y:   base64.RawURLEncoding.EncodeToString(privateKey.Y.FillBytes(make([]byte, 32))),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := verifyBrowserIdentity(
		nodeID,
		jwk,
		challenge,
		base64.RawURLEncoding.EncodeToString(signature),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(fingerprint[:]) {
		t.Fatalf("fingerprint = %q", got)
	}
	if _, err := verifyBrowserIdentity(nodeID, jwk, challenge+"-tampered",
		base64.RawURLEncoding.EncodeToString(signature)); err == nil {
		t.Fatal("tampered challenge was accepted")
	}
}

func paddedInteger(value *big.Int) []byte {
	return value.FillBytes(make([]byte, 32))
}
