package models

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

type EncryptedString string

var dbEncryptionKey []byte

func InitEncryption() {
	keyStr := os.Getenv("ENCRYPTION_KEY")
	if keyStr == "" {
		keyStr = "default_secure_passphrase_change_me"
	}
	hash := sha256.Sum256([]byte(keyStr))
	dbEncryptionKey = hash[:]
}

// Value implements the driver.Valuer interface to encrypt on write
func (es EncryptedString) Value() (driver.Value, error) {
	if es == "" {
		return nil, nil
	}
	if len(dbEncryptionKey) == 0 {
		InitEncryption()
	}

	block, err := aes.NewCipher(dbEncryptionKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(es), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Scan implements the sql.Scanner interface to decrypt on read
func (es *EncryptedString) Scan(value interface{}) error {
	if value == nil {
		*es = ""
		return nil
	}

	b64Str, ok := value.(string)
	if !ok {
		// Might be bytes
		bytes, ok := value.([]byte)
		if !ok {
			return fmt.Errorf("invalid type for EncryptedString scan: %T", value)
		}
		b64Str = string(bytes)
	}

	if b64Str == "" {
		*es = ""
		return nil
	}

	if len(dbEncryptionKey) == 0 {
		InitEncryption()
	}

	data, err := base64.StdEncoding.DecodeString(b64Str)
	if err != nil {
		// Fallback: If it's not base64, maybe it's unencrypted legacy data
		*es = EncryptedString(b64Str)
		return nil
	}

	block, err := aes.NewCipher(dbEncryptionKey)
	if err != nil {
		return err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		// Fallback for non-encrypted base64 strings
		*es = EncryptedString(b64Str)
		return nil
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// Fallback: decryption failed, might be unencrypted legacy base64 text
		*es = EncryptedString(b64Str)
		return nil
	}

	*es = EncryptedString(plaintext)
	return nil
}
