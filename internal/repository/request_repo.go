package repository

import (
	"database/sql"
	"errors"
	"strings"

	"dormfix/internal/models"
)

var (
	ErrRequestNotFound  = errors.New("ไม่พบใบแจ้งซ่อมนี้ในระบบ")
	ErrUnauthorized     = errors.New("คุณไม่มีสิทธิ์เข้าถึงหรือจัดการใบแจ้งซ่อมนี้")
	ErrStatusNotPending = errors.New("สามารถแก้ไขหรือยกเลิกได้เฉพาะคำร้องที่อยู่ในสถานะรอดำเนินการเท่านั้น")
)

// RequestRepository จัดการการเข้าถึงข้อมูลใบแจ้งซ่อมในฐานข้อมูล
type RequestRepository struct {
	db *sql.DB
}

// NewRequestRepository สร้าง instance ของ RequestRepository
func NewRequestRepository(db *sql.DB) *RequestRepository {
	return &RequestRepository{db: db}
}

// CreateRequest บันทึกใบแจ้งซ่อมใหม่ลงฐานข้อมูล
func (r *RequestRepository) CreateRequest(req *models.MaintenanceRequest) error {
	category := strings.TrimSpace(req.Category)
	title := strings.TrimSpace(req.Title)
	desc := strings.TrimSpace(req.Description)
	convTime := strings.TrimSpace(req.ConvenientTime)

	if req.Status == "" {
		req.Status = models.RequestStatusPending
	}

	query := `
		INSERT INTO maintenance_requests (user_id, room_no, category, title, description, convenient_time, status, admin_notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	res, err := r.db.Exec(query, req.UserID, req.RoomNo, category, title, desc, convTime, req.Status, req.AdminNotes)
	if err != nil {
		return err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	req.ID = id
	return nil
}

// GetRequestByID ดึงข้อมูลใบแจ้งซ่อมตาม ID
func (r *RequestRepository) GetRequestByID(id int64) (*models.MaintenanceRequest, error) {
	query := `
		SELECT id, user_id, room_no, category, title, description, convenient_time, status, admin_notes, created_at, updated_at
		FROM maintenance_requests
		WHERE id = ?
	`
	var req models.MaintenanceRequest
	err := r.db.QueryRow(query, id).Scan(
		&req.ID,
		&req.UserID,
		&req.RoomNo,
		&req.Category,
		&req.Title,
		&req.Description,
		&req.ConvenientTime,
		&req.Status,
		&req.AdminNotes,
		&req.CreatedAt,
		&req.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRequestNotFound
		}
		return nil, err
	}
	return &req, nil
}

// ListRequestsByUserID ดึงรายการใบแจ้งซ่อมเฉพาะของผู้ใช้ที่ระบุ (Data Ownership)
func (r *RequestRepository) ListRequestsByUserID(userID int64) ([]*models.MaintenanceRequest, error) {
	query := `
		SELECT id, user_id, room_no, category, title, description, convenient_time, status, admin_notes, created_at, updated_at
		FROM maintenance_requests
		WHERE user_id = ?
		ORDER BY id DESC
	`
	rows, err := r.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*models.MaintenanceRequest
	for rows.Next() {
		var req models.MaintenanceRequest
		err := rows.Scan(
			&req.ID,
			&req.UserID,
			&req.RoomNo,
			&req.Category,
			&req.Title,
			&req.Description,
			&req.ConvenientTime,
			&req.Status,
			&req.AdminNotes,
			&req.CreatedAt,
			&req.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		list = append(list, &req)
	}
	return list, rows.Err()
}

// UpdateRequestDetails ผู้เช่าแก้ไขรายละเอียดคำร้อง (ต้องเป็นเจ้าของและสถานะเป็น pending เท่านั้น)
func (r *RequestRepository) UpdateRequestDetails(reqID, userID int64, category, title, description, convenientTime string) error {
	current, err := r.GetRequestByID(reqID)
	if err != nil {
		return err
	}
	if current.UserID != userID {
		return ErrUnauthorized
	}
	if current.Status != models.RequestStatusPending {
		return ErrStatusNotPending
	}

	query := `
		UPDATE maintenance_requests
		SET category = ?, title = ?, description = ?, convenient_time = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_id = ? AND status = 'pending'
	`
	res, err := r.db.Exec(query, strings.TrimSpace(category), strings.TrimSpace(title), strings.TrimSpace(description), strings.TrimSpace(convenientTime), reqID, userID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrStatusNotPending
	}
	return nil
}

// CancelRequest ผู้เช่ายกเลิกคำร้อง (Soft Cancel: เปลี่ยนสถานะเป็น cancelled เฉพาะเมื่อเป็นเจ้าของและสถานะเป็น pending)
func (r *RequestRepository) CancelRequest(reqID, userID int64) error {
	current, err := r.GetRequestByID(reqID)
	if err != nil {
		return err
	}
	if current.UserID != userID {
		return ErrUnauthorized
	}
	if current.Status != models.RequestStatusPending {
		return ErrStatusNotPending
	}

	query := `
		UPDATE maintenance_requests
		SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND user_id = ? AND status = 'pending'
	`
	res, err := r.db.Exec(query, models.RequestStatusCancelled, reqID, userID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrStatusNotPending
	}
	return nil
}

// ListAllRequests ดึงรายการคำร้องทั้งหมดสำหรับ Admin พร้อมระบบคัดกรองตามสถานะและค้นหาเลขห้อง/หัวข้อ
func (r *RequestRepository) ListAllRequests(statusFilter, searchQuery string) ([]*models.MaintenanceRequest, error) {
	statusFilter = strings.TrimSpace(statusFilter)
	searchQuery = strings.TrimSpace(searchQuery)

	query := `
		SELECT id, user_id, room_no, category, title, description, convenient_time, status, admin_notes, created_at, updated_at
		FROM maintenance_requests
		WHERE (? = '' OR status = ?)
		  AND (? = '' OR room_no LIKE '%' || ? || '%' OR title LIKE '%' || ? || '%')
		ORDER BY id DESC
	`
	rows, err := r.db.Query(query, statusFilter, statusFilter, searchQuery, searchQuery, searchQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*models.MaintenanceRequest
	for rows.Next() {
		var req models.MaintenanceRequest
		err := rows.Scan(
			&req.ID,
			&req.UserID,
			&req.RoomNo,
			&req.Category,
			&req.Title,
			&req.Description,
			&req.ConvenientTime,
			&req.Status,
			&req.AdminNotes,
			&req.CreatedAt,
			&req.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		list = append(list, &req)
	}
	return list, rows.Err()
}

// AdminUpdateRequest ผู้ดูแลระบบอัปเดตสถานะและบันทึกหมายเหตุ/ผลการซ่อม
func (r *RequestRepository) AdminUpdateRequest(reqID int64, newStatus, adminNotes string) error {
	newStatus = strings.TrimSpace(newStatus)
	adminNotes = strings.TrimSpace(adminNotes)

	query := `
		UPDATE maintenance_requests
		SET status = ?, admin_notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	res, err := r.db.Exec(query, newStatus, adminNotes, reqID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrRequestNotFound
	}
	return nil
}

// GetRequestStats คำนวณสรุปตัวเลขสถิติงานซ่อมตามสถานะจริงจากฐานข้อมูล
func (r *RequestRepository) GetRequestStats() (*models.DashboardStats, error) {
	query := `
		SELECT 
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'cancelled' THEN 1 ELSE 0 END), 0)
		FROM maintenance_requests
	`
	var stats models.DashboardStats
	err := r.db.QueryRow(query).Scan(
		&stats.TotalRequests,
		&stats.PendingRequests,
		&stats.InProgressRequests,
		&stats.CompletedRequests,
		&stats.CancelledRequests,
	)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}
