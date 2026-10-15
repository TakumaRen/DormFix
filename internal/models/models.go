package models

import "time"

// บทบาทของผู้ใช้งานในระบบ
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// สถานะของผู้ใช้งานในระบบ
const (
	UserStatusPending   = "pending"   // รอ Admin อนุมัติ
	UserStatusActive    = "active"    // อนุมัติแล้ว ใช้งานได้ปกติ
	UserStatusSuspended = "suspended" // ถูกระงับการใช้งาน
	UserStatusArchived  = "archived"  // ย้ายออก/จัดเก็บ
)

// สถานะของใบแจ้งซ่อม
const (
	RequestStatusPending    = "pending"     // รอดำเนินการ (ผู้เช่ายกเลิกได้)
	RequestStatusInProgress = "in_progress" // กำลังดำเนินการซ่อม
	RequestStatusCompleted  = "completed"   // ซ่อมเสร็จสิ้น
	RequestStatusCancelled  = "cancelled"   // ยกเลิกแล้ว (Soft Cancel)
)

// User โครงสร้างข้อมูลผู้ใช้งาน (ทั้งผู้เช่าและแอดมิน)
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"` // เลขห้องตัวพิมพ์ใหญ่ เช่น A303
	PasswordHash string    `json:"-"`        // ไม่ส่งออก JSON
	FullName     string    `json:"full_name"`
	Phone        string    `json:"phone"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// MaintenanceRequest โครงสร้างข้อมูลใบแจ้งซ่อม
type MaintenanceRequest struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"` // เจ้าของใบแจ้งซ่อม (Immutable)
	RoomNo         string    `json:"room_no"`
	Category       string    `json:"category"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	ConvenientTime string    `json:"convenient_time"`
	Status         string    `json:"status"`
	AdminNotes     string    `json:"admin_notes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Session โครงสร้างข้อมูลเซสชัน
type Session struct {
	Token     string    `json:"token"`
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// DashboardStats สรุปตัวเลขสถิติงานซ่อมและผู้เช่าสำหรับแอดมิน
type DashboardStats struct {
	TotalRequests      int `json:"total_requests"`
	PendingRequests    int `json:"pending_requests"`
	InProgressRequests int `json:"in_progress_requests"`
	CompletedRequests  int `json:"completed_requests"`
	CancelledRequests  int `json:"cancelled_requests"`
	PendingUsersCount  int `json:"pending_users_count"`
}

