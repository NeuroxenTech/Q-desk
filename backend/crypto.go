package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"

	"crypto/mlkem"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"golang.org/x/crypto/hkdf"
)

// serverCrypto holds the post-quantum key material for this server instance.
// ML-KEM-768 is used for key encapsulation, ML-DSA-65 for signing.
type serverCrypto struct {
	dk     *mlkem.DecapsulationKey768 // ML-KEM-768 decapsulation key
	signSK *mldsa65.PrivateKey        // ML-DSA-65 signing private key
	signPK *mldsa65.PublicKey         // ML-DSA-65 signing public key
}

// newServerCrypto initialises ML-KEM and ML-DSA key material. If ML-DSA keys
// are provided via env they are loaded; otherwise a fresh pair is generated
// for ephemeral development.
func newServerCrypto(cfg *Config) (*serverCrypto, error) {
	seed := make([]byte, mlkem.SeedSize)
	if _, err := io.ReadFull(rand.Reader, seed); err != nil {
		return nil, err
	}
	dk, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		return nil, fmt.Errorf("new mlkem decapsulation key: %w", err)
	}

	sc := &serverCrypto{dk: dk}

	var pk *mldsa65.PublicKey
	var sk *mldsa65.PrivateKey
	if cfg.MLDSAPrivateKeyEnv != "" && cfg.MLDSAPublicKeyEnv != "" {
		skBytes, err := decodeKey(cfg.MLDSAPrivateKeyEnv)
		if err != nil {
			return nil, fmt.Errorf("decode mldsa private key: %w", err)
		}
		pkBytes, err := decodeKey(cfg.MLDSAPublicKeyEnv)
		if err != nil {
			return nil, fmt.Errorf("decode mldsa public key: %w", err)
		}
		pk = new(mldsa65.PublicKey)
		if err := pk.UnmarshalBinary(pkBytes); err != nil {
			return nil, fmt.Errorf("parse mldsa public key: %w", err)
		}
		sk = new(mldsa65.PrivateKey)
		if err := sk.UnmarshalBinary(skBytes); err != nil {
			return nil, fmt.Errorf("parse mldsa private key: %w", err)
		}
	} else {
		var err error
		pk, sk, err = mldsa65.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate mldsa65 key: %w", err)
		}
	}
	sc.signPK = pk
	sc.signSK = sk
	return sc, nil
}

// encapsulationKey returns the server's ML-KEM-768 public encapsulation key.
func (sc *serverCrypto) encapsulationKey() []byte {
	return sc.dk.EncapsulationKey().Bytes()
}

// mldsaPublicKey returns the server's ML-DSA-65 public key.
func (sc *serverCrypto) mldsaPublicKey() []byte {
	return sc.signPK.Bytes()
}

// decapsulate unpacks the ciphertext and returns the ML-KEM shared secret.
func (sc *serverCrypto) decapsulate(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) != mlkem.CiphertextSize768 {
		return nil, fmt.Errorf("invalid mlkem ciphertext length %d", len(ciphertext))
	}
	return sc.dk.Decapsulate(ciphertext)
}

// sign produces an ML-DSA-65 signature over message.
func (sc *serverCrypto) sign(message []byte) ([]byte, error) {
	sig := make([]byte, mldsa65.SignatureSize)
	if err := mldsa65.SignTo(sc.signSK, message, nil, false, sig); err != nil {
		return nil, err
	}
	return sig, nil
}

// sessionKey derives a 32-byte HKDF-SHA256 session key from the ML-KEM shared
// secret and an optional handshake context.
func (sc *serverCrypto) sessionKey(sharedKey, context []byte) ([]byte, error) {
	out := make([]byte, mlkem.SharedKeySize)
	akdf := hkdf.New(sha256.New, sharedKey, context, []byte("qdesk-session-v1"))
	if _, err := io.ReadFull(akdf, out); err != nil {
		return nil, err
	}
	return out, nil
}

// verifySignature verifies an ML-DSA-65 signature over message using pk.
func (sc *serverCrypto) verifySignature(pk *mldsa65.PublicKey, msg, sig []byte) bool {
	return mldsa65.Verify(pk, msg, nil, sig)
}

// hashLog computes a chained audit log hash that binds the previous hash,
// action, actor, fir and a nonce, making the log chain tamper-evident.
func hashLog(prevHash, action, actorID, fir string) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write([]byte{0})
	h.Write([]byte(action))
	h.Write([]byte{0})
	h.Write([]byte(actorID))
	h.Write([]byte{0})
	h.Write([]byte(fir))
	h.Write([]byte{0})
	return fmt.Sprintf("%x", h.Sum(nil))
}

// decodeKey decodes base64 (or raw hex) key material from the environment.
func decodeKey(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, fmt.Errorf("unsupported key encoding")
}

func parseIntDefault(s string, def int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def, err
	}
	return n, nil
}
