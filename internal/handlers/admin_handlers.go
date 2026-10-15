package handlers

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"dormfix/internal/config"
	"dormfix/internal/middleware"
	"dormfix/internal/models"
	"dormfix/internal/repository"
)

// AdminHandler จัดการคำขอของผู้ดูแลระบบ (Admin Dashboard, Request Management, User Management)
type AdminHandler struct {
	reqRepo     *repository.RequestRepository
	userRepo    *repository.UserRepository
	cfg         *config.Config
	templateDir string
}

// NewAdminHandler สร้าง instance ของ AdminHandler
func NewAdminHandler(reqRepo *repository.RequestRepository, userRepo *repository.UserRepository, cfg *config.Config, templateDir string) *AdminHandler {
	return &AdminHandler{
		reqRepo:     reqRepo,
		userRepo:    userRepo,
		cfg:         cfg,
		templateDir: templateDir,
	}
}

// render เรนเดอร์หน้าเว็บสำหรับ Admin
func (h *AdminHandler) render(w http.ResponseWriter, r *http.Request, pageTmpl string, data map[string]interface{}) {
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

	data["User"] = middleware.GetCurrentUser(r)
	data["CSRFToken"] = middleware.GetCSRFToken(r)

	if _, ok := data["Title"]; !ok {
		data["Title"] = "DormFix - ผู้ดูแลระบบ"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "เกิดข้อผิดพลาดในการแสดงผลหน้าเว็บ: "+err.Error(), http.StatusInternalServerError)
	}
}

func parseIDFromPath(path, prefix string) (int64, error) {
	trimmed := strings.TrimPrefix(path, prefix)
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 || parts[0] == "" {
		return 0, errors.New("ไม่พบ ID")
	}
	return strconv.ParseInt(parts[0], 10, 64)
}

// Dashboard แสดงแดชบอร์ดสรุปสถิติจริงจากฐานข้อมูล
func (h *AdminHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := h.reqRepo.GetRequestStats()
	if err != nil {
		http.Error(w, "ไม่สามารถดึงข้อมูลสถิติได้: "+err.Error(), http.StatusInternalServerError)
		return
	}

	pendingUsers, err := h.userRepo.CountPendingUsers()
	if err != nil {
		http.Error(w, "ไม่สามารถนับจำนวนผู้เช่ารออนุมัติได้: "+err.Error(), http.StatusInternalServerError)
		return
	}
	stats.PendingUsersCount = pendingUsers

	h.render(w, r, "admin/dashboard.html", map[string]interface{}{
		"Title": "แดชบอร์ดภาพรวม",
		"Stats": stats,
	})
}

// ListRequests แสดงรายการใบแจ้งซ่อมทั้งหมด พร้อมระบบกรองและค้นหา
func (h *AdminHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	searchQuery := r.URL.Query().Get("q")

	requests, err := h.reqRepo.ListAllRequests(statusFilter, searchQuery)
	if err != nil {
		http.Error(w, "ไม่สามารถดึงรายการแจ้งซ่อมได้: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, r, "admin/requests.html", map[string]interface{}{
		"Title":        "รายการแจ้งซ่อมทั้งหมด",
		"Requests":     requests,
		"StatusFilter": statusFilter,
		"SearchQuery":  searchQuery,
		"Success":      r.URL.Query().Get("success"),
		"Error":        r.URL.Query().Get("error"),
	})
}

// DetailRequest แสดงรายละเอียดและฟอร์มอัปเดตสถานะ/ผลการซ่อม [ฟอร์มที่ 2]
func (h *AdminHandler) DetailRequest(w http.ResponseWriter, r *http.Request) {
	reqID, err := parseIDFromPath(r.URL.Path, "/admin/requests/")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	req, err := h.reqRepo.GetRequestByID(reqID)
	if err != nil {
		if errors.Is(err, repository.ErrRequestNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tenant, _ := h.userRepo.GetUserByID(req.UserID)

	h.render(w, r, "admin/request_detail.html", map[string]interface{}{
		"Title":   "จัดการคำร้องซ่อม #" + strconv.FormatInt(req.ID, 10),
		"Request": req,
		"Tenant":  tenant,
		"Success": r.URL.Query().Get("success"),
		"Error":   r.URL.Query().Get("error"),
	})
}

// UpdateRequestSubmit อัปเดตสถานะงานซ่อมและบันทึกผลการซ่อม [ฟอร์มที่ 2]
func (h *AdminHandler) UpdateRequestSubmit(w http.ResponseWriter, r *http.Request) {
	reqID, err := parseIDFromPath(r.URL.Path, "/admin/requests/")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	newStatus := strings.TrimSpace(r.FormValue("status"))
	adminNotes := strings.TrimSpace(r.FormValue("admin_notes"))

	// ตรวจสอบความถูกต้องของสถานะ
	validStatuses := map[string]bool{
		models.RequestStatusPending:    true,
		models.RequestStatusInProgress: true,
		models.RequestStatusCompleted:  true,
		models.RequestStatusCancelled:  true,
	}

	if !validStatuses[newStatus] {
		http.Redirect(w, r, fmt.Sprintf("/admin/requests/%d?error=สถานะที่เลือกไม่ถูกต้อง", reqID), http.StatusSeeOther)
		return
	}

	err = h.reqRepo.AdminUpdateRequest(reqID, newStatus, adminNotes)
	if err != nil {
		http.Error(w, "ไม่สามารถอัปเดตคำร้องได้: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/requests/%d?success=อัปเดตสถานะและบันทึกผลการซ่อมเรียบร้อยแล้ว", reqID), http.StatusSeeOther)
}

// ListUsers แสดงรายชื่อผู้เช่าทั้งหมด และผู้เช่าที่รออนุมัติ
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	users, err := h.userRepo.ListUsers(statusFilter)
	if err != nil {
		http.Error(w, "ไม่สามารถดึงรายชื่อผู้ใช้ได้: "+err.Error(), http.StatusInternalServerError)
		return
	}

	pendingCount, _ := h.userRepo.CountPendingUsers()

	h.render(w, r, "admin/users.html", map[string]interface{}{
		"Title":        "จัดการบัญชีผู้เช่า",
		"Users":        users,
		"StatusFilter": statusFilter,
		"PendingCount": pendingCount,
		"Success":      r.URL.Query().Get("success"),
		"Error":        r.URL.Query().Get("error"),
	})
}

// ApproveUser อนุมัติผู้เช่าให้เข้าใช้งานระบบได้
func (h *AdminHandler) ApproveUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parseIDFromPath(r.URL.Path, "/admin/users/")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = h.userRepo.UpdateUserStatus(userID, models.UserStatusActive)
	if err != nil {
		http.Error(w, "ไม่สามารถอนุมัติผู้ใช้ได้: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/users?success=อนุมัติผู้เช่าเรียบร้อยแล้ว", http.StatusSeeOther)
}

// SuspendUser ระงับบัญชีผู้ใช้ และยกเลิก Session ทั้งหมดของผู้ใช้นั้นทันที
func (h *AdminHandler) SuspendUser(w http.ResponseWriter, r *http.Request) {
	currentAdmin := middleware.GetCurrentUser(r)
	userID, err := parseIDFromPath(r.URL.Path, "/admin/users/")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// 1. ป้องกัน Admin ระงับบัญชีตนเอง
	if userID == currentAdmin.ID {
		http.Redirect(w, r, "/admin/users?error=ไม่สามารถระงับบัญชีของตนเองได้", http.StatusSeeOther)
		return
	}

	// 2. ตรวจสอบว่าเป้าหมายเป็น Admin หรือไม่ ถ้าใช่ ต้องตรวจสอบว่ามี Admin active เหลืออยู่หรือไม่
	targetUser, err := h.userRepo.GetUserByID(userID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if targetUser.Role == models.RoleAdmin {
		remainingAdmins, err := h.userRepo.CountActiveAdminsExcluding(userID)
		if err != nil || remainingAdmins <= 0 {
			http.Redirect(w, r, "/admin/users?error=ไม่สามารถระงับบัญชีนี้ได้ เนื่องจากต้องมีบัญชีผู้ดูแลระบบ (Admin) ที่มีสถานะใช้งานอยู่อย่างน้อย 1 บัญชี", http.StatusSeeOther)
			return
		}
	}

	// ระงับบัญชี
	if err := h.userRepo.UpdateUserStatus(userID, models.UserStatusSuspended); err != nil {
		http.Error(w, "ไม่สามารถระงับบัญชีได้: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// ยกเลิก Session ทั้งหมดของผู้ใช้นี้ทันที
	_ = h.userRepo.DeleteSessionsByUserID(userID)

	http.Redirect(w, r, "/admin/users?success=ระงับบัญชีและยกเลิกการเข้าสู่ระบบเรียบร้อยแล้ว", http.StatusSeeOther)
}

// ArchiveUser จัดเก็บบัญชีผู้เช่าเดิม เปลี่ยน Username และยกเลิก Session เพื่อให้ผู้เช่าใหม่ลงทะเบียนห้องเดิมได้
func (h *AdminHandler) ArchiveUser(w http.ResponseWriter, r *http.Request) {
	currentAdmin := middleware.GetCurrentUser(r)
	userID, err := parseIDFromPath(r.URL.Path, "/admin/users/")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if userID == currentAdmin.ID {
		http.Redirect(w, r, "/admin/users?error=ไม่สามารถจัดเก็บบัญชีของตนเองได้", http.StatusSeeOther)
		return
	}

	targetUser, err := h.userRepo.GetUserByID(userID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if targetUser.Role == models.RoleAdmin {
		remainingAdmins, err := h.userRepo.CountActiveAdminsExcluding(userID)
		if err != nil || remainingAdmins <= 0 {
			http.Redirect(w, r, "/admin/users?error=ไม่สามารถจัดเก็บบัญชีนี้ได้ เนื่องจากต้องมีบัญชี Admin ที่ใช้งานอยู่อย่างน้อย 1 บัญชี", http.StatusSeeOther)
			return
		}
	}

	// สร้างชื่อจัดเก็บ เช่น A303_ARCHIVED_5
	archivedUsername := fmt.Sprintf("%s_ARCHIVED_%d", strings.ToUpper(targetUser.Username), targetUser.ID)

	if err := h.userRepo.ArchiveUser(userID, archivedUsername); err != nil {
		http.Error(w, "ไม่สามารถจัดเก็บบัญชีได้: "+err.Error(), http.StatusInternalServerError)
		return
	}

	_ = h.userRepo.DeleteSessionsByUserID(userID)

	http.Redirect(w, r, "/admin/users?success=จัดเก็บบัญชีเรียบร้อยแล้ว เลขห้องว่างสำหรับผู้เช่าคนใหม่", http.StatusSeeOther)
}
