import os
import boto3
from botocore.client import Config

def get_s3_client():
    endpoint = os.Getenv = os.getenv("STORAGE_ENDPOINT", "localhost:9000")
    access_key = os.getenv("STORAGE_ACCESS_KEY", "minioadmin")
    secret_key = os.getenv("STORAGE_SECRET_KEY", "minioadmin")
    use_ssl_str = os.getenv("STORAGE_USE_SSL", "false").lower()
    
    use_ssl = use_ssl_str == "true"
    
    # If endpoint doesn't start with http/https, prepend http://
    if not endpoint.startswith("http://") and not endpoint.startswith("https://"):
        protocol = "https://" if use_ssl else "http://"
        endpoint_url = f"{protocol}{endpoint}"
    else:
        endpoint_url = endpoint

    s3 = boto3.client(
        's3',
        endpoint_url=endpoint_url,
        aws_access_key_id=access_key,
        aws_secret_access_key=secret_key,
        config=Config(signature_version='s3v4'),
        verify=use_ssl
    )
    return s3

def download_file(object_name, local_dest_path):
    bucket_name = os.getenv("STORAGE_BUCKET", "documents")
    s3 = get_s3_client()
    try:
        s3.download_file(bucket_name, object_name, local_dest_path)
        print(f"Downloaded {object_name} to {local_dest_path}")
        return True
    except Exception as e:
        print(f"Error downloading file from S3: {e}")
        return False

def upload_file(local_file_path, object_name):
    bucket_name = os.getenv("STORAGE_BUCKET", "documents")
    s3 = get_s3_client()
    try:
        s3.upload_file(
            local_file_path, 
            bucket_name, 
            object_name,
            ExtraArgs={'ServerSideEncryption': 'AES256'}
        )
        print(f"Uploaded {local_file_path} to {object_name}")
        return True
    except Exception as e:
        print(f"Error uploading file to S3: {e}")
        return False

