package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Tenant struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name      string         `gorm:"not null" json:"name"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type User struct {
	ID           uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Email        string         `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string         `gorm:"not null" json:"-"`
	Role         string         `gorm:"default:'user'" json:"role"` // 'admin', 'user', 'reviewer'
	TenantID     uuid.UUID      `gorm:"type:uuid;index" json:"tenant_id"`
	Tenant       Tenant         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

type Project struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name      string         `gorm:"not null" json:"name"`
	TenantID  uuid.UUID      `gorm:"type:uuid;index" json:"tenant_id"`
	Tenant    Tenant         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type DocumentType string

const (
	DocTypePassport       DocumentType = "passport"
	DocTypeNationalID     DocumentType = "national_id"
	DocTypeDriversLicence DocumentType = "drivers_licence"
	DocTypeUnknown        DocumentType = "unknown"
)

type Document struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Filename    string         `gorm:"not null" json:"filename"`
	FilePath    string         `gorm:"not null" json:"file_path"` // Path in object storage
	Status      string         `gorm:"default:'pending'" json:"status"` // 'pending', 'processing', 'completed', 'failed'
	TenantID    uuid.UUID      `gorm:"type:uuid;index" json:"tenant_id"`
	Tenant      Tenant         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	ProjectID   uuid.UUID      `gorm:"type:uuid;index" json:"project_id"`
	Project     Project        `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	// Entered metadata (from user upload form)
	EnteredDocType     *DocumentType    `json:"entered_doc_type"`
	EnteredIDNumber    *EncryptedString `json:"entered_id_number"`
	EnteredFirstName   *EncryptedString `json:"entered_first_name"`
	EnteredSurname     *EncryptedString `json:"entered_surname"`
	EnteredDateOfIssue *EncryptedString `json:"entered_date_of_issue"`
	EnteredDOB         *EncryptedString `json:"entered_dob"`
	EnteredExpiryDate  *EncryptedString `json:"entered_expiry_date"`
	EnteredSex         *EncryptedString `json:"entered_sex"`

	// Extracted metadata (from OCR processing)
	ExtractedDocType     *DocumentType    `json:"extracted_doc_type"`
	ExtractedIDNumber    *EncryptedString `json:"extracted_id_number"`
	ExtractedFirstName   *EncryptedString `json:"extracted_first_name"`
	ExtractedSurname     *EncryptedString `json:"extracted_surname"`
	ExtractedDateOfIssue *EncryptedString `json:"extracted_date_of_issue"`
	ExtractedDOB         *EncryptedString `json:"extracted_dob"`
	ExtractedExpiryDate  *EncryptedString `json:"extracted_expiry_date"`
	ExtractedSex         *EncryptedString `json:"extracted_sex"`

	// Verification status & results
	VerificationStatus string         `gorm:"default:'unverified'" json:"verification_status"` // 'unverified', 'verified', 'failed_verification'
	VerificationResult *string        `gorm:"type:text" json:"verification_result"` // JSON details of matching fields

	// Blockchain registration target
	WalletAddress *string `json:"wallet_address"`
	AliasType     *string `json:"alias_type"`
	AliasValue    *string `json:"alias_value"`

	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type Prediction struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	DocumentID uuid.UUID `gorm:"type:uuid;uniqueIndex" json:"document_id"`
	RawJSON    string    `gorm:"type:text" json:"raw_json"` // Store OCR results as text JSON
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}

type Dataset struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name        string         `gorm:"not null" json:"name"`
	Description string         `json:"description"`
	TenantID    uuid.UUID      `gorm:"type:uuid;index" json:"tenant_id"`
	Tenant      Tenant         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	ProjectID   uuid.UUID      `gorm:"type:uuid;index" json:"project_id"`
	Project     Project        `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	Images      []DatasetImage `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"images,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type DatasetImage struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	DatasetID   uuid.UUID      `gorm:"type:uuid;index" json:"dataset_id"`
	FilePath    string         `gorm:"not null" json:"file_path"` // Path in object storage
	Filename    string         `gorm:"not null" json:"filename"`
	Status      string         `gorm:"default:'unlabeled'" json:"status"` // 'unlabeled', 'labeled', 'reviewed'
	Annotations []Annotation   `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"annotations,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type Annotation struct {
	ID             uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	DatasetImageID uuid.UUID      `gorm:"type:uuid;index" json:"dataset_image_id"`
	BoundingBox    string         `gorm:"type:text;not null" json:"bounding_box"` // JSON coordinates string e.g. [[xmin, ymin], [xmax, ymax]]
	Label          string         `gorm:"not null" json:"label"`                  // Text value or class
	CreatedBy      uuid.UUID      `gorm:"type:uuid;index" json:"created_by"`      // User ID who created it
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

type Model struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name        string         `gorm:"not null" json:"name"`
	Description string         `json:"description"`
	BaseModel   string         `gorm:"not null;default:'db_resnet50'" json:"base_model"` // e.g. "db_resnet50", "crnn_vgg16_bn"
	TenantID    uuid.UUID      `gorm:"type:uuid;index" json:"tenant_id"`
	Tenant      Tenant         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	ProjectID   uuid.UUID      `gorm:"type:uuid;index" json:"project_id"`
	Project     Project        `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	Versions    []ModelVersion `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"versions,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type ModelVersion struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	ModelID       uuid.UUID      `gorm:"type:uuid;index" json:"model_id"`
	Version       string         `gorm:"not null" json:"version"` // e.g. "v1.0.0"
	FilePath      string         `gorm:"not null" json:"file_path"` // Path in MinIO to model weights
	Status        string         `gorm:"default:'inactive'" json:"status"` // 'active', 'inactive', 'deprecated'
	Accuracy      float64        `json:"accuracy"`
	TrainingJobID *uuid.UUID     `gorm:"type:uuid" json:"training_job_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

type TrainingJob struct {
	ID           uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID     uuid.UUID      `gorm:"type:uuid;index" json:"tenant_id"`
	Tenant       Tenant         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	ProjectID    uuid.UUID      `gorm:"type:uuid;index" json:"project_id"`
	Project      Project        `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	DatasetID    uuid.UUID      `gorm:"type:uuid;index" json:"dataset_id"`
	Dataset      Dataset        `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	ModelID      uuid.UUID      `gorm:"type:uuid;index" json:"model_id"`
	Model        Model          `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	BaseModel    string         `gorm:"not null" json:"base_model"`
	Status       string         `gorm:"default:'pending'" json:"status"` // 'pending', 'running', 'completed', 'failed'
	Epochs       int            `gorm:"default:10" json:"epochs"`
	LearningRate float64        `gorm:"default:0.001" json:"learning_rate"`
	Logs         string         `gorm:"type:text" json:"logs"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

type ExtractionSchema struct {
	ID           uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	Name         string         `gorm:"not null" json:"name"`
	DocumentType string         `gorm:"not null" json:"document_type"` // e.g. "invoice", "passport", "national_id"
	Description  string         `json:"description"`
	Fields       string         `gorm:"type:text;not null" json:"fields"` // JSON array string of field definitions
	TenantID     uuid.UUID      `gorm:"type:uuid;index" json:"tenant_id"`
	Tenant       Tenant         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	ProjectID    uuid.UUID      `gorm:"type:uuid;index" json:"project_id"`
	Project      Project        `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

type ExtractedField struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	DocumentID  uuid.UUID      `gorm:"type:uuid;index" json:"document_id"`
	Document    Document       `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	Key         string         `gorm:"not null" json:"key"`
	Value       EncryptedString `json:"value"`
	Confidence  float64        `json:"confidence"`
	BoundingBox string         `json:"bounding_box"` // coordinates e.g. [[xmin, ymin], [xmax, ymax]]
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type Feedback struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	DocumentID     uuid.UUID `gorm:"type:uuid;index" json:"document_id"`
	Document       Document  `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	FieldName      string          `gorm:"not null" json:"field_name"`
	OriginalValue  EncryptedString `json:"original_value"`
	CorrectedValue EncryptedString `json:"corrected_value"`
	ReviewedBy     uuid.UUID `gorm:"type:uuid;index" json:"reviewed_by"`
	User           User      `gorm:"foreignKey:ReviewedBy;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;" json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

type AuditLog struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	TenantID  uuid.UUID `gorm:"type:uuid;index" json:"tenant_id"`
	UserID    uuid.UUID `gorm:"type:uuid;index" json:"user_id"`
	User      User      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	Action    string    `gorm:"not null" json:"action"`
	Details   string    `gorm:"type:text" json:"details"`
	IPAddress string    `json:"ip_address"`
	CreatedAt time.Time `json:"created_at"`
}



