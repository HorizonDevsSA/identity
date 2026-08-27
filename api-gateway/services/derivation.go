package services

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DeriveWalletAddress derives a deterministic ZWC wallet address from the ID number and a master salt.
func DeriveWalletAddress(idNumber, salt string) (string, error) {
	// 1. Clean the ID number (lowercase, alphanumeric characters only)
	cleanID := cleanIDNumber(idNumber)
	if cleanID == "" {
		return "", fmt.Errorf("invalid or empty ID number")
	}

	// 2. Compute SHA256 of cleanID + salt to get a 32-byte seed
	hasher := sha256.New()
	hasher.Write([]byte(cleanID + salt))
	seed := hasher.Sum(nil)

	// 3. Create secp256k1 private key from seed
	privKey := &secp256k1.PrivKey{Key: seed}

	// 4. Get the public key
	pubKey := privKey.PubKey()

	// 5. Encode address bytes to Bech32 with "zwc" prefix
	addressStr, err := sdk.Bech32ifyAddressBytes("zwc", pubKey.Address().Bytes())
	if err != nil {
		return "", fmt.Errorf("failed to encode address to Bech32: %w", err)
	}

	return addressStr, nil
}

// cleanIDNumber normalizes the ID number by lowercasing it and keeping only alphanumeric characters.
func cleanIDNumber(s string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
