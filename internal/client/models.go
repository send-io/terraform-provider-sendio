package client

import "encoding/json"

// The response shapes below follow the backend's projections
// (backend/internal/platform resources.go, marketing.go, suppressions.go,
// agents.go). Nullable columns are pointers.

// Created is the body of create calls that answer with only the id.
type Created struct {
	ID    string `json:"id"`
	Token string `json:"token,omitempty"` // POST /api-keys only
}

type DomainRecord struct {
	Record   string  `json:"record"`
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	TTL      *string `json:"ttl"`
	Priority *int64  `json:"priority"`
	Status   *string `json:"status"`
}

type DomainCapabilities struct {
	Sending   string `json:"sending"`
	Receiving string `json:"receiving"`
}

type Domain struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	Status            string             `json:"status"`
	CreatedAt         string             `json:"created_at"`
	Region            string             `json:"region"`
	OpenTracking      bool               `json:"open_tracking"`
	ClickTracking     bool               `json:"click_tracking"`
	TrackingSubdomain *string            `json:"tracking_subdomain"`
	TrackingStatus    *string            `json:"tracking_status"`
	CustomReturnPath  string             `json:"custom_return_path"`
	DeliveryRoute     *string            `json:"delivery_route"`
	TLS               *string            `json:"tls"`
	Capabilities      DomainCapabilities `json:"capabilities"`
	Records           []DomainRecord     `json:"records"`
}

type Webhook struct {
	ID            string            `json:"id"`
	CreatedAt     string            `json:"created_at"`
	Status        string            `json:"status"`
	Endpoint      string            `json:"endpoint"`
	Events        []string          `json:"events"`
	Format        string            `json:"format"`
	Headers       map[string]string `json:"headers"`
	SigningSecret string            `json:"signing_secret"`
}

type APIKey struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at"`
	TokenPrefix string  `json:"token_prefix"`
	Permission  string  `json:"permission"`
	DomainID    *string `json:"domain_id"`
	Domain      *string `json:"domain"`
}

type Suppression struct {
	ID        string  `json:"id"`
	Email     string  `json:"email"`
	Origin    string  `json:"origin"`
	CreatedAt string  `json:"created_at"`
	Comment   *string `json:"comment"`
	ExpiresAt *string `json:"expires_at"`
	Expired   bool    `json:"expired"`
}

type TemplateVariable struct {
	Key           string          `json:"key"`
	Type          string          `json:"type"`
	FallbackValue json.RawMessage `json:"fallback_value,omitempty"`
}

type Template struct {
	ID                     string             `json:"id"`
	Name                   string             `json:"name"`
	Alias                  *string            `json:"alias"`
	Status                 string             `json:"status"`
	CreatedAt              string             `json:"created_at"`
	UpdatedAt              *string            `json:"updated_at"`
	PublishedAt            *string            `json:"published_at"`
	CurrentVersionID       *string            `json:"current_version_id"`
	HasUnpublishedVersions bool               `json:"has_unpublished_versions"`
	From                   *string            `json:"from"`
	Subject                *string            `json:"subject"`
	HTML                   *string            `json:"html"`
	Text                   *string            `json:"text"`
	PreviewText            *string            `json:"preview_text"`
	VersionNumber          *int64             `json:"version_number"`
	TemplateEngine         *string            `json:"template_engine"`
	Variables              []TemplateVariable `json:"variables"`
}

type Segment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type Topic struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Description         *string `json:"description"`
	DefaultSubscription string  `json:"default_subscription"`
	Visibility          string  `json:"visibility"`
	CreatedAt           string  `json:"created_at"`
}

type ContactProperty struct {
	ID            string          `json:"id"`
	Key           string          `json:"key"`
	Type          string          `json:"type"`
	FallbackValue json.RawMessage `json:"fallback_value"`
	CreatedAt     string          `json:"created_at"`
}

type Inbox struct {
	ID          string  `json:"id"`
	Address     string  `json:"address"`
	DisplayName *string `json:"display_name"`
	ClientID    *string `json:"client_id"`
	Domain      string  `json:"domain"`
	MailboxID   *string `json:"mailbox_id"`
	CreatedAt   string  `json:"created_at"`
}
