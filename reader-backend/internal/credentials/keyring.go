package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const EnvPaymentCredentialKeys = "PAYMENT_CREDENTIAL_KEYS"

// LoadFromEnv loads the credential keyring from PAYMENT_CREDENTIAL_KEYS.
func LoadFromEnv() (*Keyring, error) {
	return ParseKeyring(os.Getenv(EnvPaymentCredentialKeys))
}

const aes256KeyBytes = 32

var (
	ErrKeyringUnconfigured = errors.New("PAYMENT_CREDENTIAL_KEYS is not configured")
	ErrKeyVersionUnknown   = errors.New("credential key version not found in keyring")
	ErrInvalidCiphertext   = errors.New("invalid encrypted credential payload")
)

// Keyring provides AES-256-GCM authenticated decryption across versioned keys.
type Keyring struct {
	keys    map[string][]byte
	ordered []string
}

// ParseKeyring parses comma-separated "version:base64_32byte_key" definitions.
func ParseKeyring(raw string) (*Keyring, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrKeyringUnconfigured
	}
	keys := make(map[string][]byte)
	var ordered []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		version, encoded, ok := strings.Cut(entry, ":")
		version = strings.TrimSpace(version)
		encoded = strings.TrimSpace(encoded)
		if !ok || version == "" || encoded == "" {
			return nil, errors.New("invalid PAYMENT_CREDENTIAL_KEYS entry format; expected version:base64")
		}
		if _, dup := keys[version]; dup {
			return nil, fmt.Errorf("duplicate key version %q in PAYMENT_CREDENTIAL_KEYS", version)
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(encoded)
		}
		if err != nil || len(decoded) != aes256KeyBytes {
			return nil, fmt.Errorf("credential key %q must decode to %d bytes", version, aes256KeyBytes)
		}
		keys[version] = decoded
		ordered = append(ordered, version)
	}
	if len(ordered) == 0 {
		return nil, ErrKeyringUnconfigured
	}
	sort.Strings(ordered)
	return &Keyring{keys: keys, ordered: ordered}, nil
}

// ActiveVersion resolves the version used for new encryptions.
func (k *Keyring) ActiveVersion(preferred string) (string, error) {
	if k == nil || len(k.keys) == 0 {
		return "", ErrKeyringUnconfigured
	}
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		if _, ok := k.keys[preferred]; !ok {
			return "", fmt.Errorf("%w: %s", ErrKeyVersionUnknown, preferred)
		}
		return preferred, nil
	}
	return k.ordered[len(k.ordered)-1], nil
}

// Encrypt seals plaintext using the named key version and optional AAD context binding.
func (k *Keyring) Encrypt(version, plaintext, aad string) (string, error) {
	if k == nil {
		return "", ErrKeyringUnconfigured
	}
	key, ok := k.keys[version]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrKeyVersionUnknown, version)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), []byte(aad))
	return version + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt authenticates and opens a ciphertext sealed by Encrypt.
func (k *Keyring) Decrypt(keyVersion, ciphertext, aad string) (string, error) {
	if k == nil {
		return "", ErrKeyringUnconfigured
	}
	encoded := strings.TrimSpace(ciphertext)
	if prefix, rest, ok := strings.Cut(encoded, ":"); ok && prefix != "" {
		if keyVersion != "" && prefix != keyVersion {
			return "", ErrInvalidCiphertext
		}
		keyVersion = prefix
		encoded = rest
	}
	key, ok := k.keys[keyVersion]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrKeyVersionUnknown, keyVersion)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", ErrInvalidCiphertext
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, body, []byte(aad))
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	return string(plaintext), nil
}

// CredentialAAD binds encrypted blobs to their specific channel account, version and purpose.
func CredentialAAD(accountID int64, version int, purpose string) string {
	return fmt.Sprintf("payment_channel:%d:%d:%s", accountID, version, purpose)
}
