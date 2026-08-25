// Package identity implements portable Ed25519 identity: deriving a
// canonical user ID from a public key, and verifying possession of the
// corresponding private key. It never sees or accepts a private key itself.
package identity

import (
	"crypto/ed25519"
	"encoding/base32"
	"errors"
	"strings"
)

var ErrInvalidPublicKey = errors.New("identity: public key must be 32 bytes")

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// UserID derives the canonical, server-independent user ID from an Ed25519
// public key. The same key always derives the same ID, on any server,
// without consulting one. See docs/architecture.md section 6.
func UserID(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", ErrInvalidPublicKey
	}
	return "usr_" + strings.ToLower(encoding.EncodeToString(publicKey)), nil
}

// VerifySignature reports whether signature is a valid Ed25519 signature by
// publicKey over message. It never touches private key material.
func VerifySignature(publicKey ed25519.PublicKey, message, signature []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(publicKey, message, signature)
}
