package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dormfix/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// setupTestDB เตรียมฐานข้อมูลชั่วคราวสำหรับการทดสอบ
func setupTestDB(t *testing.T, dbName string) (*sql.DB, *config.Config, func()) {
	t.Helper()

	testDir := filepath.Join(os.TempDir(), "dormfix_test")
	_ = os.MkdirAll(testDir, 0755)
	testDBPath := filepath.Join(testDir, dbName)
	_ = os.Remove(testDBPath)

	cfg := &config.Config{
		Port:          "8080",
		DBPath:        testDBPath,
		AppEnv:        "development",
		AdminUsername: "ADMIN",
		AdminPassword: "testAdminPassword123",
		SessionSecret: "test-secret",
	}

	db, err := InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB ล้มเหลว: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(testDBPath)
	}

	return db, cfg, cleanup
}

// 1. ทดสอบการเชื่อมต่อ, การเปิดใช้ Foreign Keys, และการสร้างตาราง
func TestInitDB_ConnectionAndForeignKeys(t *testing.T) {
	db, _, cleanup := setupTestDB(t, "test_conn.db")
	defer cleanup()

	// ตรวจสอบว่า PRAGMA foreign_keys เปิดอยู่ (ค่าต้องเป็น 1)
	var fkStatus int
	err := db.QueryRow("PRAGMA foreign_keys;").Scan(&fkStatus)
	if err != nil {
		t.Fatalf("ไม่สามารถอ่านค่า PRAGMA foreign_keys ได้: %v", err)
	}
	if fkStatus != 1 {
		t.Errorf("คาดหวัง foreign_keys = 1 แต่ได้ %d", fkStatus)
	}

	// ตรวจสอบว่ามีตาราง users, maintenance_requests, sessions ครบถ้วน
	tables := []string{"users", "maintenance_requests", "sessions"}
	for _, table := range tables {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil || count != 1 {
			t.Errorf("ไม่พบตาราง %s ในฐานข้อมูล", table)
		}
	}

	// ตรวจสอบ Index idx_users_username_upper
	var idxCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_users_username_upper'").Scan(&idxCount)
	if err != nil || idxCount != 1 {
		t.Errorf("ไม่พบ Unique Index idx_users_username_upper ในฐานข้อมูล")
	}
}

// 2. ทดสอบความปลอดภัยของ Admin เริ่มต้น และการไม่รีเซ็ตรหัสผ่านเดิมเมื่อรีสตาร์ต
func TestInitDB_SafeAdminPersistence(t *testing.T) {
	testDir := filepath.Join(os.TempDir(), "dormfix_test")
	_ = os.MkdirAll(testDir, 0755)
	testDBPath := filepath.Join(testDir, "test_admin.db")
	_ = os.Remove(testDBPath)
	defer os.Remove(testDBPath)

	cfg := &config.Config{
		Port:          "8080",
		DBPath:        testDBPath,
		AppEnv:        "development",
		AdminUsername: "ADMIN",
		AdminPassword: "OriginalAdminPassword123",
	}

	// รันครั้งที่ 1: สร้าง Admin เริ่มต้น
	db1, err := InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB ครั้งที่ 1 ล้มเหลว: %v", err)
	}

	var adminID int64
	var adminHash, adminStatus string
	err = db1.QueryRow("SELECT id, password_hash, status FROM users WHERE username = 'ADMIN'").Scan(&adminID, &adminHash, &adminStatus)
	if err != nil {
		t.Fatalf("ไม่พบบัญชี Admin ที่สร้างขึ้น: %v", err)
	}
	if adminStatus != "active" {
		t.Errorf("คาดหวังสถานะ Admin เริ่มต้นเป็น active แต่ได้ %s", adminStatus)
	}

	// ตรวจสอบว่ารหัสผ่านถูก Hash ด้วย bcrypt ถูกต้อง
	err = bcrypt.CompareHashAndPassword([]byte(adminHash), []byte("OriginalAdminPassword123"))
	if err != nil {
		t.Errorf("รหัสผ่าน Admin ไม่ตรงกับที่กำหนด: %v", err)
	}

	// จำลองว่า Admin ทำการเปลี่ยนรหัสผ่านใหม่
	newCustomPassword := "ChangedByAdminLater456"
	newHashed, _ := bcrypt.GenerateFromPassword([]byte(newCustomPassword), 10)
	_, err = db1.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(newHashed), adminID)
	if err != nil {
		t.Fatalf("จำลองการเปลี่ยนรหัสผ่าน Admin ล้มเหลว: %v", err)
	}
	db1.Close()

	// รันครั้งที่ 2 (จำลองการรีสตาร์ตเซิร์ฟเวอร์ โดยส่งรหัสเดิมใน config)
	db2, err := InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB ครั้งที่ 2 (รีสตาร์ต) ล้มเหลว: %v", err)
	}
	defer db2.Close()

	// ตรวจสอบว่าจำนวน Admin ต้องยังมีเพียง 1 คน (ไม่สร้างซ้ำ)
	var adminTotal int
	err = db2.QueryRow("SELECT COUNT(*) FROM users WHERE role = 'admin'").Scan(&adminTotal)
	if err != nil || adminTotal != 1 {
		t.Errorf("คาดหวังจำนวน Admin = 1 แต่ได้ %d", adminTotal)
	}

	// ตรวจสอบว่ารหัสผ่านต้องเป็นรหัสใหม่ที่ Admin เปลี่ยนไว้ (ต้องไม่ถูกรีเซ็ตทับ!)
	var currentHash string
	err = db2.QueryRow("SELECT password_hash FROM users WHERE id = ?", adminID).Scan(&currentHash)
	if err != nil {
		t.Fatalf("อ่านรหัสผ่านหลังรีสตาร์ตล้มเหลว: %v", err)
	}
	err = bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(newCustomPassword))
	if err != nil {
		t.Errorf("รหัสผ่าน Admin ถูกรีเซ็ตทับโดยไม่ตั้งใจหลังรีสตาร์ตเซิร์ฟเวอร์!")
	}
}

// 3. ทดสอบการป้องกัน Username ซ้ำทั้งตัวพิมพ์เล็กและตัวพิมพ์ใหญ่ (Case-insensitive)
func TestDatabase_UsernameDuplicateProtection(t *testing.T) {
	db, _, cleanup := setupTestDB(t, "test_dup.db")
	defer cleanup()

	// บันทึกผู้ใช้ห้อง A303 (ตัวพิมพ์ใหญ่)
	_, err := db.Exec(`
		INSERT INTO users (username, password_hash, full_name, phone, role, status)
		VALUES ('A303', 'hash123', 'สมชาย ใจดี', '0812345678', 'user', 'pending')
	`)
	if err != nil {
		t.Fatalf("บันทึกผู้ใช้ A303 ล้มเหลว: %v", err)
	}

	// ทดสอบที่ 1: พยายามบันทึก 'a303' (ตัวพิมพ์เล็ก) ➔ ต้องเกิด Unique Constraint Error
	_, err = db.Exec(`
		INSERT INTO users (username, password_hash, full_name, phone, role, status)
		VALUES ('a303', 'hash456', 'สมหญิง สวยงาม', '0898765432', 'user', 'pending')
	`)
	if err == nil {
		t.Errorf("คาดหวังข้อผิดพลาด Unique constraint เมื่อบันทึก 'a303' ซ้ำ แต่กลับบันทึกผ่าน!")
	} else if !strings.Contains(err.Error(), "UNIQUE") && !strings.Contains(err.Error(), "constraint") {
		t.Errorf("ข้อผิดพลาดที่ได้ไม่ใช่ Unique constraint: %v", err)
	}

	// ทดสอบที่ 2: ตรวจสอบ Layer 1 (Application Pre-check)
	var count int
	testInput := "a303"
	err = db.QueryRow("SELECT COUNT(*) FROM users WHERE UPPER(username) = UPPER(?)", testInput).Scan(&count)
	if err != nil {
		t.Fatalf("คิวรีตรวจสอบชื่อซ้ำล้มเหลว: %v", err)
	}
	if count != 1 {
		t.Errorf("Layer 1 Pre-check ไม่พบว่ามี 'a303' อยู่ในระบบ (count = %d)", count)
	}
}

// 4. ทดสอบ Foreign Key: ลบ Session แบบ Cascade และบล็อกการลบ User ที่มีคำร้องแจ้งซ่อม
func TestDatabase_ForeignKeysCascadeAndRestrict(t *testing.T) {
	db, _, cleanup := setupTestDB(t, "test_fk.db")
	defer cleanup()

	// สร้างผู้ใช้ User 1 (มีเฉพาะ Session)
	res1, err := db.Exec(`
		INSERT INTO users (username, password_hash, full_name, phone, role, status)
		VALUES ('B101', 'hash123', 'ผู้เช่าหนึ่ง', '0811111111', 'user', 'active')
	`)
	if err != nil {
		t.Fatalf("สร้างผู้ใช้ B101 ล้มเหลว: %v", err)
	}
	user1ID, _ := res1.LastInsertId()

	// สร้าง Session ให้ User 1
	_, err = db.Exec(`
		INSERT INTO sessions (token, user_id, expires_at)
		VALUES ('test_token_user1', ?, datetime('now', '+1 day'))
	`, user1ID)
	if err != nil {
		t.Fatalf("สร้าง Session ให้ User 1 ล้มเหลว: %v", err)
	}

	// ลบ User 1 ➔ Session ต้องถูกลบตามแบบ Cascade อัตโนมัติ
	_, err = db.Exec("DELETE FROM users WHERE id = ?", user1ID)
	if err != nil {
		t.Fatalf("ลบ User 1 ล้มเหลว: %v", err)
	}

	var sessionCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sessions WHERE token = 'test_token_user1'").Scan(&sessionCount)
	if err != nil || sessionCount != 0 {
		t.Errorf("คาดหวัง Session ถูกลบตามแบบ Cascade แต่ยังคงเหลืออยู่ในระบบ (count = %d)", sessionCount)
	}

	// สร้างผู้ใช้ User 2 (มีใบแจ้งซ่อม)
	res2, err := db.Exec(`
		INSERT INTO users (username, password_hash, full_name, phone, role, status)
		VALUES ('B102', 'hash123', 'ผู้เช่าสอง', '0822222222', 'user', 'active')
	`)
	if err != nil {
		t.Fatalf("สร้างผู้ใช้ B102 ล้มเหลว: %v", err)
	}
	user2ID, _ := res2.LastInsertId()

	// สร้างใบแจ้งซ่อมให้ User 2
	_, err = db.Exec(`
		INSERT INTO maintenance_requests (user_id, room_no, category, title, description, convenient_time, status)
		VALUES (?, 'B102', 'ประปา', 'ก๊อกน้ำรั่ว', 'มีน้ำหยดตลอดเวลา', 'ช่วงเย็นหลัง 17:00 น.', 'pending')
	`, user2ID)
	if err != nil {
		t.Fatalf("สร้างใบแจ้งซ่อมให้ User 2 ล้มเหลว: %v", err)
	}

	// พยายามลบ User 2 ➔ ต้องถูก SQLite บล็อกด้วย Foreign Key Constraint Failed
	_, err = db.Exec("DELETE FROM users WHERE id = ?", user2ID)
	if err == nil {
		t.Errorf("คาดหวัง Foreign Key Constraint บล็อกการลบ User ที่มีใบแจ้งซ่อม แต่คำสั่งกลับทำงานผ่าน!")
	} else if !strings.Contains(err.Error(), "FOREIGN KEY") && !strings.Contains(err.Error(), "constraint") {
		t.Errorf("ข้อผิดพลาดที่ได้ไม่ใช่ Foreign Key Constraint: %v", err)
	}
}

// 5. ทดสอบว่าในโหมด Production จะปฏิเสธการเริ่มทำงานทันทีเมื่อไม่มี ADMIN_PASSWORD
func TestInitDB_ProductionRequiresAdminPassword(t *testing.T) {
	testDir := filepath.Join(os.TempDir(), "dormfix_test")
	_ = os.MkdirAll(testDir, 0755)
	testDBPath := filepath.Join(testDir, "test_prod_fail.db")
	_ = os.Remove(testDBPath)
	defer os.Remove(testDBPath)

	cfg := &config.Config{
		Port:          "8080",
		DBPath:        testDBPath,
		AppEnv:        "production",
		AdminUsername: "ADMIN",
		AdminPassword: "", // ว่างเปล่าใน Production
	}

	db, err := InitDB(cfg)
	if db != nil {
		db.Close()
	}
	if err == nil {
		t.Fatalf("คาดหวังว่า InitDB ต้องส่งคืน Error เมื่ออยู่ในโหมด Production แต่ไม่มี ADMIN_PASSWORD")
	}
	if !strings.Contains(err.Error(), "ADMIN_PASSWORD") {
		t.Errorf("ข้อผิดพลาดที่ได้ไม่ได้ระบุถึง ADMIN_PASSWORD: %v", err)
	}
}

