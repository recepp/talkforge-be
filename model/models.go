package model

import (
	"time"

	"gorm.io/gorm"
)

// User represents a user inside the TalkForge platform.
type User struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	Nickname     string    `gorm:"type:varchar(50);uniqueIndex;not null" json:"nickname"`
	Avatar       string    `gorm:"type:varchar(512);default:'👤'" json:"avatar"`
	PasswordHash string    `gorm:"type:varchar(255)" json:"-"` // Nullable/empty for Google-only users
	GoogleID     *string   `gorm:"uniqueIndex" json:"google_id,omitempty"`
	Role         string    `gorm:"type:varchar(20);default:'user';not null" json:"role" enums:"user,admin"`
	Language     string    `gorm:"type:varchar(10);default:'tr';not null" json:"language"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName overrides the default table name for the User model to talkforge_users.
func (User) TableName() string {
	return "talkforge_users"
}

// TalkRequest represents a request to generate dialogue texts.
type TalkRequest struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	UserID        uint           `json:"user_id"`
	User          User           `gorm:"foreignKey:UserID" json:"-"`
	Mode          string         `gorm:"type:varchar(20);not null" json:"mode" enums:"new,update"`
	Status        string         `gorm:"type:varchar(20);default:'pending';not null" json:"status" enums:"pending,processing,completed,failed"`
	Language      string         `gorm:"type:varchar(50);not null" json:"language"`
	Place         string         `gorm:"type:varchar(255);not null" json:"place"`
	Topic         string         `gorm:"type:text;not null" json:"topic"`
	Duration      int            `gorm:"type:integer;default:0;not null" json:"duration"`
	SpeechType    string         `gorm:"type:varchar(100);default:'';not null" json:"speech_type"`
	Instruction   string         `gorm:"type:text" json:"instruction,omitempty"`
	VersionNumber int            `gorm:"type:integer;default:1;not null" json:"version_number"`
	ParentID      *uint          `json:"parent_id,omitempty"`
	GeneratedText string         `gorm:"type:text" json:"generated_text,omitempty"`
	ErrorMessage  string         `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName overrides the default table name for the TalkRequest model to talk_requests.
func (TalkRequest) TableName() string {
	return "talk_requests"
}
