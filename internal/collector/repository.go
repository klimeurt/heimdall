package collector

import "time"

// Repository represents a GitHub repository for HTTP payload
type Repository struct {
	Org  string `json:"org"`
	Name string `json:"name"`
}

// ProcessedRepository represents a GitHub repository that has been cloned
type ProcessedRepository struct {
	Org         string    `json:"org"`
	Name        string    `json:"name"`
	ProcessedAt time.Time `json:"processed_at"`
	WorkerID    int       `json:"worker_id"`
	ClonePath   string    `json:"clone_path"`
}

// ScannedRepository represents a GitHub repository that has been scanned for secrets
type ScannedRepository struct {
	Org               string              `json:"org"`
	Name              string              `json:"name"`
	ProcessedAt       time.Time           `json:"processed_at"`
	ScannedAt         time.Time           `json:"scanned_at"`
	WorkerID          int                 `json:"worker_id"`
	ValidSecretsFound int                 `json:"valid_secrets_found"`
	ValidSecrets      []KingfisherFinding `json:"valid_secrets"`
	ScanStatus        string              `json:"scan_status"` // "success", "failed", "no_secrets", "timeout"
	ScanDuration      time.Duration       `json:"scan_duration"`
	ErrorMessage      string              `json:"error_message,omitempty"`
}

// KingfisherFinding represents a secret found by Kingfisher
type KingfisherFinding struct {
	SecretType  string `json:"secret_type"`
	Description string `json:"description"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Commit      string `json:"commit"`
	Confidence  string `json:"confidence"`
	Validated   bool   `json:"validated"`
	Service     string `json:"service,omitempty"`
}

// CleanupJob represents a cleanup job for removing cloned repositories
type CleanupJob struct {
	ClonePath   string    `json:"clone_path"`
	Org         string    `json:"org"`
	Name        string    `json:"name"`
	RequestedAt time.Time `json:"requested_at"`
	WorkerID    int       `json:"worker_id"`
}
