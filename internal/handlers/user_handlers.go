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

// UserHandler จัดการคำขอเกี่ยวกับใบแจ้งซ่อมของผู้เช่า (CRUD และ Data Ownership)
type UserHandler struct {
	reqRepo     *repository.RequestRepository
	cfg         *config.Config
	templateDir string
}

// NewUserHandler สร้าง instance ของ UserHandler
func NewUserHandler(reqRepo *repository.RequestRepository, cfg *config.Config, templateDir string) *UserHandler {
	return &UserHandler{
		reqRepo:     reqRepo,
		cfg:         cfg,
		templateDir: templateDir,
	}
}

// render เรนเดอร์หน้าเว็บสำหรับผู้เช่า
func (h *UserHandler) render(w http.ResponseWriter, r *http.Request, pageTmpl string, data map[string]interface{}) {
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
		data["Title"] = "DormFix - ผู้เช่า"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "เกิดข้อผิดพลาดในการแสดงผลหน้าเว็บ: "+err.Error(), http.StatusInternalServerError)
	}
}

// parseID แยกค่า ID จาก URL Path เช่น /user/requests/5 หรือ /user/requests/5/edit
func parseRequestID(path string, prefix string) (int64, error) {
	trimmed := strings.TrimPrefix(path, prefix)
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 || parts[0] == "" {
		return 0, errors.New("ไม่พบ ID")
	}
	return strconv.ParseInt(parts[0], 10, 64)
}

// ListRequests แสดงรายการใบแจ้งซ่อมเฉพาะของตนเอง (Data Ownership)
func (h *UserHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)
	requests, err := h.reqRepo.ListRequestsByUserID(u.ID)
	if err != nil {
		http.Error(w, "ไม่สามารถดึงข้อมูลใบแจ้งซ่อมได้: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.render(w, r, "user/list.html", map[string]interface{}{
		"Title":    "ประวัติการแจ้งซ่อมของฉัน",
		"Requests": requests,
		"Success":  r.URL.Query().Get("success"),
		"Error":    r.URL.Query().Get("error"),
	})
}

// CreateRequestPage แสดงแบบฟอร์มแจ้งซ่อมใหม่ [ฟอร์มที่ 1]
func (h *UserHandler) CreateRequestPage(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)
	h.render(w, r, "user/create.html", map[string]interface{}{
		"Title":  "แจ้งซ่อมใหม่",
		"RoomNo": u.Username,
	})
}

// CreateRequestSubmit บันทึกใบแจ้งซ่อมใหม่ลงฐานข้อมูล [ฟอร์มที่ 1]
func (h *UserHandler) CreateRequestSubmit(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)

	category := strings.TrimSpace(r.FormValue("category"))
	title := strings.TrimSpace(r.FormValue("title"))
	description := strings.TrimSpace(r.FormValue("description"))
	convenientTime := strings.TrimSpace(r.FormValue("convenient_time"))

	formData := map[string]interface{}{
		"Title":          "แจ้งซ่อมใหม่",
		"RoomNo":         u.Username,
		"Category":       category,
		"TitleInput":     title,
		"Description":    description,
		"ConvenientTime": convenientTime,
	}

	if category == "" || title == "" || description == "" || convenientTime == "" {
		w.WriteHeader(http.StatusBadRequest)
		formData["Error"] = "กรุณากรอกข้อมูลให้ครบถ้วนทุกช่อง"
		h.render(w, r, "user/create.html", formData)
		return
	}

	// ผูก UserID และ RoomNo จาก Session ที่ยืนยันแล้วเท่านั้น (ห้ามเชื่อค่าจาก Client)
	newReq := &models.MaintenanceRequest{
		UserID:         u.ID,
		RoomNo:         u.Username,
		Category:       category,
		Title:          title,
		Description:    description,
		ConvenientTime: convenientTime,
		Status:         models.RequestStatusPending,
	}

	if err := h.reqRepo.CreateRequest(newReq); err != nil {
		http.Error(w, "ไม่สามารถบันทึกใบแจ้งซ่อมได้: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/user/requests?success=ส่งคำขอแจ้งซ่อมเรียบร้อยแล้ว", http.StatusSeeOther)
}

// DetailRequest แสดงรายละเอียดใบแจ้งซ่อม พร้อมตรวจสอบสิทธิ์ความเป็นเจ้าของ
func (h *UserHandler) DetailRequest(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)
	reqID, err := parseRequestID(r.URL.Path, "/user/requests/")
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

	// ป้องกันดูข้ามห้อง: ตรวจสอบความเป็นเจ้าของอย่างเข้มงวด
	if req.UserID != u.ID {
		http.Error(w, "คุณไม่มีสิทธิ์เข้าถึงใบแจ้งซ่อมของห้องอื่น (403 Forbidden)", http.StatusForbidden)
		return
	}

	h.render(w, r, "user/detail.html", map[string]interface{}{
		"Title":   "รายละเอียดใบแจ้งซ่อม #" + strconv.FormatInt(req.ID, 10),
		"Request": req,
		"Success": r.URL.Query().Get("success"),
		"Error":   r.URL.Query().Get("error"),
	})
}

// EditRequestPage แสดงหน้าฟอร์มแก้ไขคำร้อง (เฉพาะสถานะ pending)
func (h *UserHandler) EditRequestPage(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)
	reqID, err := parseRequestID(r.URL.Path, "/user/requests/")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	req, err := h.reqRepo.GetRequestByID(reqID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if req.UserID != u.ID {
		http.Error(w, "คุณไม่มีสิทธิ์แก้ไขใบแจ้งซ่อมนี้ (403 Forbidden)", http.StatusForbidden)
		return
	}

	if req.Status != models.RequestStatusPending {
		http.Redirect(w, r, fmt.Sprintf("/user/requests/%d?error=ไม่สามารถแก้ไขได้เนื่องจากคำร้องไม่อยู่ในสถานะรอดำเนินการ", reqID), http.StatusSeeOther)
		return
	}

	h.render(w, r, "user/edit.html", map[string]interface{}{
		"Title":   "แก้ไขใบแจ้งซ่อม #" + strconv.FormatInt(req.ID, 10),
		"Request": req,
	})
}

// EditRequestSubmit บันทึกการแก้ไขคำร้อง (เฉพาะสถานะ pending)
func (h *UserHandler) EditRequestSubmit(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)
	reqID, err := parseRequestID(r.URL.Path, "/user/requests/")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	category := strings.TrimSpace(r.FormValue("category"))
	title := strings.TrimSpace(r.FormValue("title"))
	description := strings.TrimSpace(r.FormValue("description"))
	convenientTime := strings.TrimSpace(r.FormValue("convenient_time"))

	if category == "" || title == "" || description == "" || convenientTime == "" {
		w.WriteHeader(http.StatusBadRequest)
		req, _ := h.reqRepo.GetRequestByID(reqID)
		h.render(w, r, "user/edit.html", map[string]interface{}{
			"Title":   "แก้ไขใบแจ้งซ่อม",
			"Request": req,
			"Error":   "กรุณากรอกข้อมูลให้ครบถ้วนทุกช่อง",
		})
		return
	}

	err = h.reqRepo.UpdateRequestDetails(reqID, u.ID, category, title, description, convenientTime)
	if err != nil {
		if errors.Is(err, repository.ErrUnauthorized) {
			http.Error(w, "คุณไม่มีสิทธิ์แก้ไขใบแจ้งซ่อมนี้ (403 Forbidden)", http.StatusForbidden)
			return
		}
		if errors.Is(err, repository.ErrStatusNotPending) {
			w.WriteHeader(http.StatusBadRequest)
			req, _ := h.reqRepo.GetRequestByID(reqID)
			h.render(w, r, "user/edit.html", map[string]interface{}{
				"Title":   "แก้ไขใบแจ้งซ่อม",
				"Request": req,
				"Error":   "สามารถแก้ไขได้เฉพาะคำร้องที่อยู่ในสถานะรอดำเนินการเท่านั้น",
			})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/user/requests/%d?success=แก้ไขข้อมูลเรียบร้อยแล้ว", reqID), http.StatusSeeOther)
}

// CancelRequest ผู้เช่ายกเลิกคำร้อง (Soft Cancel: เปลี่ยนเป็น cancelled เฉพาะสถานะ pending)
func (h *UserHandler) CancelRequest(w http.ResponseWriter, r *http.Request) {
	u := middleware.GetCurrentUser(r)
	reqID, err := parseRequestID(r.URL.Path, "/user/requests/")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = h.reqRepo.CancelRequest(reqID, u.ID)
	if err != nil {
		if errors.Is(err, repository.ErrUnauthorized) {
			http.Error(w, "คุณไม่มีสิทธิ์ยกเลิกใบแจ้งซ่อมนี้ (403 Forbidden)", http.StatusForbidden)
			return
		}
		if errors.Is(err, repository.ErrStatusNotPending) {
			http.Redirect(w, r, fmt.Sprintf("/user/requests/%d?error=ไม่สามารถยกเลิกได้เนื่องจากคำร้องไม่อยู่ในสถานะรอดำเนินการ", reqID), http.StatusSeeOther)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/user/requests?success=ยกเลิกใบแจ้งซ่อมเรียบร้อยแล้ว", http.StatusSeeOther)
}
