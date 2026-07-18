#!/usr/bin/env bash

# Exit immediately if a command exits with a non-zero status
set -e

echo "=== Enterprise OCR Platform: Google Secret Manager Setup ==="

# Check gcloud installation
if ! command -v gcloud &> /dev/null; then
    echo "Error: gcloud CLI is not installed."
    exit 1
fi

# 1. Prompt for GCP Project ID if not set
if [ -z "$GCP_PROJECT_ID" ]; then
    read -p "Enter your GCP Project ID: " GCP_PROJECT_ID
fi

if [ -z "$GCP_PROJECT_ID" ]; then
    echo "Error: GCP Project ID cannot be empty."
    exit 1
fi

echo "--> Using GCP Project ID: ${GCP_PROJECT_ID}"
gcloud config set project "${GCP_PROJECT_ID}"

# 2. Enable Secret Manager API
echo "--> Enabling secretmanager.googleapis.com API..."
gcloud services enable secretmanager.googleapis.com

# 3. Create Service Account if it doesn't exist
SA_NAME="ocr-platform-sa"
SA_EMAIL="${SA_NAME}@${GCP_PROJECT_ID}.iam.gserviceaccount.com"

echo "--> Setting up Service Account (${SA_EMAIL})..."
if ! gcloud iam service-accounts describe "${SA_EMAIL}" &> /dev/null; then
    gcloud iam service-accounts create "${SA_NAME}" \
        --display-name="OCR Platform Service Account"
    echo "Created service account ${SA_NAME}"
else
    echo "Service account ${SA_NAME} already exists."
fi

# 4. Define Helper Function to Provision Secrets
create_secret_if_missing() {
    local secret_id="$1"
    local secret_val="$2"

    echo "--> Provisioning secret: ${secret_id}..."
    if ! gcloud secrets describe "${secret_id}" &> /dev/null; then
        gcloud secrets create "${secret_id}" \
            --replication-policy="automatic" \
            --quiet
    fi

    # Add secret version payload
    echo -n "${secret_val}" | gcloud secrets versions add "${secret_id}" --data-file=-
    echo "   Added version to ${secret_id}."

    # Grant Secret Accessor role to Service Account
    gcloud secrets add-iam-policy-binding "${secret_id}" \
        --member="serviceAccount:${SA_EMAIL}" \
        --role="roles/secretmanager.secretAccessor" \
        --quiet > /dev/null
    echo "   Granted secretAccessor role for ${secret_id} to ${SA_EMAIL}."
}

# 5. Provision Platform Secrets
echo "--> Provisioning secrets for OCR Platform..."

# Prompts or default environment values
ENCRYPTION_KEY="${ENCRYPTION_KEY:-super_secret_key_32_bytes_long!!}"
JWT_SECRET="${JWT_SECRET:-supersecretjwttokenkey123!}"
DB_PASSWORD="${DB_PASSWORD:-ocr_secure_password}"
REDIS_PASSWORD="${REDIS_PASSWORD:-redis_secure_password}"
STORAGE_SECRET_KEY="${STORAGE_SECRET_KEY:-minioadmin}"

create_secret_if_missing "OCR_ENCRYPTION_KEY" "${ENCRYPTION_KEY}"
create_secret_if_missing "JWT_SECRET" "${JWT_SECRET}"
create_secret_if_missing "DB_PASSWORD" "${DB_PASSWORD}"
create_secret_if_missing "REDIS_PASSWORD" "${REDIS_PASSWORD}"
create_secret_if_missing "STORAGE_SECRET_KEY" "${STORAGE_SECRET_KEY}"

echo ""
echo "=== SETUP COMPLETE ==="
echo "All OCR secrets created and IAM permissions granted to ${SA_EMAIL}."
echo "List secrets with: gcloud secrets list"
