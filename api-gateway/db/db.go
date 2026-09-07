package db

import (
	"fmt"
	"log"
	"os"

	"api-gateway/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func Connect() {
	host := os.Getenv("DB_HOST")
	port := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbname := os.Getenv("DB_NAME")
	sslmode := os.Getenv("DB_SSLMODE")

	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "5432"
	}
	if user == "" {
		user = "ocr_user"
	}
	if password == "" {
		password = "ocr_secure_password"
	}
	if dbname == "" {
		dbname = "ocr_db"
	}
	if sslmode == "" {
		sslmode = "disable"
	}

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		host, user, password, dbname, port, sslmode)

	database, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	log.Println("Database connection established.")
	DB = database

	// Run migrations
	err = DB.AutoMigrate(
		&models.Tenant{},
		&models.User{},
		&models.Project{},
		&models.Document{},
		&models.Prediction{},
		&models.Dataset{},
		&models.DatasetImage{},
		&models.Annotation{},
		&models.Model{},
		&models.ModelVersion{},
		&models.TrainingJob{},
		&models.ExtractionSchema{},
		&models.ExtractedField{},
		&models.Feedback{},
		&models.AuditLog{},
		&models.OTPVerification{},
		&models.DeviceToken{},
		&models.NotificationPreference{},
		&models.Notification{},
		&models.Device{},
	)
	if err != nil {
		log.Fatalf("Failed to auto-migrate schemas: %v", err)
	}
	log.Println("Database migration completed.")
}
