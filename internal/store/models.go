package store

import "time"

const (
	PrivacyModeStandard    = "standard"
	PrivacyModeStrict      = "strict"
	RoleUser               = "user"
	RoleAdmin              = "admin"
	ContactStatusNew       = "new"
	ContactStatusContacted = "contacted"
	ContactStatusQualified = "qualified"
	ContactStatusClosed    = "closed"
)

type User struct {
	ID           uint             `gorm:"primaryKey"`
	Name         string           `gorm:"size:128;uniqueIndex;not null"`
	Email        *string          `gorm:"size:255;uniqueIndex"`
	PasswordHash string           `gorm:"type:text"`
	GitHubID     *string          `gorm:"size:128;uniqueIndex"`
	AvatarURL    string           `gorm:"type:text"`
	Role         string           `gorm:"size:32;not null;default:user"`
	Balance      int64            `gorm:"not null;default:0"`
	Plan         string           `gorm:"size:64;not null;default:free"`
	IsActive     bool             `gorm:"not null;default:true"`
	Projects     []Project        `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	APIKeys      []APIKey         `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Sessions     []ConsoleSession `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Project struct {
	ID                    uint     `gorm:"primaryKey"`
	Name                  string   `gorm:"size:128;not null"`
	Description           string   `gorm:"type:text"`
	Mode                  string   `gorm:"size:32;not null;default:hybrid"`
	PrivacyMode           string   `gorm:"size:16;not null;default:standard"`
	WebSearchEnabled      bool     `gorm:"not null;default:true"`
	AggressiveCompression bool     `gorm:"not null;default:false"`
	UserID                uint     `gorm:"index;not null"`
	User                  User     `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	IsActive              bool     `gorm:"not null;default:true"`
	Sources               []Source `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	APIKeys               []APIKey `gorm:"constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type Source struct {
	ID              uint    `gorm:"primaryKey"`
	Name            string  `gorm:"size:128;not null"`
	Kind            string  `gorm:"size:32;not null;default:byok"`
	Provider        string  `gorm:"size:64;not null;default:custom"`
	BaseURL         string  `gorm:"type:text;not null"`
	APIKey          string  `gorm:"type:text;not null"`
	SupportedModels string  `gorm:"type:text"`
	UserID          uint    `gorm:"index;not null"`
	User            User    `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	ProjectID       uint    `gorm:"index;not null"`
	Project         Project `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	IsActive        bool    `gorm:"not null;default:true"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ManagedModel struct {
	ID              uint   `gorm:"primaryKey"`
	GroupName       string `gorm:"size:64;index;not null;default:default"`
	DisplayName     string `gorm:"size:128;not null"`
	Description     string `gorm:"type:text"`
	ModelIdentifier string `gorm:"size:255;uniqueIndex;not null"`
	Provider        string `gorm:"size:64;not null;default:custom"`
	Icon            string `gorm:"size:64;not null;default:cpu"`
	SourceType      string `gorm:"size:32;not null;default:official"`
	UpstreamName    string `gorm:"size:255;uniqueIndex;not null"`
	BaseURL         string `gorm:"type:text;not null"`
	APIKey          string `gorm:"type:text;not null"`
	RouteStrategy   string `gorm:"size:32;not null;default:fixed"`
	SortOrder       int    `gorm:"not null;default:100"`
	Enabled         bool   `gorm:"not null;default:true"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type APIKey struct {
	ID             uint   `gorm:"primaryKey"`
	Name           string `gorm:"size:128;not null"`
	Key            string `gorm:"size:255;uniqueIndex;not null"`
	UserID         uint   `gorm:"index;not null"`
	User           User   `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	ProjectID      *uint  `gorm:"index"`
	Project        *Project
	AllowWebSearch bool `gorm:"not null;default:false"`
	IsActive       bool `gorm:"not null;default:true"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ConsoleSession struct {
	ID         uint      `gorm:"primaryKey"`
	UserID     uint      `gorm:"index;not null"`
	User       User      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	TokenHash  string    `gorm:"size:128;uniqueIndex;not null"`
	Provider   string    `gorm:"size:32;not null;default:email"`
	ExpiresAt  time.Time `gorm:"index;not null"`
	LastSeenAt time.Time
	CreatedAt  time.Time `gorm:"index"`
	UpdatedAt  time.Time
}

type ConsoleEmailCode struct {
	ID        uint       `gorm:"primaryKey"`
	Email     string     `gorm:"size:255;index;not null"`
	CodeHash  string     `gorm:"size:128;not null"`
	ExpiresAt time.Time  `gorm:"index;not null"`
	UsedAt    *time.Time `gorm:"index"`
	CreatedAt time.Time  `gorm:"index"`
	UpdatedAt time.Time
}

type ContactRequest struct {
	ID        uint          `gorm:"primaryKey"`
	Name      string        `gorm:"size:128;not null"`
	Email     string        `gorm:"size:255;index;not null"`
	Company   string        `gorm:"size:255"`
	Role      string        `gorm:"size:128"`
	Owner     string        `gorm:"size:128;index"`
	Message   string        `gorm:"type:text;not null"`
	Status    string        `gorm:"size:32;not null;default:new;index"`
	Source    string        `gorm:"size:64;not null;default:website"`
	Notes     []ContactNote `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	CreatedAt time.Time     `gorm:"index"`
	UpdatedAt time.Time
}

type ContactNote struct {
	ID               uint           `gorm:"primaryKey"`
	ContactRequestID uint           `gorm:"index;not null"`
	ContactRequest   ContactRequest `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Author           string         `gorm:"size:128;not null"`
	Body             string         `gorm:"type:text;not null"`
	CreatedAt        time.Time      `gorm:"index"`
	UpdatedAt        time.Time
}

type UsageLog struct {
	ID               uint      `gorm:"primaryKey"`
	RequestID        string    `gorm:"size:64;index"`
	UserID           uint      `gorm:"index;not null"`
	APIKeyID         uint      `gorm:"index;not null"`
	Endpoint         string    `gorm:"size:64;index;not null"`
	Model            string    `gorm:"size:255;index"`
	UpstreamName     string    `gorm:"size:128"`
	StatusCode       int       `gorm:"not null"`
	PromptTokens     int64     `gorm:"not null;default:0"`
	CompletionTokens int64     `gorm:"not null;default:0"`
	TotalTokens      int64     `gorm:"not null;default:0"`
	BaselineTokens   int64     `gorm:"not null;default:0"`
	SavedTokens      int64     `gorm:"not null;default:0"`
	CreditsCharged   int64     `gorm:"not null;default:0"`
	BaselineCost     int64     `gorm:"not null;default:0"`
	SavedCost        int64     `gorm:"not null;default:0"`
	RequestKind      string    `gorm:"size:32;not null"`
	Stream           bool      `gorm:"not null;default:false"`
	ToolUsed         bool      `gorm:"not null;default:false"`
	CacheHit         bool      `gorm:"not null;default:false"`
	DurationMs       int64     `gorm:"not null;default:0"`
	Success          bool      `gorm:"not null;default:false"`
	ErrorMessage     string    `gorm:"type:text"`
	CreatedAt        time.Time `gorm:"index"`
}

type RouteTrace struct {
	ID                   uint      `gorm:"primaryKey"`
	RequestID            string    `gorm:"size:64;index"`
	UserID               uint      `gorm:"index;not null"`
	APIKeyID             uint      `gorm:"index;not null"`
	Endpoint             string    `gorm:"size:64;index;not null"`
	OriginalModel        string    `gorm:"size:255;index"`
	FinalModel           string    `gorm:"size:255;index"`
	RouteTier            string    `gorm:"size:64;index"`
	IntentClass          string    `gorm:"size:64;index"`
	IntentConfidence     float64   `gorm:"not null;default:0"`
	EstimatedInputChars  int       `gorm:"not null;default:0"`
	EstimatedInputTokens int       `gorm:"not null;default:0"`
	SearchApplied        bool      `gorm:"not null;default:false"`
	CompressionApplied   bool      `gorm:"not null;default:false"`
	CacheHit             bool      `gorm:"not null;default:false"`
	DecisionReason       string    `gorm:"type:text"`
	IntentReasons        string    `gorm:"type:text"`
	CreatedAt            time.Time `gorm:"index"`
}

type CacheEntry struct {
	ID           uint      `gorm:"primaryKey"`
	UserID       uint      `gorm:"index;not null"`
	Endpoint     string    `gorm:"size:64;index;not null"`
	RequestHash  string    `gorm:"size:128;index;not null"`
	Model        string    `gorm:"size:255;index"`
	QueryText    string    `gorm:"type:text"`
	StatusCode   int       `gorm:"not null"`
	ContentType  string    `gorm:"size:255"`
	ResponseBody []byte    `gorm:"type:blob"`
	ExpiresAt    time.Time `gorm:"index"`
	CreatedAt    time.Time `gorm:"index"`
}

type SiteSetting struct {
	ID        uint   `gorm:"primaryKey"`
	Key       string `gorm:"size:128;uniqueIndex;not null"`
	Value     string `gorm:"type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time
}
