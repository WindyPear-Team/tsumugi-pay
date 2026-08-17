package app

import (
	"time"

	"github.com/google/uuid"
)

// Account is a single user's merchant account. It is not a tenant and may not
// have multiple user memberships.
type Account struct {
	ID                       uuid.UUID `gorm:"type:char(36);primaryKey"`
	Name                     string    `gorm:"size:120;not null"`
	MerchantNo               string    `gorm:"size:64;unique;not null"`
	Status                   string    `gorm:"size:16;not null;default:active"`
	APISecretCiphertext      string    `gorm:"type:text;not null"`
	CallbackSecretCiphertext string    `gorm:"type:text;not null"`
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// User is the sole login owner of an account and its payment channels.
type User struct {
	ID           uuid.UUID  `gorm:"type:char(36);primaryKey"`
	AccountID    *uuid.UUID `gorm:"type:char(36);uniqueIndex"`
	Account      *Account   `gorm:"constraint:OnDelete:CASCADE"`
	Email        string     `gorm:"size:255;unique;not null"`
	Username     string     `gorm:"size:64;uniqueIndex"`
	OIDCSubject  *string    `gorm:"size:512;uniqueIndex"`
	PasswordHash string     `gorm:"type:text;not null"`
	DisplayName  string     `gorm:"size:120;not null"`
	Role         string     `gorm:"size:32;not null"`
	IsActive     bool       `gorm:"not null;default:true"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type PaymentChannel struct {
	ID               uuid.UUID `gorm:"type:char(36);primaryKey"`
	AccountID        uuid.UUID `gorm:"type:char(36);not null;index:idx_channels_account_provider,priority:1"`
	Account          Account   `gorm:"constraint:OnDelete:CASCADE"`
	Provider         string    `gorm:"size:16;not null;index:idx_channels_account_provider,priority:2"`
	DisplayName      string    `gorm:"size:120;not null"`
	Priority         int       `gorm:"not null;default:100;index:idx_channels_dispatch,priority:3"`
	Weight           int       `gorm:"not null;default:100"`
	Enabled          bool      `gorm:"not null;default:false;index:idx_channels_dispatch,priority:2"`
	ConfigCiphertext string    `gorm:"type:text;not null;default:''"`
	WebhookToken     string    `gorm:"size:80;unique;not null"`
	CreatedAt        time.Time `gorm:"index:idx_channels_dispatch,priority:4"`
	UpdatedAt        time.Time
}

type Bill struct {
	ID                    uuid.UUID `gorm:"type:char(36);primaryKey"`
	AccountID             uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:bills_account_id_merchant_order_no_key;index:idx_bills_account_created,priority:1;index:idx_bills_account_status,priority:1"`
	Account               Account   `gorm:"constraint:OnDelete:CASCADE"`
	ChannelID             uuid.UUID `gorm:"type:char(36);not null"`
	Channel               PaymentChannel
	PlatformOrderNo       string  `gorm:"size:64;unique;not null"`
	MerchantOrderNo       string  `gorm:"size:128;not null;uniqueIndex:bills_account_id_merchant_order_no_key"`
	Subject               string  `gorm:"size:256;not null"`
	Description           string  `gorm:"size:512;not null;default:''"`
	AmountMinor           int64   `gorm:"not null"`
	Currency              string  `gorm:"size:3;not null;default:CNY"`
	Provider              string  `gorm:"size:16;not null"`
	Scene                 string  `gorm:"size:16;not null"`
	Status                string  `gorm:"size:16;not null;default:pending;index:idx_bills_account_status,priority:2"`
	ProviderTransactionID string  `gorm:"size:128"`
	ProviderPayload       string  `gorm:"type:text;not null;default:{}"`
	NotifyURL             string  `gorm:"type:text;not null"`
	ReturnURL             *string `gorm:"type:text"`
	Metadata              *string `gorm:"type:text"`
	ExpiresAt             *time.Time
	PaidAt                *time.Time
	ClosedAt              *time.Time
	CreatedAt             time.Time `gorm:"index:idx_bills_account_created,priority:2"`
	UpdatedAt             time.Time
}

type Refund struct {
	ID               uuid.UUID `gorm:"type:char(36);primaryKey"`
	AccountID        uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:refunds_account_id_refund_order_no_key"`
	Account          Account   `gorm:"constraint:OnDelete:CASCADE"`
	BillID           uuid.UUID `gorm:"type:char(36);not null;index:idx_refunds_bill_created,priority:1"`
	Bill             Bill
	RefundOrderNo    string    `gorm:"size:128;not null;uniqueIndex:refunds_account_id_refund_order_no_key"`
	AmountMinor      int64     `gorm:"not null"`
	Reason           string    `gorm:"size:256;not null;default:''"`
	Status           string    `gorm:"size:16;not null;default:pending"`
	ProviderRefundID string    `gorm:"size:128"`
	ProviderPayload  string    `gorm:"type:text;not null;default:{}"`
	CreatedAt        time.Time `gorm:"index:idx_refunds_bill_created,priority:2"`
	UpdatedAt        time.Time
}
type WebhookEvent struct {
	ID          uuid.UUID `gorm:"type:char(36);primaryKey"`
	AccountID   uuid.UUID `gorm:"type:char(36);not null"`
	Account     Account   `gorm:"constraint:OnDelete:CASCADE"`
	ChannelID   uuid.UUID `gorm:"type:char(36);not null;uniqueIndex:webhook_events_channel_id_event_key_key;index:idx_webhook_events_channel_created,priority:1"`
	Channel     PaymentChannel
	Provider    string `gorm:"size:16;not null"`
	EventKey    string `gorm:"size:200;not null;uniqueIndex:webhook_events_channel_id_event_key_key"`
	Verified    bool   `gorm:"not null;default:false"`
	Payload     string `gorm:"type:text;not null"`
	ProcessedAt *time.Time
	CreatedAt   time.Time `gorm:"index:idx_webhook_events_channel_created,priority:2"`
}
type AuditLog struct {
	ID          uuid.UUID  `gorm:"type:char(36);primaryKey"`
	AccountID   *uuid.UUID `gorm:"type:char(36);index:idx_audit_logs_account_created,priority:1"`
	Account     *Account   `gorm:"constraint:OnDelete:SET NULL"`
	ActorUserID *uuid.UUID `gorm:"type:char(36)"`
	ActorUser   *User      `gorm:"constraint:OnDelete:SET NULL"`
	Action      string     `gorm:"size:100;not null"`
	TargetType  string     `gorm:"size:64;not null"`
	TargetID    string     `gorm:"size:128;not null"`
	RequestID   string     `gorm:"size:64;not null"`
	Detail      string     `gorm:"type:text;not null;default:{}"`
	CreatedAt   time.Time  `gorm:"index:idx_audit_logs_account_created,priority:2"`
}
type SystemSetting struct {
	SettingKey   string `gorm:"size:80;primaryKey"`
	SettingValue string `gorm:"type:text;not null"`
	CreatedAt    time.Time
}
type AccountSetting struct {
	AccountID                uuid.UUID `gorm:"type:char(36);primaryKey"`
	Account                  Account   `gorm:"constraint:OnDelete:CASCADE"`
	EmailConfigCiphertext    string    `gorm:"type:text;not null;default:''"`
	HCaptchaConfigCiphertext string    `gorm:"type:text;not null;default:''"`
	OIDCConfigCiphertext     string    `gorm:"type:text;not null;default:''"`
	UpdatedAt                time.Time
}
