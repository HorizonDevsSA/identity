package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/redis/go-redis/v9"
)

var Rdb *redis.Client

const OCRQueueName = "ocr_jobs"
const TrainingQueueName = "training_jobs"

type OCRJobPayload struct {
	DocumentID string `json:"document_id"`
	TenantID   string `json:"tenant_id"`
	FilePath   string `json:"file_path"`
}

type TrainingJobPayload struct {
	JobID        string  `json:"job_id"`
	TenantID     string  `json:"tenant_id"`
	ProjectID    string  `json:"project_id"`
	DatasetID    string  `json:"dataset_id"`
	ModelID      string  `json:"model_id"`
	BaseModel    string  `json:"base_model"`
	Epochs       int     `json:"epochs"`
	LearningRate float64 `json:"learning_rate"`
}

func PublishTrainingJob(ctx context.Context, payload TrainingJobPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal training job payload: %w", err)
	}

	err = Rdb.RPush(ctx, TrainingQueueName, data).Err()
	if err != nil {
		return fmt.Errorf("failed to push training job to redis queue: %w", err)
	}

	log.Printf("Published training job to queue '%s' for job: %s", TrainingQueueName, payload.JobID)
	return nil
}

func InitRedis() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}

	Rdb = redis.NewClient(opt)

	// Test connection
	ctx := context.Background()
	_, err = Rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	log.Println("Redis client connected.")
}

func PublishOCRJob(ctx context.Context, payload OCRJobPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal job payload: %w", err)
	}

	// We use Redis RPush to a list to act as a FIFO queue
	err = Rdb.RPush(ctx, OCRQueueName, data).Err()
	if err != nil {
		return fmt.Errorf("failed to push job to redis queue: %w", err)
	}

	log.Printf("Published OCR job to queue '%s' for document: %s", OCRQueueName, payload.DocumentID)
	return nil
}
