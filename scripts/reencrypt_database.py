import os
import sys
import psycopg2
from psycopg2.extras import RealDictCursor
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
import hashlib
import base64

def get_key_bytes(key_str):
    return hashlib.sha256(key_str.encode("utf-8")).digest()

def decrypt_val(encrypted_str, old_key_bytes):
    if not encrypted_str:
        return encrypted_str
    try:
        raw_data = base64.b64decode(encrypted_str)
        if len(raw_data) < 12:
            return encrypted_str
        nonce = raw_data[:12]
        ciphertext = raw_data[12:]
        aesgcm = AESGCM(old_key_bytes)
        decrypted_bytes = aesgcm.decrypt(nonce, ciphertext, None)
        return decrypted_bytes.decode("utf-8")
    except Exception:
        # Return original if decryption fails (e.g. already migrated or plain text)
        return encrypted_str

def encrypt_val(plain_str, new_key_bytes):
    if not plain_str:
        return plain_str
    aesgcm = AESGCM(new_key_bytes)
    nonce = os.urandom(12)
    ciphertext = aesgcm.encrypt(nonce, plain_str.encode("utf-8"), None)
    return base64.b64encode(nonce + ciphertext).decode("utf-8")

def reencrypt_database(old_key_str, new_key_str):
    db_url = os.getenv("DATABASE_URL", "postgresql://ocr_user:ocr_secure_password@127.0.0.1:5432/ocr_db")
    print(f"--> Connecting to database to re-encrypt records...")
    
    old_key_bytes = get_key_bytes(old_key_str)
    new_key_bytes = get_key_bytes(new_key_str)
    
    conn = psycopg2.connect(db_url)
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            # 1. Re-encrypt documents table
            print("--> Processing 'documents' table...")
            cur.execute("""
                SELECT id, entered_id_number, entered_first_name, entered_surname, entered_dob, entered_sex, entered_expiry_date,
                       extracted_id_number, extracted_first_name, extracted_surname, extracted_dob, extracted_sex, extracted_expiry_date
                FROM documents
            """)
            docs = cur.fetchall()
            doc_count = 0
            for doc in docs:
                doc_id = doc["id"]
                updates = {}
                for col in ["entered_id_number", "entered_first_name", "entered_surname", "entered_dob", "entered_sex", "entered_expiry_date",
                            "extracted_id_number", "extracted_first_name", "extracted_surname", "extracted_dob", "extracted_sex", "extracted_expiry_date"]:
                    val = doc[col]
                    if val:
                        dec = decrypt_val(val, old_key_bytes)
                        enc = encrypt_val(dec, new_key_bytes)
                        updates[col] = enc
                
                if updates:
                    set_clause = ", ".join([f"{k} = %s" for k in updates.keys()])
                    values = list(updates.values()) + [doc_id]
                    cur.execute(f"UPDATE documents SET {set_clause} WHERE id = %s", values)
                    doc_count += 1
            print(f"   Re-encrypted {doc_count} documents.")

            # 2. Re-encrypt extracted_fields table
            print("--> Processing 'extracted_fields' table...")
            cur.execute("SELECT id, value FROM extracted_fields")
            fields = cur.fetchall()
            field_count = 0
            for field in fields:
                field_id = field["id"]
                val = field["value"]
                if val:
                    dec = decrypt_val(val, old_key_bytes)
                    enc = encrypt_val(dec, new_key_bytes)
                    cur.execute("UPDATE extracted_fields SET value = %s WHERE id = %s", (enc, field_id))
                    field_count += 1
            print(f"   Re-encrypted {field_count} extracted fields.")

            # 3. Re-encrypt feedbacks table
            print("--> Processing 'feedbacks' table...")
            cur.execute("SELECT id, original_value, corrected_value FROM feedbacks")
            fbs = cur.fetchall()
            fb_count = 0
            for fb in fbs:
                fb_id = fb["id"]
                orig_dec = decrypt_val(fb["original_value"], old_key_bytes)
                orig_enc = encrypt_val(orig_dec, new_key_bytes)
                corr_dec = decrypt_val(fb["corrected_value"], old_key_bytes)
                corr_enc = encrypt_val(corr_dec, new_key_bytes)
                cur.execute("UPDATE feedbacks SET original_value = %s, corrected_value = %s WHERE id = %s", (orig_enc, corr_enc, fb_id))
                fb_count += 1
            print(f"   Re-encrypted {fb_count} feedback records.")

        conn.commit()
        print("\n=== DATABASE RE-ENCRYPTION MIGRATION COMPLETE ===")
    except Exception as e:
        conn.rollback()
        print(f"Error during re-encryption: {e}")
    finally:
        conn.close()

if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("Usage: python reencrypt_database.py <OLD_KEY> <NEW_KEY>")
        sys.exit(1)
    old_key = sys.argv[1]
    new_key = sys.argv[2]
    reencrypt_database(old_key, new_key)
