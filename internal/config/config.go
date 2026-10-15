package config

import (
	"os"
)

// Config เก็บค่าคอนฟิกทั้งหมดของระบบ DormFix
type Config struct {
	Port          string
	DBPath        string
	AppEnv        string
	AdminUsername string
	AdminPassword string
	SessionSecret string
	SecureCookie  bool
}

// LoadConfig อ่านค่าจาก Environment Variables พร้อมค่าเริ่มต้นที่ปลอดภัย
func LoadConfig() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "data/dormfix.db"
	}

	appEnv := os.Getenv("APP_ENV")
	if appEnv == "" {
		appEnv = os.Getenv("ENV")
	}
	if appEnv == "" {
		appEnv = "development"
	}

	adminUsername := os.Getenv("ADMIN_USERNAME")
	if adminUsername == "" {
		adminUsername = "admin"
	}

	adminPassword := os.Getenv("ADMIN_PASSWORD")

	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		sessionSecret = "dormfix-default-secret-key-change-in-production"
	}

	secureCookie := appEnv == "production" || os.Getenv("COOKIE_SECURE") == "true"

	return &Config{
		Port:          port,
		DBPath:        dbPath,
		AppEnv:        appEnv,
		AdminUsername: adminUsername,
		AdminPassword: adminPassword,
		SessionSecret: sessionSecret,
		SecureCookie:  secureCookie,
	}
}
