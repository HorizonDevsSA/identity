import os
import json
import time
import tempfile
import redis
from db import (
    update_document_status, 
    save_ocr_prediction, 
    save_document_metadata, 
    get_entered_document_metadata,
    update_training_job_status,
    get_dataset_annotations,
    create_model_version,
    get_active_model_version,
    get_document_project_id,
    get_active_extraction_schemas,
    save_extracted_fields
)
from storage import download_file, upload_file
from ocr import run_ocr, extract_metadata_from_json
from extraction_engine import extract_fields_from_ocr

def clean_string(s):
    if not s:
        return ""
    import re
    s = str(s).lower()
    # Keep only alphanumeric characters
    s = re.sub(r'[^a-z0-9]', '', s)
    return s.strip()

def clean_date(d):
    if not d:
        return ""
    # Standardize delimiters to hyphen (e.g. 24/01/1994 -> 24-01-1994)
    return str(d).replace('/', '-').replace('.', '-').strip()

def compare_metadata(entered, extracted):
    if not entered:
        return "unverified", "{}"
    ent_doc_type = entered.get("entered_doc_type")
    ent_id_number = entered.get("entered_id_number")
    ent_first_name = entered.get("entered_first_name")
    ent_surname = entered.get("entered_surname")
    ent_date_of_issue = entered.get("entered_date_of_issue")
    ent_dob = entered.get("entered_dob")
    ent_expiry_date = entered.get("entered_expiry_date")
    ent_sex = entered.get("entered_sex")

    ext_doc_type = extracted.get("doc_type")
    ext_id_number = extracted.get("id_number")
    ext_first_name = extracted.get("first_name")
    ext_surname = extracted.get("surname")
    ext_date_of_issue = extracted.get("date_of_issue")
    ext_dob = extracted.get("dob")
    ext_expiry_date = extracted.get("expiry_date")
    ext_sex = extracted.get("sex")

    matches = {}

    # 1. Document Type (exact match of clean string)
    matches["doc_type"] = (clean_string(ent_doc_type) == clean_string(ext_doc_type))

    # 2. ID Number (match clean alphanumeric string)
    matches["id_number"] = (clean_string(ent_id_number) == clean_string(ext_id_number))

    # 3. First Name (exact or containment check)
    c_ent_fn = clean_string(ent_first_name)
    c_ext_fn = clean_string(ext_first_name)
    matches["first_name"] = (c_ent_fn == c_ext_fn) or (len(c_ent_fn) > 2 and len(c_ext_fn) > 2 and (c_ent_fn in c_ext_fn or c_ext_fn in c_ent_fn))

    # 4. Surname (exact or containment check)
    c_ent_sn = clean_string(ent_surname)
    c_ext_sn = clean_string(ext_surname)
    matches["surname"] = (c_ent_sn == c_ext_sn) or (len(c_ent_sn) > 2 and len(c_ext_sn) > 2 and (c_ent_sn in c_ext_sn or c_ext_sn in c_ent_sn))

    # 5. DOB (date compare after cleaning)
    matches["dob"] = (clean_date(ent_dob) == clean_date(ext_dob))

    # 6. Date of Issue (date compare after cleaning)
    matches["date_of_issue"] = (clean_date(ent_date_of_issue) == clean_date(ext_date_of_issue))

    # 7. Expiry Date (if applicable)
    # Expiry date only applies if document type is not national_id
    if ent_doc_type == "national_id":
        matches["expiry_date"] = True # Automatically pass for national IDs
    else:
        matches["expiry_date"] = (clean_date(ent_expiry_date) == clean_date(ext_expiry_date))

    # 8. Sex (if applicable / entered)
    if not ent_sex:
        matches["sex"] = True # Automatically pass if user didn't specify sex
    else:
        matches["sex"] = (clean_string(ent_sex) == clean_string(ext_sex))

    overall = all(matches.values())
    status = "verified" if overall else "failed_verification"
    return status, json.dumps(matches)

def process_training_job(payload, work_dir):
    job_id = payload.get("job_id")
    tenant_id = payload.get("tenant_id")
    project_id = payload.get("project_id")
    dataset_id = payload.get("dataset_id")
    model_id = payload.get("model_id")
    base_model = payload.get("base_model", "db_resnet50")
    epochs = int(payload.get("epochs", 10))
    lr = float(payload.get("learning_rate", 0.001))

    logs = []
    def log_and_save(message):
        print(message)
        logs.append(message)
        update_training_job_status(job_id, "running", "\n".join(logs) + "\n")

    log_and_save(f"Starting training job {job_id} for tenant {tenant_id}...")
    log_and_save(f"Using dataset ID: {dataset_id}")
    log_and_save(f"Model ID: {model_id} (Base: {base_model})")
    log_and_save(f"Hyperparameters: epochs={epochs}, learning_rate={lr}")

    try:
        # 1. Fetch dataset annotations
        log_and_save("Fetching annotations and images from dataset...")
        annotations = get_dataset_annotations(dataset_id)
        if not annotations:
            raise ValueError(f"No images or annotations found for dataset {dataset_id}. Cannot train model.")

        log_and_save(f"Found {len(annotations)} image/annotation records in dataset.")

        # 2. Verify download of images
        unique_images = list(set([ann["file_path"] for ann in annotations]))
        log_and_save(f"Downloading {len(unique_images)} unique images for verification...")
        
        for file_path in unique_images:
            filename = os.path.basename(file_path)
            local_path = os.path.join(work_dir, f"train_{job_id}_{filename}")
            
            # Download file from MinIO
            success = download_file(file_path, local_path)
            if success:
                log_and_save(f"Verified & downloaded dataset image: {filename}")
                # Clean up local verified image
                if os.path.exists(local_path):
                    os.remove(local_path)
            else:
                log_and_save(f"Warning: Failed to download dataset image {file_path}")

        # 3. Simulate training epochs
        import random
        for epoch in range(1, epochs + 1):
            log_and_save(f"Epoch {epoch}/{epochs} starting...")
            # Simulate processing time
            time.sleep(2) 
            # Simulate training loss decreasing and accuracy increasing
            train_loss = 0.5 / (epoch ** 0.5) + random.uniform(0.01, 0.05)
            val_loss = 0.6 / (epoch ** 0.5) + random.uniform(0.01, 0.05)
            accuracy = 0.70 + (0.28 * (epoch / epochs)) - random.uniform(0.0, 0.02)
            log_and_save(f"Epoch {epoch}/{epochs} - loss: {train_loss:.4f} - val_loss: {val_loss:.4f} - accuracy: {accuracy:.4f}")

        # 4. Generate dummy weights.pt and upload to storage
        final_accuracy = 0.70 + (0.28 * (epochs / epochs))
        log_and_save("Training completed. Generating model checkpoint...")
        
        local_weights_path = os.path.join(work_dir, f"weights_{job_id}.pt")
        with open(local_weights_path, "w") as f:
            f.write(f"MOCK WEIGHTS FOR MODEL {model_id} VERSION {job_id} WITH ACCURACY {final_accuracy:.4f}")

        version_str = f"v{int(time.time())}"
        object_name = f"models/{tenant_id}/{project_id}/{model_id}/{version_str}/weights.pt"
        
        log_and_save(f"Uploading model checkpoint to S3 object: {object_name}...")
        success = upload_file(local_weights_path, object_name)
        
        if os.path.exists(local_weights_path):
            os.remove(local_weights_path)
            
        if not success:
            raise RuntimeError("Failed to upload model weights to object storage.")

        # 5. Create ModelVersion in database
        log_and_save(f"Registering model version {version_str} in DB...")
        create_model_version(model_id, version_str, object_name, final_accuracy, job_id)
        
        # 6. Mark job completed
        log_and_save("Training job completed successfully!")
        update_training_job_status(job_id, "completed", "\n".join(logs) + "\n")
        
    except Exception as e:
        log_and_save(f"Error executing training job: {e}")
        update_training_job_status(job_id, "failed", "\n".join(logs) + "\n")

def main():
    redis_url = os.getenv("REDIS_URL", "redis://localhost:6379/0")
    print(f"Connecting to Redis at {redis_url}...")
    r = redis.Redis.from_url(redis_url, socket_timeout=60)
    print("Redis connected successfully.")
    
    queues = ["ocr_jobs", "training_jobs"]
    
    # Ensure temporary directory exists
    temp_dir_base = tempfile.gettempdir()
    work_dir = os.path.join(temp_dir_base, "ocr_worker_temp")
    os.makedirs(work_dir, exist_ok=True)
    print(f"Temporary work directory: {work_dir}")
    
    print(f"OCR Worker started, waiting for jobs in queues '{queues}'...")
    
    while True:
        try:
            # BLPOP blocks until a job is available in the queue
            # We use a non-zero timeout (e.g. 30s) to act as a keep-alive and prevent socket timeouts
            job = r.blpop(queues, timeout=30)
            if not job:
                continue
                
            q_name, payload_data = job
            q_name = q_name.decode("utf-8")
            payload = json.loads(payload_data.decode("utf-8"))
            
            if q_name == "training_jobs":
                print(f"Received training job: {payload.get('job_id')}")
                process_training_job(payload, work_dir)
                continue
                
            doc_id = payload.get("document_id")
            tenant_id = payload.get("tenant_id")
            file_path = payload.get("file_path")
            
            if not doc_id or not file_path:
                print(f"Invalid job payload received: {payload}")
                continue
                
            print(f"Processing OCR job for Document ID: {doc_id}, File: {file_path}")
            
            # Fetch active model version for this document's project
            active_model = None
            proj_id = get_document_project_id(doc_id)
            if proj_id:
                active_model = get_active_model_version(proj_id)
                if active_model:
                    print(f"Found active custom model '{active_model.get('name')}' (version {active_model.get('version')}) for project {proj_id}")
            
            # 1. Update status to 'processing'
            update_document_status(doc_id, "processing")
            
            # 2. Setup local paths
            filename = os.path.basename(file_path)
            local_input_path = os.path.join(work_dir, f"{doc_id}_{filename}")
            
            # 3. Download file from MinIO
            success = download_file(file_path, local_input_path)
            if not success:
                print(f"Failed to download document {doc_id}")
                update_document_status(doc_id, "failed")
                continue
                
            # 4. Run OCR Pipeline
            try:
                # Create a specific sub-temp directory for this job (e.g. for PDF page images)
                job_temp_dir = os.path.join(work_dir, f"job_{doc_id}")
                os.makedirs(job_temp_dir, exist_ok=True)
                
                raw_json, confidence = run_ocr(local_input_path, job_temp_dir, active_model_version=active_model)
                
                # 5. Save predictions to PostgreSQL
                save_ocr_prediction(doc_id, raw_json, confidence)
                
                # 6. Extract metadata from OCR output, compare with entered expected data, and save it
                try:
                    structured_json = json.loads(raw_json)
                    meta = extract_metadata_from_json(structured_json)
                    
                    # Fetch expected user-entered metadata
                    entered = get_entered_document_metadata(doc_id)
                    
                    # Run comparison / verification
                    status_ver, result_ver = compare_metadata(entered, meta)
                    
                    if status_ver == "verified":
                        from db import check_existing_verified_identity
                        fn = meta.get("first_name") or (entered.get("entered_first_name") if entered else None)
                        sn = meta.get("surname") or (entered.get("entered_surname") if entered else None)
                        dob = meta.get("dob") or (entered.get("entered_dob") if entered else None)
                        
                        if check_existing_verified_identity(tenant_id, fn, sn, dob, doc_id):
                            status_ver = "failed_verification"
                            res_map = json.loads(result_ver) if result_ver else {}
                            res_map["duplicate_verification"] = False
                            res_map["message"] = "Identity already verified under another active document"
                            result_ver = json.dumps(res_map)
                    
                    save_document_metadata(
                        doc_id,
                        meta.get("doc_type"),
                        meta.get("id_number"),
                        meta.get("first_name"),
                        meta.get("surname"),
                        meta.get("date_of_issue"),
                        meta.get("dob"),
                        meta.get("expiry_date"),
                        meta.get("sex"),
                        status_ver,
                        result_ver
                    )
                    print(f"Extracted document metadata: {meta}")
                    print(f"Verification completed: status={status_ver}, results={result_ver}")

                    # ZWC Blockchain Integration: Whitelist and register alias
                    if status_ver == "verified":
                        try:
                            from db import get_blockchain_fields
                            from blockchain import register_user_on_chain
                            import threading

                            bc_fields = get_blockchain_fields(doc_id)
                            if bc_fields and bc_fields.get("wallet_address"):
                                print(f"[ZWC-INTEGRATION] Auto-verification triggered ZWC registration for document {doc_id}")
                                w_addr = bc_fields["wallet_address"]
                                a_type = bc_fields.get("alias_type")
                                a_val = bc_fields.get("alias_value")
                                t = threading.Thread(target=register_user_on_chain, args=(w_addr, a_type, a_val))
                                t.start()
                        except Exception as ex:
                            print(f"[ZWC-ERROR] Failed to start blockchain registration thread: {ex}")
                    
                    # 6.5 Run layout-aware custom schema extraction (Phase 4)
                    try:
                        doc_type = meta.get("doc_type", "unknown")
                        schemas = get_active_extraction_schemas(tenant_id, doc_type)
                        if schemas:
                            print(f"Found {len(schemas)} active extraction schemas for tenant {tenant_id} and doc_type {doc_type}")
                            extracted_fields = []
                            for schema in schemas:
                                fields_def = json.loads(schema["fields"])
                                res = extract_fields_from_ocr(raw_json, fields_def)
                                if res:
                                    print(f"Extracted {len(res)} fields for schema {schema['name']}: {res}")
                                    extracted_fields.extend(res)
                            
                            if extracted_fields:
                                save_extracted_fields(doc_id, extracted_fields)
                        else:
                            print(f"No active extraction schemas found for tenant {tenant_id} and doc_type {doc_type}")
                    except Exception as ex:
                        print(f"Failed to perform schema-based field extraction: {ex}")
                except Exception as ex:
                    print(f"Failed to extract, verify, or save document metadata: {ex}")
                
                # 7. Update status to 'completed'
                update_document_status(doc_id, "completed")
                print(f"Successfully processed Document ID: {doc_id} with confidence {confidence:.4f}")
                
            except Exception as e:
                print(f"Error executing OCR pipeline for Document {doc_id}: {e}")
                update_document_status(doc_id, "failed")
                
            finally:
                # Cleanup downloaded and generated temp files
                if os.path.exists(local_input_path):
                    os.remove(local_input_path)
                
                job_temp_dir = os.path.join(work_dir, f"job_{doc_id}")
                if os.path.exists(job_temp_dir):
                    for f in os.listdir(job_temp_dir):
                        try:
                            os.remove(os.path.join(job_temp_dir, f))
                        except:
                            pass
                    try:
                        os.rmdir(job_temp_dir)
                    except:
                        pass
                        
        except Exception as e:
            print(f"Error in main polling loop: {e}")
            time.sleep(5) # Cooldown before trying again if Redis connection dropped

if __name__ == "__main__":
    main()
