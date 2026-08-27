import os
import psycopg2
from psycopg2.extras import RealDictCursor
from encryption import encrypt, decrypt
import datetime

def get_db_connection():
    host = os.getenv("DB_HOST", "localhost")
    port = os.getenv("DB_PORT", "5432")
    user = os.getenv("DB_USER", "ocr_user")
    password = os.getenv("DB_PASSWORD", "ocr_secure_password")
    dbname = os.getenv("DB_NAME", "ocr_db")
    
    conn = psycopg2.connect(
        host=host,
        port=port,
        user=user,
        password=password,
        dbname=dbname
    )
    return conn

def update_document_status(doc_id, status):
    conn = get_db_connection()
    try:
        with conn.cursor() as cur:
            cur.execute(
                "UPDATE documents SET status = %s, updated_at = NOW() WHERE id = %s",
                (status, doc_id)
            )
        conn.commit()
    except Exception as e:
        print(f"Database error updating status: {e}")
    finally:
        conn.close()

def save_ocr_prediction(doc_id, raw_json, confidence):
    conn = get_db_connection()
    try:
        with conn.cursor() as cur:
            # First check if prediction exists (upsert)
            cur.execute("SELECT id FROM predictions WHERE document_id = %s", (doc_id,))
            exists = cur.fetchone()
            
            if exists:
                cur.execute(
                    "UPDATE predictions SET raw_json = %s, confidence = %s, created_at = NOW() WHERE document_id = %s",
                    (raw_json, confidence, doc_id)
                )
            else:
                cur.execute(
                    "INSERT INTO predictions (id, document_id, raw_json, confidence, created_at) VALUES (gen_random_uuid(), %s, %s, %s, NOW())",
                    (doc_id, raw_json, confidence)
                )
        conn.commit()
    except Exception as e:
        print(f"Database error saving prediction: {e}")
    finally:
        conn.close()

def save_document_metadata(doc_id, doc_type, id_number, first_name, surname, date_of_issue, dob, expiry_date, sex, verification_status=None, verification_result=None):
    conn = get_db_connection()
    try:
        with conn.cursor() as cur:
            cur.execute(
                """
                UPDATE documents 
                SET extracted_doc_type = %s, extracted_id_number = %s, extracted_first_name = %s, extracted_surname = %s, 
                    extracted_date_of_issue = %s, extracted_dob = %s, extracted_expiry_date = %s, extracted_sex = %s,
                    verification_status = %s, verification_result = %s, updated_at = NOW() 
                WHERE id = %s
                """,
                (
                    doc_type, 
                    encrypt(id_number), 
                    encrypt(first_name), 
                    encrypt(surname), 
                    encrypt(date_of_issue), 
                    encrypt(dob), 
                    encrypt(expiry_date), 
                    encrypt(sex), 
                    verification_status, 
                    verification_result, 
                    doc_id
                )
            )
        conn.commit()
    except Exception as e:
        print(f"Database error saving document metadata: {e}")
    finally:
        conn.close()

def get_entered_document_metadata(doc_id):
    conn = get_db_connection()
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            cur.execute(
                """
                SELECT entered_doc_type, entered_id_number, entered_first_name, entered_surname, 
                       entered_date_of_issue, entered_dob, entered_expiry_date, entered_sex
                FROM documents 
                WHERE id = %s
                """,
                (doc_id,)
            )
            row = cur.fetchone()
            if row:
                for k in ["entered_id_number", "entered_first_name", "entered_surname", "entered_date_of_issue", "entered_dob", "entered_expiry_date", "entered_sex"]:
                    if row.get(k) is not None:
                        row[k] = decrypt(row[k])
            return row
    except Exception as e:
        print(f"Database error fetching entered metadata: {e}")
        return None
    finally:
        conn.close()

def update_training_job_status(job_id, status, logs):
    conn = get_db_connection()
    try:
        with conn.cursor() as cur:
            cur.execute(
                "UPDATE training_jobs SET status = %s, logs = %s, updated_at = NOW() WHERE id = %s",
                (status, logs, job_id)
            )
        conn.commit()
    except Exception as e:
        print(f"Database error updating training job: {e}")
    finally:
        conn.close()

def get_dataset_annotations(dataset_id):
    conn = get_db_connection()
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            cur.execute(
                """
                SELECT di.id as image_id, di.file_path, di.filename, a.bounding_box, a.label
                FROM dataset_images di
                LEFT JOIN annotations a ON a.dataset_image_id = di.id
                WHERE di.dataset_id = %s AND di.deleted_at IS NULL
                """,
                (dataset_id,)
            )
            return cur.fetchall()
    except Exception as e:
        print(f"Database error fetching dataset annotations: {e}")
        return []
    finally:
        conn.close()

def create_model_version(model_id, version, file_path, accuracy, job_id):
    conn = get_db_connection()
    try:
        with conn.cursor() as cur:
            cur.execute(
                """
                INSERT INTO model_versions (id, model_id, version, file_path, status, accuracy, training_job_id, created_at, updated_at)
                VALUES (gen_random_uuid(), %s, %s, %s, 'inactive', %s, %s, NOW(), NOW())
                """ ,
                (model_id, version, file_path, accuracy, job_id)
            )
        conn.commit()
    except Exception as e:
        print(f"Database error creating model version: {e}")
    finally:
        conn.close()

def get_active_model_version(project_id):
    conn = get_db_connection()
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            # Query models in the project, find if any has an active model version
            cur.execute(
                """
                SELECT mv.id, mv.file_path, mv.version, m.name
                FROM model_versions mv
                JOIN models m ON m.id = mv.model_id
                WHERE m.project_id = %s AND mv.status = 'active' AND m.deleted_at IS NULL AND mv.deleted_at IS NULL
                LIMIT 1
                """,
                (project_id,)
            )
            return cur.fetchone()
    except Exception as e:
        print(f"Database error getting active model version: {e}")
        return None
    finally:
        conn.close()

def get_document_project_id(doc_id):
    conn = get_db_connection()
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT project_id FROM documents WHERE id = %s", (doc_id,))
            res = cur.fetchone()
            if res:
                return res[0]
            return None
    except Exception as e:
        print(f"Database error fetching document project_id: {e}")
        return None
    finally:
        conn.close()

def get_active_extraction_schemas(tenant_id, document_type):
    conn = get_db_connection()
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            cur.execute(
                """
                SELECT id, name, document_type, fields, tenant_id, project_id
                FROM extraction_schemas
                WHERE tenant_id = %s AND document_type = %s AND deleted_at IS NULL
                """,
                (tenant_id, document_type)
            )
            return cur.fetchall()
    except Exception as e:
        print(f"Database error getting active extraction schemas: {e}")
        return []
    finally:
        conn.close()

def save_extracted_fields(doc_id, extracted_fields_list):
    conn = get_db_connection()
    try:
        with conn.cursor() as cur:
            # Delete existing
            cur.execute("DELETE FROM extracted_fields WHERE document_id = %s", (doc_id,))
            
            # Insert new
            for field in extracted_fields_list:
                cur.execute(
                    """
                    INSERT INTO extracted_fields (id, document_id, key, value, confidence, bounding_box, created_at, updated_at)
                    VALUES (gen_random_uuid(), %s, %s, %s, %s, %s, NOW(), NOW())
                    """,
                    (doc_id, field["key"], encrypt(field["value"]), field["confidence"], field["bounding_box"])
                )
        conn.commit()
    except Exception as e:
        print(f"Database error saving extracted fields: {e}")
    finally:
        conn.close()

def normalize_string(s):
    if not s:
        return ""
    return "".join(s.lower().split())

def is_expired(expiry_str):
    if not expiry_str:
        return False
    formats = ["%d-%m-%Y", "%d/%m/%Y", "%Y-%m-%d"]
    expiry_str = expiry_str.strip()
    for fmt in formats:
        try:
            t = datetime.datetime.strptime(expiry_str, fmt).date()
            return t < datetime.date.today()
        except ValueError:
            continue
    return False

def check_existing_verified_identity(tenant_id, first_name, surname, dob, exclude_doc_id=None):
    if not first_name or not surname or not dob:
        return False

    norm_first = normalize_string(first_name)
    norm_surname = normalize_string(surname)
    norm_dob = normalize_string(dob)

    conn = get_db_connection()
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            cur.execute(
                """
                SELECT id, extracted_doc_type, extracted_first_name, extracted_surname, extracted_dob, extracted_expiry_date
                FROM documents 
                WHERE tenant_id = %s AND verification_status = 'verified'
                """,
                (tenant_id,)
            )
            rows = cur.fetchall()
            for row in rows:
                if exclude_doc_id and str(row["id"]) == str(exclude_doc_id):
                    continue

                # Decrypt values
                doc_first = decrypt(row["extracted_first_name"])
                doc_surname = decrypt(row["extracted_surname"])
                doc_dob = decrypt(row["extracted_dob"])
                doc_type = row["extracted_doc_type"]
                doc_expiry = decrypt(row["extracted_expiry_date"])

                if (normalize_string(doc_first) == norm_first and
                    normalize_string(doc_surname) == norm_surname and
                    normalize_string(doc_dob) == norm_dob):
                    
                    # If non-national ID and expired, let it pass
                    if doc_type != "national_id" and is_expired(doc_expiry):
                        continue
                    
                    return True
            return False
    except Exception as e:
        print(f"Database error checking duplicate identity: {e}")
        return False
    finally:
        conn.close()

def get_blockchain_fields(doc_id):
    conn = get_db_connection()
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            cur.execute(
                """
                SELECT wallet_address, alias_type, alias_value
                FROM documents 
                WHERE id = %s
                """,
                (doc_id,)
            )
            return cur.fetchone()
    except Exception as e:
        print(f"Database error fetching blockchain fields: {e}")
        return None
    finally:
        conn.close()



