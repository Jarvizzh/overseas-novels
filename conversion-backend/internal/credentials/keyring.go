package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
)

type Keyring map[string]cipher.AEAD

func Parse(raw string) (Keyring, error) {
	keys := make(Keyring)
	if raw == "" {
		return keys, nil
	}
	var encoded map[string]string
	if err := json.Unmarshal([]byte(raw), &encoded); err != nil {
		return nil, errors.New("invalid CONVERSION_CREDENTIAL_KEYS")
	}
	for version, value := range encoded {
		key, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(key) != 32 || version == "" {
			return nil, errors.New("credential keys require a version and a base64 AES-256 key")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		gcm, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, err
		}
		keys[version] = gcm
	}
	return keys, nil
}

func (k Keyring) Decrypt(version string, ciphertext []byte) (string, error) {
	gcm, ok := k[version]
	if !ok {
		return "", errors.New("credential key version unavailable")
	}
	plaintext, err := gcm.Open(nil, nil, ciphertext, []byte(version))
	if err != nil || len(plaintext) == 0 {
		return "", errors.New("invalid encrypted credential")
	}
	return string(plaintext), nil
}

func (k Keyring) Encrypt(version, plaintext string) ([]byte, error) {
	gcm, ok := k[version]
	if !ok || plaintext == "" {
		return nil, errors.New("credential or key version unavailable")
	}
	return gcm.Seal(nil, nil, []byte(plaintext), []byte(version)), nil
}
