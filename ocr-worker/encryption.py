import os
import base64
import hashlib
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

# Initialize key
raw_key = os.environ.get("ENCRYPTION_KEY", "default_secure_passphrase_change_me")
encryption_key = hashlib.sha256(raw_key.encode("utf-8")).digest()

def encrypt(plaintext):
    if plaintext is None:
        return None
    if not isinstance(plaintext, str):
        plaintext = str(plaintext)
    if plaintext == "":
        return ""
    
    # 12-byte random nonce
    nonce = os.urandom(12)
    aesgcm = AESGCM(encryption_key)
    ciphertext_and_tag = aesgcm.encrypt(nonce, plaintext.encode("utf-8"), None)
    
    # Combine nonce + ciphertext + tag
    result = nonce + ciphertext_and_tag
    return base64.b64encode(result).decode("utf-8")

def decrypt(b64_str):
    if b64_str is None or b64_str == "":
        return b64_str
    
    try:
        data = base64.b64decode(b64_str)
        if len(data) < 12:
            return b64_str # Fallback for unencrypted legacy fields
        
        nonce = data[:12]
        ciphertext_and_tag = data[12:]
        
        aesgcm = AESGCM(encryption_key)
        plaintext = aesgcm.decrypt(nonce, ciphertext_and_tag, None)
        return plaintext.decode("utf-8")
    except Exception:
        # Fallback for unencrypted legacy fields
        return b64_str
