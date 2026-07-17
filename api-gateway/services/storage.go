package services

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/encrypt"
)

var MinioClient *minio.Client
var BucketName string

func InitStorage() {
	endpoint := os.Getenv("STORAGE_ENDPOINT")
	accessKey := os.Getenv("STORAGE_ACCESS_KEY")
	secretKey := os.Getenv("STORAGE_SECRET_KEY")
	useSSLStr := os.Getenv("STORAGE_USE_SSL")
	BucketName = os.Getenv("STORAGE_BUCKET")

	if endpoint == "" {
		endpoint = "localhost:9000"
	}
	if accessKey == "" {
		accessKey = "minioadmin"
	}
	if secretKey == "" {
		secretKey = "minioadmin"
	}
	if BucketName == "" {
		BucketName = "documents"
	}

	useSSL := false
	if useSSLStr != "" {
		var err error
		useSSL, err = strconv.ParseBool(useSSLStr)
		if err != nil {
			log.Printf("Warning: invalid STORAGE_USE_SSL value '%s', defaulting to false", useSSLStr)
		}
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		log.Fatalf("Failed to initialize MinIO client: %v", err)
	}

	MinioClient = client
	log.Println("MinIO storage client initialized.")

	// Ensure bucket exists
	ctx := context.Background()
	exists, err := MinioClient.BucketExists(ctx, BucketName)
	if err != nil {
		log.Printf("Warning: Failed to check if bucket '%s' exists: %v", BucketName, err)
		return
	}

	if !exists {
		err = MinioClient.MakeBucket(ctx, BucketName, minio.MakeBucketOptions{})
		if err != nil {
			log.Fatalf("Failed to create bucket '%s': %v", BucketName, err)
		}
		log.Printf("Created storage bucket: %s", BucketName)
	} else {
		log.Printf("Storage bucket '%s' already exists.", BucketName)
	}
}

// UploadFile uploads an io.Reader to MinIO and returns the destination object name
func UploadFile(ctx context.Context, objectName string, reader io.Reader, size int64, contentType string) (string, error) {
	_, err := MinioClient.PutObject(ctx, BucketName, objectName, reader, size, minio.PutObjectOptions{
		ContentType:          contentType,
		ServerSideEncryption: encrypt.NewSSE(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload object: %w", err)
	}
	return objectName, nil
}

// GetPresignedURL generates a pre-signed URL for viewing/downloading the file (valid for 1 hour)
func GetPresignedURL(ctx context.Context, objectName string) (string, error) {
	reqParams := make(url.Values)
	presignedURL, err := MinioClient.PresignedGetObject(ctx, BucketName, objectName, time.Hour, reqParams)
	if err != nil {
		return "", fmt.Errorf("failed to get presigned URL: %w", err)
	}
	return presignedURL.String(), nil
}
