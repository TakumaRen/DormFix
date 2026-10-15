package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"dormfix/internal/config"
	"dormfix/internal/middleware"
	"dormfix/internal/models"
	"dormfix/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

// AuthHandler จัดการคำขอเกี่ยวกับระบบยืนยันตัวตน (Login, Register, Logout, Pending)
type AuthHandler struct {
	repo        *repository.UserRepository
	cfg         *config.Config
	templateDir string
}

// NewAuthHandler สร้าง instance ของ AuthHandler
func NewAuthHandler(repo *repository.UserRepository, cfg *config.Config, templateDir string) *AuthHandler {
	return &AuthHandler{
		repo:        repo,
		cfg:         cfg,
		templateDir: templateDir,
	}
}

// render เรนเดอร์ HTML Template ร่วมกับ Base Layout
func (h *AuthHandler) render(w http.ResponseWriter, r *http.Request, pageTmpl string, data map[string]interface{}) {
	basePath := filepath.Join(h.templateDir, "layouts", "base.html")
	pagePath := filepath.Join(h.templateDir, pageTmpl)

	tmpl, err := template.ParseFiles(basePath, pagePath)
	if err != nil {
		http.Error(w, "เกิดข้อผิดพลาดในการโหลดหน้าเว็บ: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if data == nil {
		data = make(map[string]interface{})
	}

	// แนบข้อมูลผู้ใช้และ CSRF Token เข้าสู่ Template อัตโนมัติ
	data["User"] = middleware.GetCurrentUser(r)
	data["CSRFToken"] = middleware.GetCSRFToken(r)

	if _, ok := data["Title"]; !ok {
		data["Title"] = "DormFix"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "เกิดข้อผิดพลาดในการแสดงผลหน้าเว็บ: "+err.Error(), http.StatusInternalServerError)
	}
}

// Root จัดการเส้นทางหน้าแรก (/) นำทางไปยังหน้าที่เหมาะสมตามสถานะการเข้าสู่ระบบ
func (h *AuthHandler) Root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	u := middleware.GetCurrentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if u.Role == models.RoleAdmin {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	if u.Status == models.UserStatusPending {
		http.Redirect(w, r, "/pending", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/user/requests", http.StatusSeeOther)
}

// LoginPage แสดงหน้าฟอร์มเข้าสู่ระบบ
func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)
	if u != nil {
		if u.Role == models.RoleAdmin {
			http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
			return
		}
		if u.Status == models.UserStatusPending {
			http.Redirect(w, r, "/pending", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/user/requests", http.StatusSeeOther)
		return
	}

	successMsg := r.URL.Query().Get("success")
	h.render(w, r, "auth/login.html", map[string]interface{}{
		"Title":   "เข้าสู่ระบบ",
		"Success": successMsg,
	})
}

// LoginSubmit ประมวลผลการเข้าสู่ระบบ
func (h *AuthHandler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	username := strings.ToUpper(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")

	if username == "" || password == "" {
		w.WriteHeader(http.StatusBadRequest)
		h.render(w, r, "auth/login.html", map[string]interface{}{
			"Title":    "เข้าสู่ระบบ",
			"Error":    "กรุณากรอกชื่อผู้ใช้/เลขห้องพักและรหัสผ่านให้ครบถ้วน",
			"Username": username,
		})
		return
	}

	user, err := h.repo.GetUserByUsername(username)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		h.render(w, r, "auth/login.html", map[string]interface{}{
			"Title":    "เข้าสู่ระบบ",
			"Error":    "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง",
			"Username": username,
		})
		return
	}

	// ตรวจสอบรหัสผ่านด้วย bcrypt
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		h.render(w, r, "auth/login.html", map[string]interface{}{
			"Title":    "เข้าสู่ระบบ",
			"Error":    "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง",
			"Username": username,
		})
		return
	}

	// ตรวจสอบสถานะบัญชีที่ถูกระงับ
	if user.Status == models.UserStatusSuspended {
		w.WriteHeader(http.StatusForbidden)
		h.render(w, r, "auth/login.html", map[string]interface{}{
			"Title":    "เข้าสู่ระบบ",
			"Error":    "บัญชีห้องพักนี้ถูกระงับการใช้งาน กรุณาติดต่อสำนักงานหอพัก",
			"Username": username,
		})
		return
	}

	// สร้าง Session Token สุ่ม 32 bytes (64 ตัวอักษร hex)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		http.Error(w, "ไม่สามารถสร้างเซสชันได้", http.StatusInternalServerError)
		return
	}
	sessionToken := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(24 * time.Hour)

	sess := &models.Session{
		Token:     sessionToken,
		UserID:    user.ID,
		ExpiresAt: expiresAt,
	}

	if err := h.repo.CreateSession(sess); err != nil {
		http.Error(w, "เกิดข้อผิดพลาดในการบันทึกเซสชัน", http.StatusInternalServerError)
		return
	}

	// ตั้งค่า Session Cookie ปลอดภัย
	middleware.SetSessionCookie(w, sessionToken, expiresAt, h.cfg.SecureCookie)

	// เปลี่ยนเส้นทางตาม Role และ Status
	if user.Role == models.RoleAdmin {
		http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
		return
	}

	if user.Status == models.UserStatusPending {
		http.Redirect(w, r, "/pending", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/user/requests", http.StatusSeeOther)
}

// RegisterPage แสดงหน้าฟอร์มสมัครสมาชิก
func (h *AuthHandler) RegisterPage(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)
	if u != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	h.render(w, r, "auth/register.html", map[string]interface{}{
		"Title": "สมัครสมาชิกผู้เช่าใหม่",
	})
}

// RegisterSubmit ประมวลผลการสมัครสมาชิก โดยบังคับ role เป็น user และ status เป็น pending เสมอ
func (h *AuthHandler) RegisterSubmit(w http.ResponseWriter, r *http.Request) {
	username := strings.ToUpper(strings.TrimSpace(r.FormValue("username")))
	fullName := strings.TrimSpace(r.FormValue("full_name"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	password := r.FormValue("password")
	confirmPassword := r.FormValue("confirm_password")

	formData := map[string]interface{}{
		"Title":    "สมัครสมาชิกผู้เช่าใหม่",
		"Username": username,
		"FullName": fullName,
		"Phone":    phone,
	}

	// 1. ตรวจสอบข้อมูลจำเป็น
	if username == "" || fullName == "" || phone == "" || password == "" {
		w.WriteHeader(http.StatusBadRequest)
		formData["Error"] = "กรุณากรอกข้อมูลให้ครบทุกช่องที่มีเครื่องหมาย *"
		h.render(w, r, "auth/register.html", formData)
		return
	}

	if len(password) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		formData["Error"] = "รหัสผ่านต้องมีความยาวอย่างน้อย 6 ตัวอักษร"
		h.render(w, r, "auth/register.html", formData)
		return
	}

	if password != confirmPassword {
		w.WriteHeader(http.StatusBadRequest)
		formData["Error"] = "รหัสผ่านและยืนยันรหัสผ่านไม่ตรงกัน"
		h.render(w, r, "auth/register.html", formData)
		return
	}

	// 2. ตรวจสอบเลขห้องซ้ำในระบบ
	exists, err := h.repo.CheckUsernameExists(username)
	if err != nil {
		http.Error(w, "เกิดข้อผิดพลาดในการตรวจสอบข้อมูล", http.StatusInternalServerError)
		return
	}
	if exists {
		w.WriteHeader(http.StatusConflict)
		formData["Error"] = "เลขห้องนี้มีอยู่ในระบบแล้ว หากท่านเป็นผู้เช่าใหม่ กรุณาติดต่อสำนักงานหอพัก"
		h.render(w, r, "auth/register.html", formData)
		return
	}

	// 3. เข้ารหัสผ่านด้วย bcrypt cost 10
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		http.Error(w, "ไม่สามารถเข้ารหัสผ่านได้", http.StatusInternalServerError)
		return
	}

	// 4. บันทึกผู้ใช้ใหม่ โดยกำหนด Role และ Status จากฝั่ง Backend เท่านั้น (ห้ามเชื่อค่าจาก Form)
	newUser := &models.User{
		Username:     username,
		PasswordHash: string(hashed),
		FullName:     fullName,
		Phone:        phone,
		Role:         models.RoleUser,          // บังคับ role = 'user' เสมอ
		Status:       models.UserStatusPending, // บังคับ status = 'pending' เสมอ
	}

	if err := h.repo.CreateUser(newUser); err != nil {
		if errors.Is(err, repository.ErrUsernameDuplicate) {
			w.WriteHeader(http.StatusConflict)
			formData["Error"] = "เลขห้องนี้มีอยู่ในระบบแล้ว หากท่านเป็นผู้เช่าใหม่ กรุณาติดต่อสำนักงานหอพัก"
			h.render(w, r, "auth/register.html", formData)
			return
		}
		http.Error(w, "เกิดข้อผิดพลาดในการบันทึกข้อมูล: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// สมัครสมาชิกสำเร็จ นำทางไปยังหน้า Login พร้อมแจ้งเตือนให้เข้าสู่ระบบ
	http.Redirect(w, r, "/login?success=ลงทะเบียนห้องพักเรียบร้อยแล้ว บัญชีของท่านอยู่ระหว่างรอการอนุมัติ กรุณาเข้าสู่ระบบเพื่อตรวจสอบสถานะ", http.StatusSeeOther)
}

// PendingPage แสดงหน้าจอแจ้งเตือนว่ากำลังรอการอนุมัติจาก Admin
func (h *AuthHandler) PendingPage(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// หากได้รับอนุมัติแล้ว นำทางไปยังหน้าของบทบาทนั้น
	if u.Status == models.UserStatusActive {
		if u.Role == models.RoleAdmin {
			http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/user/requests", http.StatusSeeOther)
		return
	}

	h.render(w, r, "auth/pending.html", map[string]interface{}{
		"Title": "รอการอนุมัติห้องพัก",
	})
}

// Logout ออกจากระบบ ยกเลิก Session ในฐานข้อมูล และเคลียร์ Cookie
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(middleware.SessionCookieName)
	if err == nil && cookie.Value != "" {
		_ = h.repo.DeleteSession(cookie.Value)
	}

	middleware.ClearSessionCookie(w, h.cfg.SecureCookie)
	http.Redirect(w, r, "/login?success=ออกจากระบบเรียบร้อยแล้ว", http.StatusSeeOther)
}
