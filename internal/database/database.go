package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"dormfix/internal/config"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// InitDB เชื่อมต่อฐานข้อมูล SQLite, บังคับใช้ Foreign Keys, Auto-migrate ตาราง, และ Seed Admin เริ่มต้นอย่างปลอดภัย
func InitDB(cfg *config.Config) (*sql.DB, error) {
	// 1. สร้างโฟลเดอร์สำหรับเก็บฐานข้อมูลหากยังไม่มี
	dir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("ไม่สามารถสร้างโฟลเดอร์ฐานข้อมูลได้: %w", err)
	}

	// 2. เปิดการเชื่อมต่อ SQLite พร้อมเปิดใช้งาน Foreign Key และ WAL mode ใน DSN
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", cfg.DBPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("ไม่สามารถเปิดไฟล์ฐานข้อมูล SQLite ได้: %w", err)
	}

	// กำหนดให้ใช้ Single Writer Connection เพื่อป้องกันปัญหา Database Locked ใน SQLite
	db.SetMaxOpenConns(1)

	// 3. ตรวจสอบการเชื่อมต่อและยืนยัน Foreign Keys อีกครั้ง
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ไม่สามารถเชื่อมต่อฐานข้อมูลได้: %w", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return nil, fmt.Errorf("ไม่สามารถเปิดใช้งาน PRAGMA foreign_keys ได้: %w", err)
	}

	// ตรวจสอบค่า PRAGMA foreign_keys
	var fkStatus int
	if err := db.QueryRow("PRAGMA foreign_keys;").Scan(&fkStatus); err != nil || fkStatus != 1 {
		return nil, fmt.Errorf("SQLite foreign_keys ไม่ได้เปิดใช้งาน (status: %d)", fkStatus)
	}

	// 4. ทำ Auto-migration สร้าง Schema ตารางและ Index
	if err := migrateSchema(db); err != nil {
		return nil, fmt.Errorf("การทำ Migration ผิดพลาด: %w", err)
	}

	// 5. Seed บัญชี Admin เริ่มต้นอย่างปลอดภัยตามนโยบาย
	if err := seedAdminIfNeeded(db, cfg); err != nil {
		return nil, fmt.Errorf("การตั้งค่า Admin เริ่มต้นผิดพลาด: %w", err)
	}

	log.Printf("[DormFix] เชื่อมต่อฐานข้อมูล SQLite สำเร็จ: %s (Foreign Keys: ON)", cfg.DBPath)
	return db, nil
}

// migrateSchema สร้างตาราง users, maintenance_requests, sessions และ Unique Index
func migrateSchema(db *sql.DB) error {
	schema := `
	-- 1. ตารางผู้ใช้ (Users)
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		full_name TEXT NOT NULL,
		phone TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'user',
		status TEXT NOT NULL DEFAULT 'pending',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Index แบบ Unique บน UPPER(username) เพื่อป้องกันเลขห้องซ้ำทั้งตัวพิมพ์เล็กและใหญ่
	CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username_upper ON users(UPPER(username));

	-- 2. ตารางใบแจ้งซ่อม (Maintenance Requests)
	-- foreign key ชี้ไปที่ users(id) โดยไม่ใส่ ON DELETE CASCADE เพื่อป้องกันการลบข้อมูลคำร้อง
	CREATE TABLE IF NOT EXISTS maintenance_requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		room_no TEXT NOT NULL,
		category TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT NOT NULL,
		convenient_time TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		admin_notes TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	);

	-- 3. ตารางเซสชัน (Sessions)
	-- foreign key ชี้ไปที่ users(id) พร้อม ON DELETE CASCADE เพื่อลบ session อัตโนมัติเมื่อ user ถูกลบ
	CREATE TABLE IF NOT EXISTS sessions (
		token TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	);
	`

	_, err := db.Exec(schema)
	return err
}

// seedAdminIfNeeded สร้างบัญชี Admin เริ่มต้นอย่างปลอดภัย หากยังไม่มีในระบบ
func seedAdminIfNeeded(db *sql.DB, cfg *config.Config) error {
	var adminCount int
	err := db.QueryRow("SELECT COUNT(*) FROM users WHERE role = 'admin'").Scan(&adminCount)
	if err != nil {
		return err
	}

	// หากมี Admin อยู่แล้วในระบบ ข้ามขั้นตอนนี้ทันที ไม่สร้างซ้ำ และไม่รีเซ็ตรหัสผ่านเดิม
	if adminCount > 0 {
		return nil
	}

	adminUsername := strings.ToUpper(strings.TrimSpace(cfg.AdminUsername))
	adminPassword := cfg.AdminPassword

	// นโยบาย Production: หากไม่มีการกำหนด ADMIN_PASSWORD ต้องส่งคืน Error เพื่อให้ main() สั่ง Fatal หยุดทำงาน
	if cfg.AppEnv == "production" {
		if adminPassword == "" {
			return fmt.Errorf("FATAL: ระบบอยู่ในโหมด Production แต่ไม่ได้ตั้งค่าตัวแปรแวดล้อม ADMIN_PASSWORD")
		}
	} else {
		// นโยบาย Local Development: หากไม่ได้ตั้ง ADMIN_PASSWORD ให้สร้างรหัสผ่านสุ่มที่ปลอดภัย
		if adminPassword == "" {
			bytes := make([]byte, 8)
			if _, err := rand.Read(bytes); err != nil {
				return err
			}
			adminPassword = hex.EncodeToString(bytes) // รหัสสุ่ม 16 ตัวอักษร
			fmt.Printf("\n==========================================================\n")
			fmt.Printf(" [DormFix Initial Setup]\n")
			fmt.Printf(" สร้างบัญชีผู้ดูแลระบบ (Admin) เริ่มต้นเรียบร้อยแล้ว:\n")
			fmt.Printf(" Username: %s\n", adminUsername)
			fmt.Printf(" Password: %s\n", adminPassword)
			fmt.Printf(" (รหัสผ่านนี้แสดงเฉพาะในเครื่อง Local ครั้งแรก โปรดบันทึกไว้)\n")
			fmt.Printf("==========================================================\n\n")
		}
	}

	// เข้ารหัสผ่านด้วย bcrypt cost 10
	hashed, err := bcrypt.GenerateFromPassword([]byte(adminPassword), 10)
	if err != nil {
		return fmt.Errorf("ไม่สามารถเข้ารหัสผ่าน Admin ได้: %w", err)
	}

	_, err = db.Exec(`
		INSERT INTO users (username, password_hash, full_name, phone, role, status)
		VALUES (?, ?, 'ผู้ดูแลระบบหอพัก', '0000000000', 'admin', 'active')
	`, adminUsername, string(hashed))

	return err
}
