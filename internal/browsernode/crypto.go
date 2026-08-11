package browsernode

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
)

type publicJWK struct {
	KTY string `json:"kty"`
	CRV string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func verifyBrowserIdentity(nodeID string, rawJWK json.RawMessage, challenge, signature string) (string, error) {
	var jwk publicJWK
	if err := json.Unmarshal(rawJWK, &jwk); err != nil {
		return "", errors.New("invalid browser public key")
	}
	if jwk.KTY != "EC" || jwk.CRV != "P-256" {
		return "", errors.New("browser identity key must be ECDSA P-256")
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil || len(xBytes) != 32 {
		return "", errors.New("invalid browser public key x coordinate")
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil || len(yBytes) != 32 {
		return "", errors.New("invalid browser public key y coordinate")
	}
	publicKey := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}
	if !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return "", errors.New("browser public key is not on P-256")
	}
	rawKey := elliptic.Marshal(publicKey.Curve, publicKey.X, publicKey.Y)
	fingerprint := sha256.Sum256(rawKey)
	expectedNodeID := "ecs_" + hex.EncodeToString(fingerprint[:16])
	if nodeID != expectedNodeID {
		return "", errors.New("browser node_id does not match its public key")
	}

	signatureBytes, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || len(signatureBytes) != 64 {
		return "", errors.New("browser signature must be a 64-byte ECDSA signature")
	}
	digest := sha256.Sum256([]byte(challengeMessage(challenge, nodeID)))
	r := new(big.Int).SetBytes(signatureBytes[:32])
	s := new(big.Int).SetBytes(signatureBytes[32:])
	if !ecdsa.Verify(publicKey, digest[:], r, s) {
		return "", errors.New("browser challenge signature is invalid")
	}
	return hex.EncodeToString(fingerprint[:]), nil
}

func challengeMessage(challenge, nodeID string) string {
	return "ecs-browser-v1\n" + challenge + "\n" + nodeID
}
