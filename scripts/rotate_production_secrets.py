import os
import secrets
import subprocess
import sys

PROJECT_ID = os.getenv("GCP_PROJECT_ID", "ai-projects-496915")

def generate_production_keys():
    # 32-byte exact key for AES-GCM-256 (24 urlsafe bytes = 32 chars)
    encryption_key = secrets.token_hex(16)  # Exactly 32 hex characters = 32 bytes
    jwt_secret = secrets.token_urlsafe(48)      # 64 characters high-entropy signing key
    db_password = secrets.token_urlsafe(24)      # 32 characters strong password
    redis_password = secrets.token_urlsafe(24)   # 32 characters strong password
    storage_secret_key = secrets.token_urlsafe(24)# 32 characters strong password
    
    return {
        "OCR_ENCRYPTION_KEY": encryption_key,
        "JWT_SECRET": jwt_secret,
        "DB_PASSWORD": db_password,
        "REDIS_PASSWORD": redis_password,
        "STORAGE_SECRET_KEY": storage_secret_key
    }

def add_gcp_secret_version(secret_id, payload_val):
    print(f"--> Rotating {secret_id} in Google Secret Manager...")
    cmd = [
        "gcloud", "secrets", "versions", "add", secret_id,
        f"--project={PROJECT_ID}",
        "--data-file=-"
    ]
    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    stdout, stderr = proc.communicate(input=payload_val)
    if proc.returncode != 0:
        print(f"Error rotating secret {secret_id}: {stderr}")
        return False
    print(f"   Successfully added new production version payload to {secret_id}.")
    return True

def update_local_env_file(new_secrets):
    env_path = os.path.join(os.path.dirname(__file__), "..", ".env")
    print(f"--> Updating local .env configuration at {env_path}...")
    lines = []
    if os.path.exists(env_path):
        with open(env_path, "r") as f:
            lines = f.readlines()
            
    # Create or replace key=val
    updated_keys = set()
    new_lines = []
    for line in lines:
        line_strip = line.strip()
        if "=" in line_strip and not line_strip.startswith("#"):
            k, _ = line_strip.split("=", 1)
            k = k.strip()
            if k in new_secrets:
                new_lines.append(f"{k}={new_secrets[k]}\n")
                updated_keys.add(k)
                continue
        new_lines.append(line)
        
    for k, v in new_secrets.items():
        if k not in updated_keys:
            new_lines.append(f"{k}={v}\n")
            
    with open(env_path, "w") as f:
        f.writelines(new_lines)
    print("   Updated .env successfully.")

def main():
    print("=== Enterprise OCR Platform: Production Key Rotation ===")
    print(f"Target GCP Project: {PROJECT_ID}\n")
    
    new_secrets = generate_production_keys()
    
    print("Generated High-Entropy Production Credentials:")
    for k, v in new_secrets.items():
        # Mask display for safety except length
        masked = v[:4] + "..." + v[-4:] if len(v) > 8 else "***"
        print(f" - {k}: {masked} (length: {len(v)})")
    print("")
    
    success = True
    for k, v in new_secrets.items():
        if not add_gcp_secret_version(k, v):
            success = False
            
    update_local_env_file(new_secrets)
    
    if success:
        print("\n=== PRODUCTION KEY ROTATION COMPLETED SUCCESSFULLY ===")
    else:
        print("\n=== KEY ROTATION COMPLETED WITH WARNINGS ===")

if __name__ == "__main__":
    main()
