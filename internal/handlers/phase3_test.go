package handlers

import (
	"database/sql"
	"fmt"
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

func setupPhase3App(t *testing.T, dbName string) (http.Handler, *repository.UserRepository, *repository.RequestRepository, *config.Config, *sql.DB, func()) {
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

	userRepo := repository.NewUserRepository(db)
	reqRepo := repository.NewRequestRepository(db)

	cwd, _ := os.Getwd()
	var templateDir string
	if strings.HasSuffix(cwd, "handlers") {
		templateDir = filepath.Join("..", "..", "web", "templates")
	} else {
		templateDir = filepath.Join("web", "templates")
	}

	authHandler := NewAuthHandler(userRepo, cfg, templateDir)
	userHandler := NewUserHandler(reqRepo, cfg, templateDir)
	adminHandler := NewAdminHandler(reqRepo, userRepo, cfg, templateDir)

	mux := http.NewServeMux()

	// Auth routes
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

	// User routes
	userMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/user/requests" || path == "/user/requests/":
			if r.Method == http.MethodGet {
				userHandler.ListRequests(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		case path == "/user/requests/new":
			if r.Method == http.MethodPost {
				userHandler.CreateRequestSubmit(w, r)
			} else if r.Method == http.MethodGet {
				userHandler.CreateRequestPage(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		case strings.HasSuffix(path, "/edit"):
			if r.Method == http.MethodPost {
				userHandler.EditRequestSubmit(w, r)
			} else if r.Method == http.MethodGet {
				userHandler.EditRequestPage(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		case strings.HasSuffix(path, "/cancel"):
			if r.Method == http.MethodPost {
				userHandler.CancelRequest(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		default:
			if r.Method == http.MethodGet {
				userHandler.DetailRequest(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		}
	})
	mux.Handle("/user/requests", middleware.RequireActiveUser(userMux))
	mux.Handle("/user/requests/", middleware.RequireActiveUser(userMux))

	// Admin routes
	mux.Handle("/admin/dashboard", middleware.RequireAdmin(http.HandlerFunc(adminHandler.Dashboard)))

	adminRequestsMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/admin/requests" || path == "/admin/requests/" {
			if r.Method == http.MethodGet {
				adminHandler.ListRequests(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if r.Method == http.MethodPost {
			adminHandler.UpdateRequestSubmit(w, r)
		} else if r.Method == http.MethodGet {
			adminHandler.DetailRequest(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.Handle("/admin/requests", middleware.RequireAdmin(adminRequestsMux))
	mux.Handle("/admin/requests/", middleware.RequireAdmin(adminRequestsMux))

	adminUsersMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/admin/users" || path == "/admin/users/":
			if r.Method == http.MethodGet {
				adminHandler.ListUsers(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		case strings.HasSuffix(path, "/approve"):
			if r.Method == http.MethodPost {
				adminHandler.ApproveUser(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		case strings.HasSuffix(path, "/suspend"):
			if r.Method == http.MethodPost {
				adminHandler.SuspendUser(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		case strings.HasSuffix(path, "/archive"):
			if r.Method == http.MethodPost {
				adminHandler.ArchiveUser(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		default:
			http.NotFound(w, r)
		}
	})
	mux.Handle("/admin/users", middleware.RequireAdmin(adminUsersMux))
	mux.Handle("/admin/users/", middleware.RequireAdmin(adminUsersMux))

	handler := middleware.SessionMiddleware(userRepo)(mux)
	handler = middleware.UniversalCSRFMiddleware(cfg)(handler)

	cleanup := func() {
		db.Close()
		os.Remove(testDBPath)
	}

	return handler, userRepo, reqRepo, cfg, db, cleanup
}

// helper create active session
func createActiveSession(t *testing.T, userRepo *repository.UserRepository, u *models.User, token string) {
	t.Helper()
	err := userRepo.CreateSession(&models.Session{
		Token:     token,
		UserID:    u.ID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("สร้าง Session ล้มเหลว: %v", err)
	}
}

// 1. ผู้เช่าสร้างใบแจ้งซ่อมและบันทึกลงฐานข้อมูลได้
func TestPhase3_1_TenantCreateRequest(t *testing.T) {
	app, userRepo, reqRepo, cfg, _, cleanup := setupPhase3App(t, "phase3_t1.db")
	defer cleanup()

	tenant := &models.User{Username: "A101", FullName: "สมชาย รักสงบ", Phone: "0811111111", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenant)
	createActiveSession(t, userRepo, tenant, "session_tenant_a")

	csrfToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	form := url.Values{}
	form.Set("csrf_token", csrfToken)
	form.Set("category", "ไฟฟ้า")
	form.Set("title", "หลอดไฟระเบียงดับ")
	form.Set("description", "เปิดสวิตช์แล้วไฟกระพริบแล้วดับไปเลย")
	form.Set("convenient_time", "วันจันทร์-ศุกร์ หลัง 17:00 น.")

	req := httptest.NewRequest("POST", "/user/requests/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "session_tenant_a"})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 แต่ได้ %d: %s", rec.Code, rec.Body.String())
	}

	// ตรวจสอบในฐานข้อมูล
	requests, err := reqRepo.ListRequestsByUserID(tenant.ID)
	if err != nil || len(requests) != 1 {
		t.Fatalf("คาดหวังพบ 1 คำร้องใน DB แต่พบ %d (err: %v)", len(requests), err)
	}
	r := requests[0]
	if r.RoomNo != "A101" || r.Status != models.RequestStatusPending || r.Title != "หลอดไฟระเบียงดับ" {
		t.Errorf("ข้อมูลคำร้องไม่ตรงตามที่ส่ง: %+v", r)
	}
}

// 2. ผู้เช่าดูเฉพาะคำร้องของตัวเอง
func TestPhase3_2_TenantViewOwnRequestsOnly(t *testing.T) {
	app, userRepo, reqRepo, _, _, cleanup := setupPhase3App(t, "phase3_t2.db")
	defer cleanup()

	tenantA := &models.User{Username: "A101", FullName: "ผู้เช่า A", Role: models.RoleUser, Status: models.UserStatusActive}
	tenantB := &models.User{Username: "B202", FullName: "ผู้เช่า B", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenantA)
	_ = userRepo.CreateUser(tenantB)
	createActiveSession(t, userRepo, tenantA, "sess_a")
	createActiveSession(t, userRepo, tenantB, "sess_b")

	_ = reqRepo.CreateRequest(&models.MaintenanceRequest{UserID: tenantA.ID, RoomNo: "A101", Category: "ประปา", Title: "ก๊อกน้ำห้อง A รั่ว", Description: "d", ConvenientTime: "t"})
	_ = reqRepo.CreateRequest(&models.MaintenanceRequest{UserID: tenantB.ID, RoomNo: "B202", Category: "ไฟฟ้า", Title: "ปลั๊กไฟห้อง B เสีย", Description: "d", ConvenientTime: "t"})

	// ส่งคำขอโดย Tenant A
	req := httptest.NewRequest("GET", "/user/requests", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("คาดหวัง Status 200 แต่ได้ %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "ก๊อกน้ำห้อง A รั่ว") {
		t.Errorf("ผู้เช่า A ต้องมองเห็นคำร้องของตนเอง")
	}
	if strings.Contains(body, "ปลั๊กไฟห้อง B เสีย") {
		t.Errorf("ผู้เช่า A ต้องมองไม่เห็นคำร้องของห้อง B")
	}
}

// 3. ผู้เช่า A พยายามดู แก้ไข และยกเลิกคำร้องของผู้เช่า B ต้องถูกปฏิเสธ
func TestPhase3_3_TenantCrossAccessForbidden(t *testing.T) {
	app, userRepo, reqRepo, cfg, _, cleanup := setupPhase3App(t, "phase3_t3.db")
	defer cleanup()

	tenantA := &models.User{Username: "A101", FullName: "ผู้เช่า A", Role: models.RoleUser, Status: models.UserStatusActive}
	tenantB := &models.User{Username: "B202", FullName: "ผู้เช่า B", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenantA)
	_ = userRepo.CreateUser(tenantB)
	createActiveSession(t, userRepo, tenantA, "sess_a")

	reqB := &models.MaintenanceRequest{
		UserID:         tenantB.ID,
		RoomNo:         "B202",
		Category:       "แอร์",
		Title:          "แอร์ห้อง B ไม่เย็น",
		Description:    "ร้อนมาก",
		ConvenientTime: "บ่าย",
	}
	_ = reqRepo.CreateRequest(reqB)

	csrfToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	// 3.1 Tenant A พยายามดูรายละเอียดคำร้องของ B
	reqView := httptest.NewRequest("GET", fmt.Sprintf("/user/requests/%d", reqB.ID), nil)
	reqView.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})
	recView := httptest.NewRecorder()
	app.ServeHTTP(recView, reqView)

	if recView.Code != http.StatusForbidden {
		t.Errorf("คาดหวัง 403 Forbidden เมื่อผู้เช่า A ดูคำร้องของ B แต่ได้ %d", recView.Code)
	}

	// 3.2 Tenant A พยายามแก้ไขคำร้องของ B
	formEdit := url.Values{}
	formEdit.Set("csrf_token", csrfToken)
	formEdit.Set("category", "แอร์")
	formEdit.Set("title", "แอบเปลี่ยนหัวข้อ")
	formEdit.Set("description", "เปลี่ยนรายละเอียด")
	formEdit.Set("convenient_time", "เย็น")

	reqEdit := httptest.NewRequest("POST", fmt.Sprintf("/user/requests/%d/edit", reqB.ID), strings.NewReader(formEdit.Encode()))
	reqEdit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqEdit.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqEdit.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})
	recEdit := httptest.NewRecorder()
	app.ServeHTTP(recEdit, reqEdit)

	if recEdit.Code != http.StatusForbidden {
		t.Errorf("คาดหวัง 403 Forbidden เมื่อผู้เช่า A พยายามแก้ไขคำร้องของ B แต่ได้ %d", recEdit.Code)
	}

	// 3.3 Tenant A พยายามยกเลิกคำร้องของ B
	formCancel := url.Values{}
	formCancel.Set("csrf_token", csrfToken)
	reqCancel := httptest.NewRequest("POST", fmt.Sprintf("/user/requests/%d/cancel", reqB.ID), strings.NewReader(formCancel.Encode()))
	reqCancel.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqCancel.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqCancel.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})
	recCancel := httptest.NewRecorder()
	app.ServeHTTP(recCancel, reqCancel)

	if recCancel.Code != http.StatusForbidden {
		t.Errorf("คาดหวัง 403 Forbidden เมื่อผู้เช่า A พยายามยกเลิกคำร้องของ B แต่ได้ %d", recCancel.Code)
	}
}

// 4. ผู้เช่าแก้ไขและยกเลิกคำร้องสถานะ pending ได้
func TestPhase3_4_TenantEditAndCancelPendingRequest(t *testing.T) {
	app, userRepo, reqRepo, cfg, _, cleanup := setupPhase3App(t, "phase3_t4.db")
	defer cleanup()

	tenant := &models.User{Username: "A101", FullName: "ผู้เช่า A", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenant)
	createActiveSession(t, userRepo, tenant, "sess_a")

	req := &models.MaintenanceRequest{
		UserID:         tenant.ID,
		RoomNo:         "A101",
		Category:       "สุขภัณฑ์",
		Title:          "ฝักบัวตัน",
		Description:    "น้ำไหลเบา",
		ConvenientTime: "เสาร์-อาทิตย์",
	}
	_ = reqRepo.CreateRequest(req)

	csrfToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	// 4.1 แก้ไขคำร้อง
	formEdit := url.Values{}
	formEdit.Set("csrf_token", csrfToken)
	formEdit.Set("category", "สุขภัณฑ์")
	formEdit.Set("title", "ฝักบัวตันและสายยางรั่วซึม")
	formEdit.Set("description", "น้ำพุ่งออกจากสายยางและหัวฝักบัวตัน")
	formEdit.Set("convenient_time", "วันเสาร์ 13:00 - 15:00 น.")

	reqEdit := httptest.NewRequest("POST", fmt.Sprintf("/user/requests/%d/edit", req.ID), strings.NewReader(formEdit.Encode()))
	reqEdit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqEdit.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqEdit.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})
	recEdit := httptest.NewRecorder()
	app.ServeHTTP(recEdit, reqEdit)

	if recEdit.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 หลังแก้ไขสำเร็จ แต่ได้ %d", recEdit.Code)
	}

	updated, _ := reqRepo.GetRequestByID(req.ID)
	if updated.Title != "ฝักบัวตันและสายยางรั่วซึม" {
		t.Errorf("หัวข้อไม่ได้รับการอัปเดต")
	}

	// 4.2 ยกเลิกคำร้อง
	formCancel := url.Values{}
	formCancel.Set("csrf_token", csrfToken)
	reqCancel := httptest.NewRequest("POST", fmt.Sprintf("/user/requests/%d/cancel", req.ID), strings.NewReader(formCancel.Encode()))
	reqCancel.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqCancel.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqCancel.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})
	recCancel := httptest.NewRecorder()
	app.ServeHTTP(recCancel, reqCancel)

	if recCancel.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 หลังยกเลิกสำเร็จ แต่ได้ %d", recCancel.Code)
	}

	cancelled, _ := reqRepo.GetRequestByID(req.ID)
	if cancelled.Status != models.RequestStatusCancelled {
		t.Errorf("คาดหวังสถานะ cancelled แต่ได้ %s", cancelled.Status)
	}
}

// 5. ผู้เช่าแก้ไขหรือยกเลิกคำร้องสถานะ in_progress และ completed ไม่ได้
func TestPhase3_5_TenantCannotEditOrCancelNonPending(t *testing.T) {
	app, userRepo, reqRepo, cfg, _, cleanup := setupPhase3App(t, "phase3_t5.db")
	defer cleanup()

	tenant := &models.User{Username: "A101", FullName: "ผู้เช่า A", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenant)
	createActiveSession(t, userRepo, tenant, "sess_a")

	req1 := &models.MaintenanceRequest{UserID: tenant.ID, RoomNo: "A101", Category: "ไฟฟ้า", Title: "งานกำลังซ่อม", Description: "d", ConvenientTime: "t"}
	_ = reqRepo.CreateRequest(req1)
	_ = reqRepo.AdminUpdateRequest(req1.ID, models.RequestStatusInProgress, "ช่างกำลังเดินทาง")

	req2 := &models.MaintenanceRequest{UserID: tenant.ID, RoomNo: "A101", Category: "ไฟฟ้า", Title: "งานเสร็จแล้ว", Description: "d", ConvenientTime: "t"}
	_ = reqRepo.CreateRequest(req2)
	_ = reqRepo.AdminUpdateRequest(req2.ID, models.RequestStatusCompleted, "เสร็จสิ้น")

	csrfToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	// พยายามแก้ไขคำร้องสถานะ in_progress
	formEdit := url.Values{}
	formEdit.Set("csrf_token", csrfToken)
	formEdit.Set("category", "ไฟฟ้า")
	formEdit.Set("title", "พยายามแก้ไข in_progress")
	formEdit.Set("description", "d")
	formEdit.Set("convenient_time", "t")

	reqEdit1 := httptest.NewRequest("POST", fmt.Sprintf("/user/requests/%d/edit", req1.ID), strings.NewReader(formEdit.Encode()))
	reqEdit1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqEdit1.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqEdit1.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})
	recEdit1 := httptest.NewRecorder()
	app.ServeHTTP(recEdit1, reqEdit1)

	if recEdit1.Code != http.StatusBadRequest {
		t.Errorf("คาดหวัง 400 Bad Request เมื่อแก้ไขคำร้อง in_progress แต่ได้ %d", recEdit1.Code)
	}

	// พยายามยกเลิกคำร้องสถานะ completed
	formCancel := url.Values{}
	formCancel.Set("csrf_token", csrfToken)
	reqCancel2 := httptest.NewRequest("POST", fmt.Sprintf("/user/requests/%d/cancel", req2.ID), strings.NewReader(formCancel.Encode()))
	reqCancel2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqCancel2.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqCancel2.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})
	recCancel2 := httptest.NewRecorder()
	app.ServeHTTP(recCancel2, reqCancel2)

	// ต้องถูก redirect กลับพร้อม error message
	if recCancel2.Code != http.StatusSeeOther || !strings.Contains(recCancel2.Header().Get("Location"), "error=") {
		t.Errorf("คาดหวัง Redirect พร้อม error เมื่อยกเลิกคำร้อง completed แต่ได้ Code: %d, Location: %s", recCancel2.Code, recCancel2.Header().Get("Location"))
	}
}

// 6. การยกเลิกต้องเปลี่ยนเป็น cancelled และยังเก็บข้อมูลคำร้องไว้ (Soft Cancel)
func TestPhase3_6_SoftCancelPreservesData(t *testing.T) {
	_, userRepo, reqRepo, _, _, cleanup := setupPhase3App(t, "phase3_t6.db")
	defer cleanup()

	tenant := &models.User{Username: "A101", FullName: "ผู้เช่า A", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenant)

	req := &models.MaintenanceRequest{
		UserID:         tenant.ID,
		RoomNo:         "A101",
		Category:       "เฟอร์นิเจอร์",
		Title:          "โต๊ะอ่านหนังสือโยก",
		Description:    "ขาโต๊ะหลวม",
		ConvenientTime: "ตลอดเวลา",
	}
	_ = reqRepo.CreateRequest(req)

	err := reqRepo.CancelRequest(req.ID, tenant.ID)
	if err != nil {
		t.Fatalf("CancelRequest ล้มเหลว: %v", err)
	}

	// ตรวจสอบว่า record ยังอยู่ ไม่ถูกลบออกจาก DB
	cancelled, err := reqRepo.GetRequestByID(req.ID)
	if err != nil {
		t.Fatalf("ไม่พบคำร้องหลัง Soft Cancel (ห้ามลบข้อมูล): %v", err)
	}
	if cancelled.Status != models.RequestStatusCancelled {
		t.Errorf("สถานะต้องเป็น cancelled แต่ได้ %s", cancelled.Status)
	}
	if cancelled.Title != "โต๊ะอ่านหนังสือโยก" || cancelled.Description != "ขาโต๊ะหลวม" {
		t.Errorf("ข้อมูลเดิมสูญหายหลังยกเลิก")
	}
}

// 7. Admin เปลี่ยนสถานะและบันทึกหมายเหตุได้ (Form 2)
func TestPhase3_7_AdminStatusAndNotesUpdate(t *testing.T) {
	app, userRepo, reqRepo, cfg, _, cleanup := setupPhase3App(t, "phase3_t7.db")
	defer cleanup()

	admin, _ := userRepo.GetUserByUsername("ADMIN")
	createActiveSession(t, userRepo, admin, "sess_admin")

	tenant := &models.User{Username: "A101", FullName: "ผู้เช่า A", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenant)

	req := &models.MaintenanceRequest{
		UserID:         tenant.ID,
		RoomNo:         "A101",
		Category:       "ประปา",
		Title:          "ท่อน้ำซึม",
		Description:    "ใต้ซิงก์",
		ConvenientTime: "เสาร์เช้า",
	}
	_ = reqRepo.CreateRequest(req)

	csrfToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	form := url.Values{}
	form.Set("csrf_token", csrfToken)
	form.Set("status", models.RequestStatusCompleted)
	form.Set("admin_notes", "ช่างสมคิดเปลี่ยนปะเก็นยางและขันข้อต่อท่อเสร็จเรียบร้อยแล้ว ไม่รั่วซึม")

	postReq := httptest.NewRequest("POST", fmt.Sprintf("/admin/requests/%d", req.ID), strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	postReq.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_admin"})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, postReq)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 หลัง Admin อัปเดตคำร้อง แต่ได้ %d", rec.Code)
	}

	updated, _ := reqRepo.GetRequestByID(req.ID)
	if updated.Status != models.RequestStatusCompleted {
		t.Errorf("คาดหวังสถานะ completed แต่ได้ %s", updated.Status)
	}
	if !strings.Contains(updated.AdminNotes, "เปลี่ยนปะเก็นยาง") {
		t.Errorf("หมายเหตุช่างไม่ได้รับการบันทึก: %s", updated.AdminNotes)
	}
}

// 8. ผู้ใช้ที่ไม่ใช่ Admin เข้าถึงเส้นทาง Admin ไม่ได้ (403)
func TestPhase3_8_NonAdminForbiddenFromAdminRoutes(t *testing.T) {
	app, userRepo, _, _, _, cleanup := setupPhase3App(t, "phase3_t8.db")
	defer cleanup()

	tenant := &models.User{Username: "A101", FullName: "ผู้เช่าทั่วไป", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenant)
	createActiveSession(t, userRepo, tenant, "sess_regular_user")

	adminEndpoints := []string{
		"/admin/dashboard",
		"/admin/requests",
		"/admin/users",
	}

	for _, ep := range adminEndpoints {
		req := httptest.NewRequest("GET", ep, nil)
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_regular_user"})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("ผู้ใช้ทั่วไปต้องถูกปฏิเสธ 403 เมื่อเข้า %s แต่ได้ Status: %d", ep, rec.Code)
		}
	}
}

// 9. Admin อนุมัติ ระงับ และ Archive บัญชีได้ตามสิทธิ์
func TestPhase3_9_AdminApproveSuspendArchiveUser(t *testing.T) {
	app, userRepo, _, cfg, _, cleanup := setupPhase3App(t, "phase3_t9.db")
	defer cleanup()

	admin, _ := userRepo.GetUserByUsername("ADMIN")
	createActiveSession(t, userRepo, admin, "sess_admin")

	tenant := &models.User{Username: "B202", FullName: "ผู้เช่า B", Phone: "082", Role: models.RoleUser, Status: models.UserStatusPending}
	_ = userRepo.CreateUser(tenant)

	csrfToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	// 9.1 อนุมัติผู้เช่า (pending -> active)
	form := url.Values{}
	form.Set("csrf_token", csrfToken)
	reqApprove := httptest.NewRequest("POST", fmt.Sprintf("/admin/users/%d/approve", tenant.ID), strings.NewReader(form.Encode()))
	reqApprove.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqApprove.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqApprove.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_admin"})
	recApprove := httptest.NewRecorder()
	app.ServeHTTP(recApprove, reqApprove)

	if recApprove.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 หลังอนุมัติ แต่ได้ %d", recApprove.Code)
	}
	u1, _ := userRepo.GetUserByID(tenant.ID)
	if u1.Status != models.UserStatusActive {
		t.Errorf("สถานะต้องเป็น active หลังอนุมัติ แต่ได้ %s", u1.Status)
	}

	// 9.2 ระงับบัญชี (active -> suspended)
	reqSuspend := httptest.NewRequest("POST", fmt.Sprintf("/admin/users/%d/suspend", tenant.ID), strings.NewReader(form.Encode()))
	reqSuspend.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSuspend.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqSuspend.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_admin"})
	recSuspend := httptest.NewRecorder()
	app.ServeHTTP(recSuspend, reqSuspend)

	if recSuspend.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 หลังระงับ แต่ได้ %d", recSuspend.Code)
	}
	u2, _ := userRepo.GetUserByID(tenant.ID)
	if u2.Status != models.UserStatusSuspended {
		t.Errorf("สถานะต้องเป็น suspended แต่ได้ %s", u2.Status)
	}

	// 9.3 Archive บัญชี (suspended -> archived)
	reqArchive := httptest.NewRequest("POST", fmt.Sprintf("/admin/users/%d/archive", tenant.ID), strings.NewReader(form.Encode()))
	reqArchive.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqArchive.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqArchive.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_admin"})
	recArchive := httptest.NewRecorder()
	app.ServeHTTP(recArchive, reqArchive)

	if recArchive.Code != http.StatusSeeOther {
		t.Fatalf("คาดหวัง Redirect 303 หลังจัดเก็บ แต่ได้ %d", recArchive.Code)
	}
	u3, _ := userRepo.GetUserByID(tenant.ID)
	if u3.Status != models.UserStatusArchived {
		t.Errorf("สถานะต้องเป็น archived แต่ได้ %s", u3.Status)
	}
	expectedArchivedUsername := fmt.Sprintf("B202_ARCHIVED_%d", tenant.ID)
	if u3.Username != expectedArchivedUsername {
		t.Errorf("คาดหวังชื่อผู้ใช้จัดเก็บเป็น %s แต่ได้ %s", expectedArchivedUsername, u3.Username)
	}
}

// 10. การ Archive ผู้เช่าเดิมไม่ทำให้ผู้เช่าใหม่เห็นประวัติคำร้องเดิม
func TestPhase3_10_ArchiveTenantDataIsolationForNewTenant(t *testing.T) {
	app, userRepo, reqRepo, cfg, _, cleanup := setupPhase3App(t, "phase3_t10.db")
	defer cleanup()

	admin, _ := userRepo.GetUserByUsername("ADMIN")
	createActiveSession(t, userRepo, admin, "sess_admin")

	// 10.1 ผู้เช่าเดิมพักห้อง 301
	oldTenant := &models.User{Username: "301", FullName: "ผู้เช่าคนเก่า", Phone: "081", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(oldTenant)

	// ผู้เช่าเดิมเคยแจ้งซ่อมไว้ 2 รายการ
	_ = reqRepo.CreateRequest(&models.MaintenanceRequest{UserID: oldTenant.ID, RoomNo: "301", Category: "ไฟฟ้า", Title: "ปลั๊กไหม้ (ของเก่า)", Description: "d", ConvenientTime: "t"})
	_ = reqRepo.CreateRequest(&models.MaintenanceRequest{UserID: oldTenant.ID, RoomNo: "301", Category: "ประปา", Title: "ท่อแตก (ของเก่า)", Description: "d", ConvenientTime: "t"})

	csrfToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	// 10.2 ผู้เช่าเก่าย้ายออก -> Admin ทำการ Archive
	form := url.Values{}
	form.Set("csrf_token", csrfToken)
	reqArchive := httptest.NewRequest("POST", fmt.Sprintf("/admin/users/%d/archive", oldTenant.ID), strings.NewReader(form.Encode()))
	reqArchive.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqArchive.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqArchive.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_admin"})
	recArchive := httptest.NewRecorder()
	app.ServeHTTP(recArchive, reqArchive)

	if recArchive.Code != http.StatusSeeOther {
		t.Fatalf("Archive ล้มเหลว")
	}

	// 10.3 ผู้เช่าคนใหม่สมัครห้อง 301 เดิมได้สำเร็จ (เพราะชื่อเก่าถูกเปลี่ยนเป็น 301_ARCHIVED_<ID> แล้ว)
	formReg := url.Values{}
	formReg.Set("csrf_token", csrfToken)
	formReg.Set("username", "301")
	formReg.Set("full_name", "ผู้เช่าคนใหม่เอี่ยม")
	formReg.Set("phone", "0899999999")
	formReg.Set("password", "newPass123")
	formReg.Set("confirm_password", "newPass123")

	reqReg := httptest.NewRequest("POST", "/register", strings.NewReader(formReg.Encode()))
	reqReg.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqReg.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	recReg := httptest.NewRecorder()
	app.ServeHTTP(recReg, reqReg)

	if recReg.Code != http.StatusSeeOther {
		t.Fatalf("ผู้เช่าใหม่สมัครเลขห้องเดิมไม่สำเร็จ: %d, body: %s", recReg.Code, recReg.Body.String())
	}

	newTenant, err := userRepo.GetUserByUsername("301")
	if err != nil {
		t.Fatalf("ไม่พบผู้เช่าใหม่ห้อง 301 ในระบบ: %v", err)
	}

	// Admin อนุมัติผู้เช่าใหม่
	_ = userRepo.UpdateUserStatus(newTenant.ID, models.UserStatusActive)
	createActiveSession(t, userRepo, newTenant, "sess_new_tenant")

	// 10.4 ผู้เช่าใหม่เข้าดูรายการแจ้งซ่อมของตนเอง -> ต้องไม่เห็นคำร้องของผู้เช่าเดิม (0 รายการ)
	reqList := httptest.NewRequest("GET", "/user/requests", nil)
	reqList.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_new_tenant"})
	recList := httptest.NewRecorder()
	app.ServeHTTP(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("ผู้เช่าใหม่เรียกดูรายการไม่สำเร็จ: %d", recList.Code)
	}

	body := recList.Body.String()
	if strings.Contains(body, "ของเก่า") {
		t.Errorf("ผู้เช่าใหม่ต้องไม่เห็นประวัติการแจ้งซ่อมของผู้เช่าเดิมเด็ดขาด!")
	}
	if !strings.Contains(body, "ยังไม่มีประวัติการแจ้งซ่อม") {
		t.Errorf("ผู้เช่าใหม่ที่ยังไม่เคยแจ้งซ่อมต้องแสดงสถานะยังไม่มีประวัติ")
	}

	// แต่ในฐานข้อมูล คำร้องเดิมยังคงเชื่อมโยงกับ user_id เดิม
	oldRequests, err := reqRepo.ListRequestsByUserID(oldTenant.ID)
	if err != nil || len(oldRequests) != 2 {
		t.Errorf("คำร้องเดิมของผู้เช่าเก่าต้องยังคงอยู่ในฐานข้อมูลครบ 2 รายการ แต่พบ %d", len(oldRequests))
	}
}

// 11. การระงับบัญชีทำให้ Session เดิมไม่สามารถใช้งานต่อได้
func TestPhase3_11_SuspensionInvalidatesActiveSessions(t *testing.T) {
	app, userRepo, _, cfg, _, cleanup := setupPhase3App(t, "phase3_t11.db")
	defer cleanup()

	admin, _ := userRepo.GetUserByUsername("ADMIN")
	createActiveSession(t, userRepo, admin, "sess_admin")

	tenant := &models.User{Username: "A505", FullName: "ผู้เช่าระงับ", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenant)
	tenantSessionToken := "active_tenant_token_to_be_revoked"
	createActiveSession(t, userRepo, tenant, tenantSessionToken)

	// ตรวจสอบก่อนระงับ: ผู้เช่าเข้าถึงหน้า /user/requests ได้ (HTTP 200)
	reqBefore := httptest.NewRequest("GET", "/user/requests", nil)
	reqBefore.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: tenantSessionToken})
	recBefore := httptest.NewRecorder()
	app.ServeHTTP(recBefore, reqBefore)
	if recBefore.Code != http.StatusOK {
		t.Fatalf("ก่อนระงับต้องเข้าถึงได้ แต่ได้ %d", recBefore.Code)
	}

	csrfToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	// Admin สั่งระงับบัญชี
	form := url.Values{}
	form.Set("csrf_token", csrfToken)
	reqSuspend := httptest.NewRequest("POST", fmt.Sprintf("/admin/users/%d/suspend", tenant.ID), strings.NewReader(form.Encode()))
	reqSuspend.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSuspend.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqSuspend.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_admin"})
	recSuspend := httptest.NewRecorder()
	app.ServeHTTP(recSuspend, reqSuspend)

	if recSuspend.Code != http.StatusSeeOther {
		t.Fatalf("ระงับบัญชีไม่สำเร็จ")
	}

	// ตรวจสอบหลังระงับ: Session Token เดิมต้องถูกลบจาก DB
	_, err := userRepo.GetSessionByToken(tenantSessionToken)
	if err != repository.ErrSessionNotFound {
		t.Errorf("Session ของผู้ใช้ที่ถูกระงับต้องถูกลบทันที")
	}

	// ส่งคำขอด้วย Token เดิมอีกครั้ง -> ต้องถูกตีกลับไปหน้า Login
	reqAfter := httptest.NewRequest("GET", "/user/requests", nil)
	reqAfter.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: tenantSessionToken})
	recAfter := httptest.NewRecorder()
	app.ServeHTTP(recAfter, reqAfter)

	if recAfter.Code != http.StatusSeeOther || recAfter.Header().Get("Location") != "/login" {
		t.Errorf("ผู้ใช้ที่ถูกระงับต้องถูก Redirect ไป /login แต่ได้ Code: %d, Location: %s", recAfter.Code, recAfter.Header().Get("Location"))
	}
}

// 12. การป้องกัน Admin ทำให้ระบบไม่มี Admin ที่ยังใช้งานได้
func TestPhase3_12_AdminZeroProtection(t *testing.T) {
	app, userRepo, _, cfg, _, cleanup := setupPhase3App(t, "phase3_t12.db")
	defer cleanup()

	admin1, _ := userRepo.GetUserByUsername("ADMIN")
	createActiveSession(t, userRepo, admin1, "sess_admin1")

	csrfToken := middleware.GenerateCSRFToken(cfg.SessionSecret)

	// 12.1 Admin พยายามระงับบัญชีตนเอง -> ต้องถูกปฏิเสธ
	form := url.Values{}
	form.Set("csrf_token", csrfToken)

	reqSelfSuspend := httptest.NewRequest("POST", fmt.Sprintf("/admin/users/%d/suspend", admin1.ID), strings.NewReader(form.Encode()))
	reqSelfSuspend.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSelfSuspend.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqSelfSuspend.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_admin1"})
	recSelfSuspend := httptest.NewRecorder()
	app.ServeHTTP(recSelfSuspend, reqSelfSuspend)

	if !strings.Contains(recSelfSuspend.Header().Get("Location"), "error=") {
		t.Errorf("Admin ต้องไม่สามารถระงับบัญชีตนเองได้ Location: %s", recSelfSuspend.Header().Get("Location"))
	}

	// ตรวจสอบสถานะว่ายังเป็น active
	adminCheck, _ := userRepo.GetUserByID(admin1.ID)
	if adminCheck.Status != models.UserStatusActive {
		t.Errorf("Admin ต้องยังคงมีสถานะ active อยู่")
	}

	// 12.2 สร้าง Admin คนที่สอง (admin2)
	admin2 := &models.User{Username: "ADMIN2", FullName: "ผู้ดูแลคนที่ 2", Role: models.RoleAdmin, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(admin2)
	createActiveSession(t, userRepo, admin2, "sess_admin2")

	// admin1 ระงับ admin2 -> สำเร็จ เพราะยังมี admin1 เหลืออยู่
	reqSuspend2 := httptest.NewRequest("POST", fmt.Sprintf("/admin/users/%d/suspend", admin2.ID), strings.NewReader(form.Encode()))
	reqSuspend2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSuspend2.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: csrfToken})
	reqSuspend2.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_admin1"})
	recSuspend2 := httptest.NewRecorder()
	app.ServeHTTP(recSuspend2, reqSuspend2)

	if !strings.Contains(recSuspend2.Header().Get("Location"), "success=") {
		t.Errorf("ระงับ admin2 ต้องสำเร็จเมื่อมี admin อื่นเหลืออยู่")
	}

	// ตอนนี้มี active admin เหลือเพียง admin1 คนเดียว
	// หาก admin2 (หรือใครก็ตาม) พยายามระงับ admin1 คนสุดท้าย -> ต้องถูกปฏิเสธ (ระบบห้ามขาด Admin)
	remAdmins, err := userRepo.CountActiveAdminsExcluding(admin1.ID)
	if err != nil || remAdmins != 0 {
		t.Fatalf("คาดหวัง remaining active admins = 0 แต่ได้ %d", remAdmins)
	}
}

// 13. POST ที่ไม่มี CSRF Token หรือใช้ Token ผิดต้องได้รับ HTTP 403
func TestPhase3_13_UniversalCSRFValidation(t *testing.T) {
	app, userRepo, _, _, _, cleanup := setupPhase3App(t, "phase3_t13.db")
	defer cleanup()

	tenant := &models.User{Username: "A101", FullName: "ผู้เช่า A", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(tenant)
	createActiveSession(t, userRepo, tenant, "sess_a")

	// 13.1 POST โดยไม่มี CSRF Token
	formNoCSRF := url.Values{}
	formNoCSRF.Set("category", "ไฟฟ้า")
	formNoCSRF.Set("title", "หลอดไฟเสีย")
	formNoCSRF.Set("description", "เปิดไม่ติด")
	formNoCSRF.Set("convenient_time", "เช้า")

	reqNoCSRF := httptest.NewRequest("POST", "/user/requests/new", strings.NewReader(formNoCSRF.Encode()))
	reqNoCSRF.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqNoCSRF.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})
	recNoCSRF := httptest.NewRecorder()
	app.ServeHTTP(recNoCSRF, reqNoCSRF)

	if recNoCSRF.Code != http.StatusForbidden {
		t.Errorf("คาดหวัง 403 Forbidden เมื่อไม่มี CSRF Token แต่ได้ %d", recNoCSRF.Code)
	}

	// 13.2 POST โดยมี CSRF Token ผิด
	formBadCSRF := url.Values{}
	formBadCSRF.Set("csrf_token", "invalid_forged_csrf_token")
	formBadCSRF.Set("category", "ไฟฟ้า")
	formBadCSRF.Set("title", "หลอดไฟเสีย")
	formBadCSRF.Set("description", "เปิดไม่ติด")
	formBadCSRF.Set("convenient_time", "เช้า")

	reqBadCSRF := httptest.NewRequest("POST", "/user/requests/new", strings.NewReader(formBadCSRF.Encode()))
	reqBadCSRF.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqBadCSRF.AddCookie(&http.Cookie{Name: middleware.CSRFCookieName, Value: "invalid_forged_csrf_token"})
	reqBadCSRF.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess_a"})
	recBadCSRF := httptest.NewRecorder()
	app.ServeHTTP(recBadCSRF, reqBadCSRF)

	if recBadCSRF.Code != http.StatusForbidden {
		t.Errorf("คาดหวัง 403 Forbidden เมื่อ CSRF Token ผิด แต่ได้ %d", recBadCSRF.Code)
	}
}
