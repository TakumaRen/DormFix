package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dormfix/internal/config"
	"dormfix/internal/database"
	"dormfix/internal/middleware"
	"dormfix/internal/models"
	"dormfix/internal/repository"
)

func setupTestApp(t *testing.T, dbName string) (*http.ServeMux, *repository.UserRepository, *config.Config, *sql.DB, func()) {
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
		SessionSecret: "test-secret-key-for-csrf-signing-12345",
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB ล้มเหลว: %v", err)
	}

	repo := repository.NewUserRepository(db)

	// พาธของ template ต้องชี้ไปยังโฟลเดอร์ web/templates ของโปรเจกต์
	cwd, _ := os.Getwd()
	var templateDir string
	if strings.HasSuffix(cwd, "handlers") {
		templateDir = filepath.Join("..", "..", "web", "templates")
	} else {
		templateDir = filepath.Join("web", "templates")
	}

	authHandler := NewAuthHandler(repo, cfg, templateDir)

	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			authHandler.LoginSubmit(w, r)
			return
		}
		authHandler.LoginPage(w, r)
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			authHandler.RegisterSubmit(w, r)
			return
		}
		authHandler.RegisterPage(w, r)
	})
	mux.HandleFunc("/pending", authHandler.PendingPage)
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		authHandler.Logout(w, r)
	})

	// Protected routes
	mux.Handle("/user/requests", middleware.RequireActiveUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("User Requests Page"))
	})))

	mux.Handle("/admin/dashboard", middleware.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Admin Dashboard Page"))
	})))

	cleanup := func() {
		db.Close()
		os.Remove(testDBPath)
	}

	return mux, repo, cfg, db, cleanup
}

// 1. ทดสอบการสมัครสมาชิกสำเร็จ และการปฏิเสธเมื่อ Username ซ้ำ
func TestAuth_RegisterSuccessAndDuplicate(t *testing.T) {
	mux, repo, cfg, _, cleanup := setupTestApp(t, "test_auth_reg.db")
	defer cleanup()

	handler := middleware.SessionMiddleware(repo)(mux)
	handler = middleware.UniversalCSRFMiddleware(cfg)(handler)

	// สร้าง CSRF Token ที่ถูกต้อง
	validToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	// การสมัครที่ 1: สมัครห้อง A501 (พิมพ์ตัวเล็ก 'a501')
	form := url.Values{}
	form.Set("csrf_token", validToken)
	form.Set("username", "a501")
	form.Set("full_name", "นายทดสอบ ผู้เช่า")
	form.Set("phone", "0812345678")
	form.Set("password", "pass1234")
	form.Set("confirm_password", "pass1234")

	req := httptest.NewRequest("POST", "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: validToken})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 เมื่อสมัครสำเร็จ แต่ได้ Status: %d", rec.Code)
	}

	// ตรวจสอบในฐานข้อมูลว่า Username ถูกแปลงเป็น 'A501' และ Status = 'pending', Role = 'user'
	user, err := repo.GetUserByUsername("A501")
	if err != nil {
		t.Fatalf("ไม่พบผู้ใช้ A501 ในฐานข้อมูล: %v", err)
	}
	if user.Username != "A501" {
		t.Errorf("คาดหวัง A501 แต่ได้ %s", user.Username)
	}
	if user.Role != models.RoleUser {
		t.Errorf("คาดหวัง Role = user แต่ได้ %s", user.Role)
	}
	if user.Status != models.UserStatusPending {
		t.Errorf("คาดหวัง Status = pending แต่ได้ %s", user.Status)
	}

	// การสมัครที่ 2: พยายามสมัครห้อง 'A501' ซ้ำอีกครั้ง
	reqDup := httptest.NewRequest("POST", "/register", strings.NewReader(form.Encode()))
	reqDup.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqDup.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: validToken})

	recDup := httptest.NewRecorder()
	handler.ServeHTTP(recDup, reqDup)

	if recDup.Code != http.StatusConflict {
		t.Errorf("คาดหวัง Status 409 Conflict เมื่อสมัครเลขห้องซ้ำ แต่ได้: %d", recDup.Code)
	}
	if !strings.Contains(recDup.Body.String(), "เลขห้องนี้มีอยู่ในระบบแล้ว") {
		t.Errorf("ไม่พบข้อความแจ้งเตือนเลขห้องซ้ำในผลลัพธ์")
	}
}

// 2. ทดสอบการเข้าสู่ระบบ: สำเร็จ และล้มเหลว
func TestAuth_LoginSuccessAndFailure(t *testing.T) {
	mux, repo, cfg, _, cleanup := setupTestApp(t, "test_auth_login.db")
	defer cleanup()

	handler := middleware.SessionMiddleware(repo)(mux)
	handler = middleware.UniversalCSRFMiddleware(cfg)(handler)

	validToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	// กรณีที่ 1: รหัสผ่านผิด
	formWrong := url.Values{}
	formWrong.Set("csrf_token", validToken)
	formWrong.Set("username", "ADMIN")
	formWrong.Set("password", "WrongPassword123")

	reqWrong := httptest.NewRequest("POST", "/login", strings.NewReader(formWrong.Encode()))
	reqWrong.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqWrong.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: validToken})

	recWrong := httptest.NewRecorder()
	handler.ServeHTTP(recWrong, reqWrong)

	if recWrong.Code != http.StatusUnauthorized {
		t.Errorf("คาดหวัง Status 401 เมื่อรหัสผ่านผิด แต่ได้: %d", recWrong.Code)
	}

	// กรณีที่ 2: รหัสผ่านถูกต้อง
	formCorrect := url.Values{}
	formCorrect.Set("csrf_token", validToken)
	formCorrect.Set("username", "admin") // พิมพ์ตัวเล็ก
	formCorrect.Set("password", "testAdminPassword123")

	reqCorrect := httptest.NewRequest("POST", "/login", strings.NewReader(formCorrect.Encode()))
	reqCorrect.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqCorrect.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: validToken})

	recCorrect := httptest.NewRecorder()
	handler.ServeHTTP(recCorrect, reqCorrect)

	if recCorrect.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 เมื่อเข้าสู่ระบบสำเร็จ แต่ได้: %d", recCorrect.Code)
	}

	// ตรวจสอบว่ามี Cookie dormfix_session ถูกส่งกลับมา
	cookies := recCorrect.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == middleware.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("ไม่พบ Session Cookie ในผลลัพธ์หลัง Login สำเร็จ")
	}
}

// 3. ทดสอบบัญชีสถานะ pending ไม่สามารถเข้าถึงหน้าฟังก์ชันผู้เช่าได้
func TestAuth_PendingUserBlockedFromUserRoutes(t *testing.T) {
	mux, repo, cfg, _, cleanup := setupTestApp(t, "test_auth_pending.db")
	defer cleanup()

	handler := middleware.SessionMiddleware(repo)(mux)
	handler = middleware.UniversalCSRFMiddleware(cfg)(handler)

	// สร้างผู้ใช้สถานะ pending
	pendingUser := &models.User{
		Username:     "P101",
		PasswordHash: "hash",
		FullName:     "ผู้เช่ารออนุมัติ",
		Phone:        "0811111111",
		Role:         models.RoleUser,
		Status:       models.UserStatusPending,
	}
	_ = repo.CreateUser(pendingUser)

	// สร้าง Session สำหรับ pending user
	sessionToken := "pending_user_session_token_123"
	_ = repo.CreateSession(&models.Session{
		Token:     sessionToken,
		UserID:    pendingUser.ID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})

	// พยายามเข้าถึง /user/requests (ที่ต้องใช้สถานะ active)
	req := httptest.NewRequest("GET", "/user/requests", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sessionToken})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// ต้องถูก Redirect ไปยัง /pending
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 แต่ได้: %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/pending" {
		t.Errorf("คาดหวัง Redirect ไปที่ /pending แต่ได้: %s", rec.Header().Get("Location"))
	}
}

// 4. ทดสอบ Session หมดอายุ ถูกปฏิเสธการเข้าถึง
func TestAuth_SessionExpiration(t *testing.T) {
	mux, repo, cfg, _, cleanup := setupTestApp(t, "test_auth_expire.db")
	defer cleanup()

	handler := middleware.SessionMiddleware(repo)(mux)
	handler = middleware.UniversalCSRFMiddleware(cfg)(handler)

	user := &models.User{
		Username:     "E101",
		PasswordHash: "hash",
		FullName:     "ผู้เช่าหมดอายุ",
		Phone:        "0811111111",
		Role:         models.RoleUser,
		Status:       models.UserStatusActive,
	}
	_ = repo.CreateUser(user)

	// สร้าง Session ที่หมดอายุไปแล้ว (ExpiresAt ในอดีต)
	expiredToken := "expired_session_token_999"
	_ = repo.CreateSession(&models.Session{
		Token:     expiredToken,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	})

	// พยายามเข้าถึง /user/requests
	req := httptest.NewRequest("GET", "/user/requests", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: expiredToken})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// ต้องถูกปฏิเสธและ Redirect ไปหน้า /login
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 เมื่อ Session หมดอายุ แต่ได้: %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("คาดหวัง Redirect ไปที่ /login แต่ได้: %s", rec.Header().Get("Location"))
	}
}

// 5. ทดสอบการ Logout: ลบ Session และเคลียร์ Cookie
func TestAuth_Logout(t *testing.T) {
	mux, repo, cfg, _, cleanup := setupTestApp(t, "test_auth_logout.db")
	defer cleanup()

	handler := middleware.SessionMiddleware(repo)(mux)
	handler = middleware.UniversalCSRFMiddleware(cfg)(handler)

	validToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	user := &models.User{
		Username:     "L101",
		PasswordHash: "hash",
		FullName:     "ผู้เช่าออกจากระบบ",
		Phone:        "0811111111",
		Role:         models.RoleUser,
		Status:       models.UserStatusActive,
	}
	_ = repo.CreateUser(user)

	sessionToken := "session_to_be_logged_out"
	_ = repo.CreateSession(&models.Session{
		Token:     sessionToken,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})

	// ส่งคำขอ Logout ด้วย POST พร้อม CSRF Token
	form := url.Values{}
	form.Set("csrf_token", validToken)

	req := httptest.NewRequest("POST", "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: validToken})
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sessionToken})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 เมื่อ Logout แต่ได้: %d", rec.Code)
	}

	// ตรวจสอบว่า Session ในฐานข้อมูลถูกลบไปแล้ว
	_, err := repo.GetSessionByToken(sessionToken)
	if err != repository.ErrSessionNotFound {
		t.Errorf("คาดหวังว่า Session ต้องถูกลบจากฐานข้อมูลแล้ว แต่ยังพบอยู่")
	}

	// ตรวจสอบว่า Cookie ถูกเคลียร์ (MaxAge < 0)
	var clearedCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.SessionCookieName {
			clearedCookie = c
			break
		}
	}
	if clearedCookie == nil || clearedCookie.MaxAge >= 0 {
		t.Errorf("Session Cookie ไม่ได้ถูกเคลียร์อย่างถูกต้อง")
	}
}

// 6. ทดสอบการป้องกัน Universal CSRF: ปฏิเสธคำขอเมื่อไม่มี Token หรือ Token ผิด
func TestAuth_UniversalCSRFProtection(t *testing.T) {
	mux, repo, cfg, _, cleanup := setupTestApp(t, "test_auth_csrf.db")
	defer cleanup()

	handler := middleware.SessionMiddleware(repo)(mux)
	handler = middleware.UniversalCSRFMiddleware(cfg)(handler)

	// กรณีที่ 1: ส่ง POST /login โดยไม่มี CSRF Token ➔ ต้องได้ 403 Forbidden
	formNoCSRF := url.Values{}
	formNoCSRF.Set("username", "admin")
	formNoCSRF.Set("password", "pass")

	reqNoCSRF := httptest.NewRequest("POST", "/login", strings.NewReader(formNoCSRF.Encode()))
	reqNoCSRF.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recNoCSRF := httptest.NewRecorder()
	handler.ServeHTTP(recNoCSRF, reqNoCSRF)

	if recNoCSRF.Code != http.StatusForbidden {
		t.Errorf("คาดหวัง HTTP 403 Forbidden เมื่อไม่มี CSRF Token แต่ได้: %d", recNoCSRF.Code)
	}

	// กรณีที่ 2: ส่ง POST /register โดยมี CSRF Token ปลอม (ผู้โจมตีพยายามส่ง Token เองที่ไม่ตรงกับ Server Secret) ➔ ต้องได้ 403 Forbidden
	fakeToken := "attacker_injected_fake_csrf_token"
	formFakeCSRF := url.Values{}
	formFakeCSRF.Set("csrf_token", fakeToken)
	formFakeCSRF.Set("username", "A999")
	formFakeCSRF.Set("password", "pass1234")

	reqFakeCSRF := httptest.NewRequest("POST", "/register", strings.NewReader(formFakeCSRF.Encode()))
	reqFakeCSRF.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqFakeCSRF.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: fakeToken})

	recFakeCSRF := httptest.NewRecorder()
	handler.ServeHTTP(recFakeCSRF, reqFakeCSRF)

	if recFakeCSRF.Code != http.StatusForbidden {
		t.Errorf("คาดหวัง HTTP 403 Forbidden เมื่อส่ง CSRF Token ปลอม แต่ได้: %d", recFakeCSRF.Code)
	}
}
