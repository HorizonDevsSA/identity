package services

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RegisterUserOnChain executes ZWC transactions to whitelist the user and register their alias.
func RegisterUserOnChain(walletAddress, aliasType, aliasValue string) error {
	binaryPath := os.Getenv("ZWC_BINARY_PATH")
	if binaryPath == "" {
		binaryPath = "/Volumes/Untitled/zwc/zwc-chain/zwc-chaind"
	}

	homeDir := os.Getenv("ZWC_HOME_DIR")
	if homeDir == "" {
		homeDir = "/Users/mac/.zwc-chain"
	}

	keyName := os.Getenv("ZWC_VERIFIER_KEY_NAME")
	if keyName == "" {
		keyName = "bob"
	}

	keyring := os.Getenv("ZWC_KEYRING_BACKEND")
	if keyring == "" {
		keyring = "test"
	}

	chainID := os.Getenv("ZWC_CHAIN_ID")
	if chainID == "" {
		chainID = "zwcchain"
	}

	nodeURL := os.Getenv("ZWC_NODE_URL")
	if nodeURL == "" {
		nodeURL = "tcp://localhost:26657"
	}

	walletAddress = strings.TrimSpace(walletAddress)
	aliasType = strings.TrimSpace(aliasType)
	aliasValue = strings.TrimSpace(aliasValue)

	if walletAddress == "" {
		return fmt.Errorf("wallet address cannot be empty")
	}

	log.Printf("[ZWC-INTEGRATION] Starting blockchain registration for %s...", walletAddress)

	// Step 1: Set KYC Status (Whitelist)
	kycArgs := []string{
		"tx", "kyc", "set-kyc-status",
		"--address", walletAddress,
		"--verified",
		"--tier", "1",
		"--from", keyName,
		"--keyring-backend", keyring,
		"--chain-id", chainID,
		"--node", nodeURL,
		"--yes",
		"--home", homeDir,
	}

	log.Printf("[ZWC-INTEGRATION] Running: %s %s", binaryPath, strings.Join(kycArgs, " "))
	cmd := exec.Command(binaryPath, kycArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ZWC-INTEGRATION] SetKYCStatus transaction failed: %v, Output: %s", err, string(output))
		return fmt.Errorf("kyc whitelist transaction failed: %w (output: %s)", err, string(output))
	}
	log.Printf("[ZWC-INTEGRATION] SetKYCStatus transaction succeeded! Output: %s", string(output))

	// Step 2: Set Alias (if specified)
	if aliasType != "" && aliasValue != "" {
		// Sleep for 2 seconds to allow the SetKYCStatus block to be committed
		// and avoid account sequence mismatch.
		log.Printf("[ZWC-INTEGRATION] Sleeping for 2 seconds to allow KYC block commit...")
		time.Sleep(2 * time.Second)

		aliasArgs := []string{
			"tx", "alias", "set-alias",
			"--address", walletAddress,
			"--alias-type", aliasType,
			"--alias-value", aliasValue,
			"--from", keyName,
			"--keyring-backend", keyring,
			"--chain-id", chainID,
			"--node", nodeURL,
			"--yes",
			"--home", homeDir,
		}

		log.Printf("[ZWC-INTEGRATION] Running: %s %s", binaryPath, strings.Join(aliasArgs, " "))
		cmdAlias := exec.Command(binaryPath, aliasArgs...)
		outputAlias, err := cmdAlias.CombinedOutput()
		if err != nil {
			log.Printf("[ZWC-INTEGRATION] SetAlias transaction failed: %v, Output: %s", err, string(outputAlias))
			return fmt.Errorf("alias mapping transaction failed: %w (output: %s)", err, string(outputAlias))
		}
		log.Printf("[ZWC-INTEGRATION] SetAlias transaction succeeded! Output: %s", string(outputAlias))
	}

	log.Printf("[ZWC-INTEGRATION] Registration process completed successfully for %s", walletAddress)
	return nil
}
