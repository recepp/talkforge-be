package model

import (
	"fmt"
	"log"
	"strings"

	"talkforge-be/config"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DB is the global database connection handle.
var DB *gorm.DB

// InitDB initializes the PostgreSQL connection using GORM and runs auto-migrations.
func InitDB(cfg *config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database using connection string: %v", err)
	}

	log.Println("Database connection established successfully using DATABASE_URL.")

	// Run auto-migrations for talkforge_users, talk_requests, talk_tags, talk_types, rooms, room_members, room_messages, and gemini_call_logs
	err = db.AutoMigrate(&User{}, &TalkRequest{}, &TalkTag{}, &TalkType{}, &Room{}, &RoomMember{}, &RoomMessage{}, &GeminiCallLog{})
	if err != nil {
		return nil, fmt.Errorf("failed to run database auto-migrations: %v", err)
	}

	// Back-fill: ensure any pre-existing room_members rows have status='accepted'
	// (rows created before the status column was added will have NULL or empty status).
	if err := db.Exec("UPDATE room_members SET status = 'accepted' WHERE status IS NULL OR status = ''").Error; err != nil {
		log.Printf("Warning: Failed to back-fill room_member statuses: %v", err)
	}

	log.Println("Database auto-migrations executed successfully.")


	DB = db


	// Seed default Talk Types if table is empty
	err = seedTalkTypes(db)
	if err != nil {
		log.Printf("Warning: Failed to seed talk types: %v", err)
	}

	// Bootstrap Admin User if config credentials are set
	err = bootstrapAdmin(db, cfg)
	if err != nil {
		log.Printf("Warning: Failed to bootstrap admin user: %v", err)
	}

	return db, nil
}

func bootstrapAdmin(db *gorm.DB, cfg *config.Config) error {
	adminEmail := cfg.AdminBootstrapName
	if !strings.Contains(adminEmail, "@") {
		adminEmail = adminEmail + "@talkforge.local"
	}

	var count int64
	err := db.Model(&User{}).Where("email = ? OR role = ?", adminEmail, "admin").Count(&count).Error
	if err != nil {
		return err
	}

	if count == 0 {
		hashed, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminBootstrapPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}

		adminUser := User{
			Email:        adminEmail,
			Nickname:     "admin",
			PasswordHash: string(hashed),
			Role:         "admin",
			Avatar:       "👑",
		}

		err = db.Create(&adminUser).Error
		if err != nil {
			return err
		}
		log.Printf("Admin user bootstrapped successfully: %s", adminEmail)
	}

	return nil
}

func seedTalkTypes(db *gorm.DB) error {
	var count int64
	if err := db.Model(&TalkType{}).Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		return nil
	}

	defaultTypes := []TalkType{
		{
			Key:          "politician",
			Symbol:       "🏛️",
			SortOrder:    1,
			IsCustom:     false,
			SystemPrompt: "You are an expert political speechwriter. Write a persuasive, charismatic, and inspiring speech tailored for a public figure or politician addressing voters or citizens. Focus on strong rhetoric, clear vision, key policy themes, and patriotic or community-focused tone.",
			LabelTR:      "Siyasetçi Konuşması",
			LabelEN:      "Political Speech",
			LabelDE:      "Politische Rede",
			LabelFR:      "Discours Politique",
			LabelES:      "Discurso Político",
			LabelAR:      "خطاب سياسي",
			LabelRU:      "Политическая речь",
		},
		{
			Key:          "imam",
			Symbol:       "🕌",
			SortOrder:    2,
			IsCustom:     false,
			SystemPrompt: "You are a respected religious scholar and imam. Write a serene, wise, and spiritual sermon (khutbah or lecture) focusing on morality, community ethics, unity, peace, and spiritual guidance with appropriate respectful traditional phrasing.",
			LabelTR:      "İmam Vaazı / Hutbe",
			LabelEN:      "Imam Sermon / Khutbah",
			LabelDE:      "Imam-Predigt / Chutba",
			LabelFR:      "Sermon d'Imam / Khutba",
			LabelES:      "Sermón de Imán / Khutbah",
			LabelAR:      "خطبة إمام / موعظة",
			LabelRU:      "Проповедь Имама",
		},
		{
			Key:          "teacher_welcome",
			Symbol:       "🎓",
			SortOrder:    3,
			IsCustom:     false,
			SystemPrompt: "You are an inspiring and warm school teacher welcoming a new class of students. Write an encouraging, friendly, and motivational intro speech establishing enthusiasm for learning, positive rules, and mutual respect.",
			LabelTR:      "Öğretmen Sınıfa Tanışma Konuşması",
			LabelEN:      "Teacher Welcome Speech",
			LabelDE:      "Begrüßungsrede des Lehrers",
			LabelFR:      "Discours de Bienvenue de l'Enseignant",
			LabelES:      "Discurso de Bienvenida del Profesor",
			LabelAR:      "كلمة ترحيبية من المعلم للطلاب",
			LabelRU:      "Приветственная речь учителя",
		},
		{
			Key:          "teacher_farewell",
			Symbol:       "👋",
			SortOrder:    4,
			IsCustom:     false,
			SystemPrompt: "You are a beloved teacher saying goodbye to graduating or departing students. Write an emotional, reflective, proud, and inspiring farewell speech encouraging them in their future endeavors.",
			LabelTR:      "Öğretmen Sınıfa Veda Konuşması",
			LabelEN:      "Teacher Farewell Speech",
			LabelDE:      "Abschiedsrede des Lehrers",
			LabelFR:      "Discours d'Adieu de l'Enseignant",
			LabelES:      "Discurso de Despedida del Profesor",
			LabelAR:      "كلمة وداع من المعلم للطلاب",
			LabelRU:      "Прощальная речь учителя",
		},
		{
			Key:          "business",
			Symbol:       "💼",
			SortOrder:    5,
			IsCustom:     false,
			SystemPrompt: "You are a high-level corporate executive and public speaker. Write a professional, sharp, impact-driven business presentation speech focusing on vision, strategy, results, value creation, and team motivation.",
			LabelTR:      "Genel İş Sunumu / Hitabet",
			LabelEN:      "Business Presentation Speech",
			LabelDE:      "Geschäftspräsentation / Rede",
			LabelFR:      "Présentation d'Affaires",
			LabelES:      "Presentación de Negocios",
			LabelAR:      "عرض تقديم للأعمال",
			LabelRU:      "Бизнес-презентация",
		},
		{
			Key:          "wedding",
			Symbol:       "🥂",
			SortOrder:    6,
			IsCustom:     false,
			SystemPrompt: "You are a charismatic toastmaster at a wedding or celebration. Write a heartwarming, joyful, humorous, and affectionate toast/speech celebrating the couple, friendship, love, and future happiness.",
			LabelTR:      "Düğün / Kutlama Konuşması",
			LabelEN:      "Wedding / Celebration Toast",
			LabelDE:      "Hochzeits- / Feierrede",
			LabelFR:      "Discours de Mariage",
			LabelES:      "Discurso de Boda / Celebración",
			LabelAR:      "خطاب زفاف / احتفال",
			LabelRU:      "Свадебная / Праздничная речь",
		},
		{
			Key:          "motivational",
			Symbol:       "🚀",
			SortOrder:    7,
			IsCustom:     false,
			SystemPrompt: "You are an energetic keynote motivational speaker. Write a high-energy, empowering speech that inspires action, resilience, overcoming obstacles, and achieving greatness.",
			LabelTR:      "Motivasyon Konuşması",
			LabelEN:      "Motivational Speech",
			LabelDE:      "Motivationsrede",
			LabelFR:      "Discours Motivationnel",
			LabelES:      "Discurso Motivacional",
			LabelAR:      "خطاب تحفيزي",
			LabelRU:      "Мотивационная речь",
		},
		{
			Key:          "sales",
			Symbol:       "📊",
			SortOrder:    8,
			IsCustom:     false,
			SystemPrompt: "You are a master pitch presenter and sales leader. Write a persuasive, compelling sales presentation focusing on solving customer pain points, highlighting key product benefits, and closing with a strong call to action.",
			LabelTR:      "Satış Sunumu",
			LabelEN:      "Sales Presentation",
			LabelDE:      "Verkaufspräsentation",
			LabelFR:      "Présentation de Vente",
			LabelES:      "Presentación de Ventas",
			LabelAR:      "عرض مبيعات",
			LabelRU:      "Презентация продаж",
		},
		{
			Key:          "official",
			Symbol:       "📜",
			SortOrder:    9,
			IsCustom:     false,
			SystemPrompt: "You are an official spokesperson delivering a formal address. Write a structured, elegant, diplomatic, and authoritative speech appropriate for formal ceremonies, announcements, or official events.",
			LabelTR:      "Resmi Hitabet",
			LabelEN:      "Official Address",
			LabelDE:      "Offizielle Ansprache",
			LabelFR:      "Allocution Officielle",
			LabelES:      "Discurso Oficial",
			LabelAR:      "خطاب رسمي",
			LabelRU:      "Официальное обращение",
		},
		{
			Key:          "other",
			Symbol:       "✨",
			SortOrder:    10,
			IsCustom:     true,
			SystemPrompt: "You are a versatile master speechwriter. Adapt your tone, rhetoric, and style strictly to the user's specified target audience, role, and custom speech purpose.",
			LabelTR:      "Diğer",
			LabelEN:      "Other",
			LabelDE:      "Andere",
			LabelFR:      "Autre",
			LabelES:      "Otro",
			LabelAR:      "آخر",
			LabelRU:      "Другое",
		},
	}

	if err := db.Create(&defaultTypes).Error; err != nil {
		return err
	}

	log.Println("Default talk types seeded successfully.")
	return nil
}

// LogGeminiCall creates an entry in gemini_call_logs table.
func LogGeminiCall(userID uint, action string, status string) {
	if DB == nil {
		return
	}
	callLog := GeminiCallLog{
		UserID: userID,
		Action: action,
		Status: status,
	}
	if err := DB.Create(&callLog).Error; err != nil {
		log.Printf("Failed to log Gemini call: %v", err)
	}
}


