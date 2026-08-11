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
	ID               uint           `gorm:"primaryKey" json:"id"`
	UserID           uint           `json:"user_id"`
	User             User           `gorm:"foreignKey:UserID" json:"-"`
	Mode             string         `gorm:"type:varchar(20);not null" json:"mode" enums:"new,update,partial_update"`
	Status           string         `gorm:"type:varchar(20);default:'pending';not null" json:"status" enums:"pending,processing,completed,failed"`
	Language         string         `gorm:"type:varchar(50);not null" json:"language"`
	Place            string         `gorm:"type:varchar(255);not null" json:"place"`
	Topic            string         `gorm:"type:text;not null" json:"topic"`
	Duration         int            `gorm:"type:integer;default:0;not null" json:"duration"`
	SpeechType       string         `gorm:"type:varchar(100);default:'';not null" json:"speech_type"`
	CustomSpeechType string         `gorm:"type:varchar(255);default:'';not null" json:"custom_speech_type,omitempty"`
	Instruction      string         `gorm:"type:text" json:"instruction,omitempty"`
	SelectedText     string         `gorm:"type:text" json:"selected_text,omitempty"`
	VersionNumber    int            `gorm:"type:integer;default:1;not null" json:"version_number"`
	VersionLabel     string         `gorm:"type:varchar(50);default:'';not null" json:"version_label"`
	ParentID         *uint          `json:"parent_id,omitempty"`
	RoomID           *uint          `json:"room_id,omitempty"` // nil = personal talk; set = shared inside a Room
	IsFavorite       bool           `gorm:"default:false;not null" json:"is_favorite"`
	IsArchived       bool           `gorm:"default:false;not null" json:"is_archived"`
	GeneratedText    string         `gorm:"type:text" json:"generated_text,omitempty"`
	ErrorMessage     string         `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName overrides the default table name for the TalkRequest model to talk_requests.
func (TalkRequest) TableName() string {
	return "talk_requests"
}

// TalkTag represents a user-defined label attached to a root conversation.
// Tags are stored per-user so that room members can independently label shared talks.
// Only root TalkRequests (parent_id IS NULL) may have tags; this constraint is
// enforced at the handler layer.
type TalkTag struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	RootTalkID  uint      `gorm:"index:idx_talk_tag_unique,unique" json:"root_talk_id"`
	UserID      uint      `gorm:"index:idx_talk_tag_unique,unique" json:"user_id"`
	Name        string    `gorm:"type:varchar(100);index:idx_talk_tag_unique,unique;not null" json:"name"`
	CreatedAt   time.Time `json:"created_at"`
}

// TableName overrides the default table name for the TalkTag model to talk_tags.
func (TalkTag) TableName() string {
	return "talk_tags"
}

// TalkType represents a dynamic talk type/purpose with multi-language labels and system prompt.
type TalkType struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Key          string    `gorm:"type:varchar(50);uniqueIndex;not null" json:"key"`
	Symbol       string    `gorm:"type:varchar(10);default:''" json:"symbol"`
	SystemPrompt string    `gorm:"type:text;not null" json:"system_prompt"`
	IsCustom     bool      `gorm:"default:false" json:"is_custom"`
	SortOrder    int       `gorm:"default:0" json:"sort_order"`
	LabelTR      string    `gorm:"type:varchar(100);not null" json:"label_tr"`
	LabelEN      string    `gorm:"type:varchar(100);not null" json:"label_en"`
	LabelDE      string    `gorm:"type:varchar(100);not null" json:"label_de"`
	LabelFR      string    `gorm:"type:varchar(100);default:''" json:"label_fr"`
	LabelES      string    `gorm:"type:varchar(100);default:''" json:"label_es"`
	LabelAR      string    `gorm:"type:varchar(100);default:''" json:"label_ar"`
	LabelRU      string    `gorm:"type:varchar(100);default:''" json:"label_ru"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName overrides default table name for TalkType model to talk_types.
func (TalkType) TableName() string {
	return "talk_types"
}

// Room represents a shared workspace where members can jointly manage talks
// (ownership of talks inside a room is via membership, not a single user).
type Room struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"type:varchar(255);not null" json:"name"`
	OwnerID   uint      `json:"owner_id"`
	Owner     User      `gorm:"foreignKey:OwnerID" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName overrides the default table name for the Room model to rooms.
func (Room) TableName() string {
	return "rooms"
}

// RoomMember represents a user's membership and role within a Room.
// Status lifecycle: "pending" → "accepted" | "declined".
// Room owners are always inserted with status="accepted".
// InvitedEmail is set when the invitee has no account yet; cleared after account linking.
type RoomMember struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	RoomID       uint      `gorm:"index:idx_room_member" json:"room_id"`
	UserID       *uint     `gorm:"index:idx_room_member" json:"user_id,omitempty"`
	InvitedEmail string    `gorm:"type:varchar(255);default:''" json:"invited_email,omitempty"`
	User         User      `gorm:"foreignKey:UserID" json:"-"`
	Role         string    `gorm:"type:varchar(20);not null" json:"role" enums:"writer,reader"`
	Status       string    `gorm:"type:varchar(20);default:'accepted';not null" json:"status" enums:"pending,accepted,declined"`
	LastReadAt   time.Time `json:"last_read_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// TableName overrides the default table name for the RoomMember model to room_members.
func (RoomMember) TableName() string {
	return "room_members"
}

// RoomMessage represents one chat message in a room's discussion thread
// about a specific talk (the discussion that precedes generating its next version).
type RoomMessage struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	RoomID        uint      `json:"room_id"`
	TalkRequestID uint      `json:"talk_request_id"`
	UserID        uint      `json:"user_id"`
	User          User      `gorm:"foreignKey:UserID" json:"-"`
	Text          string    `gorm:"type:text;not null" json:"text"`
	CreatedAt     time.Time `json:"created_at"`
}

// TableName overrides the default table name for the RoomMessage model to room_messages.
func (RoomMessage) TableName() string {
	return "room_messages"
}

