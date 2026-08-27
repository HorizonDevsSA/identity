import os
import subprocess
import logging

logger = logging.getLogger(__name__)

def register_user_on_chain(wallet_address, alias_type=None, alias_value=None):
    binary_path = os.getenv("ZWC_BINARY_PATH", "/Volumes/Untitled/zwc/zwc-chain/zwc-chaind")
    home_dir = os.getenv("ZWC_HOME_DIR", "/Users/mac/.zwc-chain")
    key_name = os.getenv("ZWC_VERIFIER_KEY_NAME", "bob")
    keyring = os.getenv("ZWC_KEYRING_BACKEND", "test")
    chain_id = os.getenv("ZWC_CHAIN_ID", "zwcchain")
    node_url = os.getenv("ZWC_NODE_URL", "tcp://localhost:26657")

    if not wallet_address:
        logger.error("Wallet address cannot be empty")
        return False

    wallet_address = wallet_address.strip()
    logger.info(f"[ZWC-INTEGRATION] Starting blockchain registration for {wallet_address}...")

    # Step 1: Whitelist KYC status
    kyc_args = [
        binary_path,
        "tx", "kyc", "set-kyc-status",
        "--address", wallet_address,
        "--verified",
        "--tier", "1",
        "--from", key_name,
        "--keyring-backend", keyring,
        "--chain-id", chain_id,
        "--node", node_url,
        "--yes",
        "--home", home_dir
    ]

    logger.info(f"[ZWC-INTEGRATION] Running: {' '.join(kyc_args)}")
    try:
        res = subprocess.run(kyc_args, capture_output=True, text=True, check=True)
        logger.info(f"[ZWC-INTEGRATION] SetKYCStatus transaction succeeded! stdout: {res.stdout}")
    except subprocess.CalledProcessError as e:
        logger.error(f"[ZWC-INTEGRATION] SetKYCStatus transaction failed: {e}, stdout: {e.stdout}, stderr: {e.stderr}")
        return False
    except Exception as ex:
        logger.exception(f"[ZWC-INTEGRATION] SetKYCStatus unexpected error: {ex}")
        return False

    # Step 2: Register Alias (if type and value are provided)
    if alias_type and alias_value:
        import time
        logger.info("[ZWC-INTEGRATION] Sleeping for 2 seconds to allow KYC block commit...")
        time.sleep(2)
        alias_type = alias_type.strip()
        alias_value = alias_value.strip()

        alias_args = [
            binary_path,
            "tx", "alias", "set-alias",
            "--address", wallet_address,
            "--alias-type", alias_type,
            "--alias-value", alias_value,
            "--from", key_name,
            "--keyring-backend", keyring,
            "--chain-id", chain_id,
            "--node", node_url,
            "--yes",
            "--home", home_dir
        ]

        logger.info(f"[ZWC-INTEGRATION] Running: {' '.join(alias_args)}")
        try:
            res_alias = subprocess.run(alias_args, capture_output=True, text=True, check=True)
            logger.info(f"[ZWC-INTEGRATION] SetAlias transaction succeeded! stdout: {res_alias.stdout}")
        except subprocess.CalledProcessError as e:
            logger.error(f"[ZWC-INTEGRATION] SetAlias transaction failed: {e}, stdout: {e.stdout}, stderr: {e.stderr}")
            return False
        except Exception as ex:
            logger.exception(f"[ZWC-INTEGRATION] SetAlias unexpected error: {ex}")
            return False

    logger.info(f"[ZWC-INTEGRATION] Registration process completed successfully for {wallet_address}")
    return True
