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
	ValidSecrets      []TruffleHogFinding `json:"valid_secrets"`
	ScanStatus        string              `json:"scan_status"` // "success", "failed", "no_secrets", "timeout"
	ScanDuration      time.Duration       `json:"scan_duration"`
	ErrorMessage      string              `json:"error_message,omitempty"`
}

// TruffleHogFinding represents a secret found by TruffleHog
type TruffleHogFinding struct {
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

// OSVScanResult represents a vulnerability found by OSV Scanner
type OSVScanResult struct {
	ID          string   `json:"id"`
	Package     string   `json:"package"`
	Version     string   `json:"version"`
	Ecosystem   string   `json:"ecosystem"`
	Severity    string   `json:"severity"`
	Summary     string   `json:"summary"`
	Details     string   `json:"details"`
	Aliases     []string `json:"aliases"`
	Fixed       string   `json:"fixed,omitempty"`
	CVE         string   `json:"cve,omitempty"`
	CWE         []string `json:"cwe,omitempty"`
	PackageFile string   `json:"package_file"`
}

// OSVScannedRepository represents a repository that has been scanned for vulnerabilities
type OSVScannedRepository struct {
	Org                  string          `json:"org"`
	Name                 string          `json:"name"`
	ProcessedAt          time.Time       `json:"processed_at"`
	ScannedAt            time.Time       `json:"scanned_at"`
	WorkerID             int             `json:"worker_id"`
	VulnerabilitiesFound int             `json:"vulnerabilities_found"`
	Vulnerabilities      []OSVScanResult `json:"vulnerabilities"`
	ScanStatus           string          `json:"scan_status"` // "success", "failed", "no_vulnerabilities", "timeout"
	ScanDuration         time.Duration   `json:"scan_duration"`
	ErrorMessage         string          `json:"error_message,omitempty"`
}

// ScanCoordinationMessage represents a message sent to the coordinator when a scanner completes
type ScanCoordinationMessage struct {
	ClonePath    string        `json:"clone_path"`
	Org          string        `json:"org"`
	Name         string        `json:"name"`
	ScannerType  string        `json:"scanner_type"` // "scanner-trufflehog" or "scanner-osv"
	CompletedAt  time.Time     `json:"completed_at"`
	WorkerID     int           `json:"worker_id"`
	ScanStatus   string        `json:"scan_status"`
	ScanDuration time.Duration `json:"scan_duration"`
}

// CoordinationState represents the state of a repository being scanned by multiple scanners
type CoordinationState struct {
	ClonePath          string    `json:"clone_path"`
	Org                string    `json:"org"`
	Name               string    `json:"name"`
	StartedAt          time.Time `json:"started_at"`
	TruffleHogComplete bool      `json:"trufflehog_complete"`
	OSVComplete        bool      `json:"osv_complete"`
	TruffleHogStatus   string    `json:"trufflehog_status,omitempty"`
	OSVStatus          string    `json:"osv_status,omitempty"`
}
