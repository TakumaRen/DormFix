package repository

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"dormfix/internal/models"
)

var (
	ErrUserNotFound      = errors.New("ไม่พบผู้ใช้งานนี้ในระบบ")
	ErrUsernameDuplicate = errors.New("เลขห้องนี้มีอยู่ในระบบแล้ว หากท่านเป็นผู้เช่าใหม่ กรุณาติดต่อสำนักงานหอพัก")
	ErrSessionNotFound   = errors.New("ไม่พบเซสชันหรือเซสชันหมดอายุแล้ว")
)

// UserRepository จัดการการเข้าถึงข้อมูลผู้ใช้และเซสชันในฐานข้อมูล
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository สร้าง instance ของ UserRepository
func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// CheckUsernameExists ตรวจสอบว่ามี username นี้ในระบบแล้วหรือไม่ (ไม่สนใจตัวพิมพ์เล็ก-ใหญ่)
func (r *UserRepository) CheckUsernameExists(username string) (bool, error) {
	norm := strings.ToUpper(strings.TrimSpace(username))
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM users WHERE UPPER(username) = UPPER(?)", norm).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CreateUser สร้างผู้ใช้งานใหม่ โดยแปลง username เป็นตัวพิมพ์ใหญ่ และบังคับ role/status ตามที่กำหนด
func (r *UserRepository) CreateUser(u *models.User) error {
	normUsername := strings.ToUpper(strings.TrimSpace(u.Username))
	fullName := strings.TrimSpace(u.FullName)
	phone := strings.TrimSpace(u.Phone)

	// ตรวจสอบความซ้ำซ้อนเบื้องต้น (Layer 1 Pre-check)
	exists, err := r.CheckUsernameExists(normUsername)
	if err != nil {
		return err
	}
	if exists {
		return ErrUsernameDuplicate
	}

	// บันทึกลงฐานข้อมูล (Layer 2 Unique Index Guard)
	query := `
		INSERT INTO users (username, password_hash, full_name, phone, role, status)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	res, err := r.db.Exec(query, normUsername, u.PasswordHash, fullName, phone, u.Role, u.Status)
	if err != nil {
		// ตรวจสอบข้อผิดพลาด Unique constraint จาก SQLite กรณีมีคำขอซ้อนกัน
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "constraint") {
			return ErrUsernameDuplicate
		}
		return err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	u.ID = id
	u.Username = normUsername
	return nil
}

// GetUserByUsername ค้นหาผู้ใช้จาก username (ไม่สนใจตัวพิมพ์เล็ก-ใหญ่)
func (r *UserRepository) GetUserByUsername(username string) (*models.User, error) {
	norm := strings.ToUpper(strings.TrimSpace(username))
	query := `
		SELECT id, username, password_hash, full_name, phone, role, status, created_at, updated_at
		FROM users
		WHERE UPPER(username) = UPPER(?)
	`
	var u models.User
	err := r.db.QueryRow(query, norm).Scan(
		&u.ID,
		&u.Username,
		&u.PasswordHash,
		&u.FullName,
		&u.Phone,
		&u.Role,
		&u.Status,
		&u.CreatedAt,
		&u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByID ค้นหาผู้ใช้จาก ID
func (r *UserRepository) GetUserByID(id int64) (*models.User, error) {
	query := `
		SELECT id, username, password_hash, full_name, phone, role, status, created_at, updated_at
		FROM users
		WHERE id = ?
	`
	var u models.User
	err := r.db.QueryRow(query, id).Scan(
		&u.ID,
		&u.Username,
		&u.PasswordHash,
		&u.FullName,
		&u.Phone,
		&u.Role,
		&u.Status,
		&u.CreatedAt,
		&u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

// CreateSession บันทึก Session ลงตาราง sessions
func (r *UserRepository) CreateSession(s *models.Session) error {
	query := `
		INSERT INTO sessions (token, user_id, expires_at)
		VALUES (?, ?, ?)
	`
	_, err := r.db.Exec(query, s.Token, s.UserID, s.ExpiresAt)
	return err
}

// GetSessionByToken ดึง Session และตรวจสอบว่ายังไม่หมดอายุ
func (r *UserRepository) GetSessionByToken(token string) (*models.Session, error) {
	query := `
		SELECT token, user_id, expires_at, created_at
		FROM sessions
		WHERE token = ?
	`
	var s models.Session
	err := r.db.QueryRow(query, token).Scan(
		&s.Token,
		&s.UserID,
		&s.ExpiresAt,
		&s.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}

	// ตรวจสอบการหมดอายุด้วย Go time.Now() เพื่อความถูกต้องแม่นยำในทุก Timezone
	if time.Now().After(s.ExpiresAt) {
		// ลบ Session ที่หมดอายุแล้วออกจากฐานข้อมูล
		_ = r.DeleteSession(token)
		return nil, ErrSessionNotFound
	}

	return &s, nil
}

// DeleteSession ลบ Session ตาม Token (ใช้เมื่อ Logout)
func (r *UserRepository) DeleteSession(token string) error {
	_, err := r.db.Exec("DELETE FROM sessions WHERE token = ?", token)
	return err
}

// DeleteSessionsByUserID ลบ Session ทั้งหมดของผู้ใช้ (ใช้เมื่อระงับบัญชีหรือ Archive)
func (r *UserRepository) DeleteSessionsByUserID(userID int64) error {
	_, err := r.db.Exec("DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

// CountActiveAdminsExcluding นับจำนวน Admin ที่มีสถานะ active ที่ไม่ใช่ ID ที่ระบุ
func (r *UserRepository) CountActiveAdminsExcluding(excludedUserID int64) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM users
		WHERE role = 'admin' AND status = 'active' AND id != ?
	`
	var count int
	err := r.db.QueryRow(query, excludedUserID).Scan(&count)
	return count, err
}

// UpdateUserStatus อัปเดตสถานะของผู้ใช้ (pending, active, suspended)
func (r *UserRepository) UpdateUserStatus(userID int64, status string) error {
	query := `
		UPDATE users
		SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, status, userID)
	return err
}

// ArchiveUser เปลี่ยน username เพื่อจัดเก็บเลขห้องเดิม และเปลี่ยนสถานะเป็น archived
func (r *UserRepository) ArchiveUser(userID int64, newUsername string) error {
	query := `
		UPDATE users
		SET username = ?, status = 'archived', updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, newUsername, userID)
	return err
}

// CleanExpiredSessions ลบเซสชันที่หมดอายุแล้ว (สำหรับการบำรุงรักษา)
func (r *UserRepository) CleanExpiredSessions() error {
	_, err := r.db.Exec("DELETE FROM sessions WHERE expires_at <= ?", time.Now())
	return err
}

// ListUsers ดึงรายชื่อผู้ใช้ทั้งหมด โดยสามารถกรองตามสถานะ (pending, active, suspended) ได้
func (r *UserRepository) ListUsers(statusFilter string) ([]*models.User, error) {
	var query string
	var rows *sql.Rows
	var err error

	if statusFilter != "" {
		query = `
			SELECT id, username, password_hash, full_name, phone, role, status, created_at, updated_at
			FROM users
			WHERE status = ?
			ORDER BY id DESC
		`
		rows, err = r.db.Query(query, statusFilter)
	} else {
		query = `
			SELECT id, username, password_hash, full_name, phone, role, status, created_at, updated_at
			FROM users
			ORDER BY id DESC
		`
		rows, err = r.db.Query(query)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		var u models.User
		err := rows.Scan(
			&u.ID,
			&u.Username,
			&u.PasswordHash,
			&u.FullName,
			&u.Phone,
			&u.Role,
			&u.Status,
			&u.CreatedAt,
			&u.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		users = append(users, &u)
	}

	return users, rows.Err()
}

// CountPendingUsers นับจำนวนผู้เช่าที่รออนุมัติบัญชี
func (r *UserRepository) CountPendingUsers() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM users WHERE status = 'pending'").Scan(&count)
	return count, err
}
