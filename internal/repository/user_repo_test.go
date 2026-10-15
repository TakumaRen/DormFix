package repository

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"dormfix/internal/config"
	"dormfix/internal/database"
	"dormfix/internal/models"
)

func setupTestRepo(t *testing.T, dbName string) (*UserRepository, func()) {
	t.Helper()
	testDir := filepath.Join(os.TempDir(), "dormfix_test")
	_ = os.MkdirAll(testDir, 0755)
	testDBPath := filepath.Join(testDir, dbName)
	_ = os.Remove(testDBPath)

	cfg := &config.Config{
		Port:          "8080",
		DBPath:        testDBPath,
		AppEnv:        "development",
		AdminUsername: "MAIN_ADMIN",
		AdminPassword: "adminPassword123",
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB ล้มเหลว: %v", err)
	}

	repo := NewUserRepository(db)
	cleanup := func() {
		db.Close()
		os.Remove(testDBPath)
	}

	return repo, cleanup
}

func TestUserRepo_CreateAndGet(t *testing.T) {
	repo, cleanup := setupTestRepo(t, "test_repo_create.db")
	defer cleanup()

	user := &models.User{
		Username:     "b201", // ส่งตัวพิมพ์เล็กมา
		PasswordHash: "hashedpassword123",
		FullName:     "สมศักดิ์ สุขใจ",
		Phone:        "0812345678",
		Role:         models.RoleUser,
		Status:       models.UserStatusPending,
	}

	err := repo.CreateUser(user)
	if err != nil {
		t.Fatalf("CreateUser ล้มเหลว: %v", err)
	}

	if user.ID == 0 {
		t.Errorf("คาดหวัง user.ID > 0")
	}
	if user.Username != "B201" {
		t.Errorf("คาดหวัง username ถูกแปลงเป็น B201 แต่ได้ %s", user.Username)
	}

	// ค้นหาด้วยตัวพิมพ์เล็ก 'b201'
	found, err := repo.GetUserByUsername("b201")
	if err != nil {
		t.Fatalf("GetUserByUsername('b201') ล้มเหลว: %v", err)
	}
	if found.ID != user.ID || found.FullName != "สมศักดิ์ สุขใจ" {
		t.Errorf("ข้อมูลผู้ใช้ไม่ตรงกับที่บันทึกไว้")
	}

	// ค้นหาด้วย ID
	foundByID, err := repo.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("GetUserByID(%d) ล้มเหลว: %v", user.ID, err)
	}
	if foundByID.Username != "B201" {
		t.Errorf("Username ที่ค้นหาด้วย ID ไม่ตรง: %s", foundByID.Username)
	}
}

func TestUserRepo_DuplicateUsername(t *testing.T) {
	repo, cleanup := setupTestRepo(t, "test_repo_dup.db")
	defer cleanup()

	u1 := &models.User{
		Username:     "B202",
		PasswordHash: "hash1",
		FullName:     "ผู้เช่า 1",
		Phone:        "0811111111",
		Role:         models.RoleUser,
		Status:       models.UserStatusPending,
	}
	if err := repo.CreateUser(u1); err != nil {
		t.Fatalf("บันทึก u1 ล้มเหลว: %v", err)
	}

	// พยายามสร้าง 'b202' (ตัวพิมพ์เล็ก)
	u2 := &models.User{
		Username:     "b202",
		PasswordHash: "hash2",
		FullName:     "ผู้เช่า 2",
		Phone:        "0822222222",
		Role:         models.RoleUser,
		Status:       models.UserStatusPending,
	}
	err := repo.CreateUser(u2)
	if err == nil {
		t.Fatalf("คาดหวัง ErrUsernameDuplicate เมื่อใช้เลขห้องซ้ำ แต่กลับสำเร็จ")
	}
	if err != ErrUsernameDuplicate {
		t.Errorf("คาดหวัง ErrUsernameDuplicate แต่ได้ข้อผิดพลาดอื่น: %v", err)
	}
}

func TestUserRepo_Sessions(t *testing.T) {
	repo, cleanup := setupTestRepo(t, "test_repo_session.db")
	defer cleanup()

	user := &models.User{
		Username:     "C301",
		PasswordHash: "hash",
		FullName:     "ผู้ทดสอบเซสชัน",
		Phone:        "0899999999",
		Role:         models.RoleUser,
		Status:       models.UserStatusActive,
	}
	if err := repo.CreateUser(user); err != nil {
		t.Fatalf("CreateUser ล้มเหลว: %v", err)
	}

	sessionToken := "random_test_session_token_12345"
	sess := &models.Session{
		Token:     sessionToken,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	if err := repo.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession ล้มเหลว: %v", err)
	}

	// ดึง Session
	foundSess, err := repo.GetSessionByToken(sessionToken)
	if err != nil {
		t.Fatalf("GetSessionByToken ล้มเหลว: %v", err)
	}
	if foundSess.UserID != user.ID {
		t.Errorf("UserID ใน Session ไม่ถูกต้อง: คาดหวัง %d แต่ได้ %d", user.ID, foundSess.UserID)
	}

	// ลบ Session เดี่ยว (Logout)
	if err := repo.DeleteSession(sessionToken); err != nil {
		t.Fatalf("DeleteSession ล้มเหลว: %v", err)
	}
	_, err = repo.GetSessionByToken(sessionToken)
	if err != ErrSessionNotFound {
		t.Errorf("คาดหวัง ErrSessionNotFound หลังลบ Session แต่ได้: %v", err)
	}
}

func TestUserRepo_CountActiveAdmins(t *testing.T) {
	repo, cleanup := setupTestRepo(t, "test_repo_admin.db")
	defer cleanup()

	// จาก setupTestRepo มี MAIN_ADMIN (ID 1) อยู่แล้ว
	admin1, err := repo.GetUserByUsername("MAIN_ADMIN")
	if err != nil {
		t.Fatalf("ไม่พบ MAIN_ADMIN: %v", err)
	}

	// ตรวจสอบจำนวน Active Admin โดยไม่รวม admin1
	count, err := repo.CountActiveAdminsExcluding(admin1.ID)
	if err != nil {
		t.Fatalf("CountActiveAdminsExcluding ล้มเหลว: %v", err)
	}
	if count != 0 {
		t.Errorf("คาดหวัง 0 คน (เพราะมีคนเดียว) แต่ได้ %d", count)
	}

	// เพิ่ม Admin คนที่ 2
	admin2 := &models.User{
		Username:     "ADMIN_TWO",
		PasswordHash: "hash2",
		FullName:     "ผู้ดูแลคนที่สอง",
		Phone:        "0800000002",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := repo.CreateUser(admin2); err != nil {
		t.Fatalf("สร้าง admin2 ล้มเหลว: %v", err)
	}

	// ตอนนี้นับโดยไม่รวม admin1 ต้องได้ 1 (คือ admin2)
	count, err = repo.CountActiveAdminsExcluding(admin1.ID)
	if err != nil || count != 1 {
		t.Errorf("คาดหวัง 1 คนแต่ได้ %d", count)
	}

	// หากระงับ admin2
	if err := repo.UpdateUserStatus(admin2.ID, models.UserStatusSuspended); err != nil {
		t.Fatalf("UpdateUserStatus ล้มเหลว: %v", err)
	}

	// นับใหม่โดยไม่รวม admin1 ต้องเหลือ 0 คน
	count, err = repo.CountActiveAdminsExcluding(admin1.ID)
	if err != nil || count != 0 {
		t.Errorf("คาดหวัง 0 คนหลัง admin2 ถูกระงับ แต่ได้ %d", count)
	}
}
