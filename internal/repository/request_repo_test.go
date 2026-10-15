package repository

import (
	"os"
	"path/filepath"
	"testing"

	"dormfix/internal/config"
	"dormfix/internal/database"
	"dormfix/internal/models"
)

func setupTestRequestRepo(t *testing.T, dbName string) (*RequestRepository, *UserRepository, func()) {
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
		AdminPassword: "adminPassword123",
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB ล้มเหลว: %v", err)
	}

	reqRepo := NewRequestRepository(db)
	userRepo := NewUserRepository(db)

	cleanup := func() {
		db.Close()
		os.Remove(testDBPath)
	}

	return reqRepo, userRepo, cleanup
}

func TestRequestRepo_CreateAndListByUserID(t *testing.T) {
	reqRepo, userRepo, cleanup := setupTestRequestRepo(t, "test_req_create.db")
	defer cleanup()

	// สร้าง User A และ User B
	userA := &models.User{Username: "A101", FullName: "ผู้เช่า A", Phone: "081", Role: models.RoleUser, Status: models.UserStatusActive}
	userB := &models.User{Username: "B202", FullName: "ผู้เช่า B", Phone: "082", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(userA)
	_ = userRepo.CreateUser(userB)

	// สร้างคำร้องของ User A
	reqA1 := &models.MaintenanceRequest{
		UserID:         userA.ID,
		RoomNo:         "A101",
		Category:       "ไฟฟ้า",
		Title:          "หลอดไฟห้องน้ำขาด",
		Description:    "เปิดสวิตช์แล้วไฟไม่ติด",
		ConvenientTime: "หลัง 16:00 น.",
	}
	if err := reqRepo.CreateRequest(reqA1); err != nil {
		t.Fatalf("CreateRequest reqA1 ล้มเหลว: %v", err)
	}

	// สร้างคำร้องของ User B
	reqB1 := &models.MaintenanceRequest{
		UserID:         userB.ID,
		RoomNo:         "B202",
		Category:       "ประปา",
		Title:          "ท่อน้ำรั่ว",
		Description:    "น้ำหยดใต้ซิงก์",
		ConvenientTime: "วันเสาร์ทั้งวัน",
	}
	if err := reqRepo.CreateRequest(reqB1); err != nil {
		t.Fatalf("CreateRequest reqB1 ล้มเหลว: %v", err)
	}

	// User A ดึงรายการคำร้อง ➔ ต้องได้เฉพาะของ A (1 รายการ)
	listA, err := reqRepo.ListRequestsByUserID(userA.ID)
	if err != nil {
		t.Fatalf("ListRequestsByUserID(userA) ล้มเหลว: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != reqA1.ID {
		t.Errorf("User A ต้องมองเห็นเฉพาะคำร้องของตนเอง แต่ได้ %d รายการ", len(listA))
	}

	// User B ดึงรายการคำร้อง ➔ ต้องได้เฉพาะของ B (1 รายการ)
	listB, err := reqRepo.ListRequestsByUserID(userB.ID)
	if err != nil {
		t.Fatalf("ListRequestsByUserID(userB) ล้มเหลว: %v", err)
	}
	if len(listB) != 1 || listB[0].ID != reqB1.ID {
		t.Errorf("User B ต้องมองเห็นเฉพาะคำร้องของตนเอง แต่ได้ %d รายการ", len(listB))
	}
}

func TestRequestRepo_UpdateAndCancelRules(t *testing.T) {
	reqRepo, userRepo, cleanup := setupTestRequestRepo(t, "test_req_rules.db")
	defer cleanup()

	userA := &models.User{Username: "A101", FullName: "ผู้เช่า A", Phone: "081", Role: models.RoleUser, Status: models.UserStatusActive}
	userB := &models.User{Username: "B202", FullName: "ผู้เช่า B", Phone: "082", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(userA)
	_ = userRepo.CreateUser(userB)

	req := &models.MaintenanceRequest{
		UserID:         userA.ID,
		RoomNo:         "A101",
		Category:       "แอร์",
		Title:          "แอร์ไม่เย็น",
		Description:    "มีแต่ลมร้อน",
		ConvenientTime: "ช่วงบ่าย",
	}
	_ = reqRepo.CreateRequest(req)

	// 1. User B พยายามแก้ไขคำร้องของ User A ➔ ต้องถูกปฏิเสธด้วย ErrUnauthorized
	err := reqRepo.UpdateRequestDetails(req.ID, userB.ID, "แอร์", "หัวข้อใหม่", "รายละเอียดใหม่", "ช่วงเย็น")
	if err != ErrUnauthorized {
		t.Errorf("คาดหวัง ErrUnauthorized เมื่อ User B แก้คำร้องของ A แต่ได้: %v", err)
	}

	// 2. User B พยายามยกเลิกคำร้องของ User A ➔ ต้องถูกปฏิเสธด้วย ErrUnauthorized
	err = reqRepo.CancelRequest(req.ID, userB.ID)
	if err != ErrUnauthorized {
		t.Errorf("คาดหวัง ErrUnauthorized เมื่อ User B กดยกเลิกคำร้องของ A แต่ได้: %v", err)
	}

	// 3. User A แก้ไขคำร้องของตนเองขณะเป็น pending ➔ สำเร็จ
	err = reqRepo.UpdateRequestDetails(req.ID, userA.ID, "แอร์", "แอร์มีเสียงดังและไม่เย็น", "ลมเบามาก", "ช่วงเช้า")
	if err != nil {
		t.Fatalf("User A แก้ไขคำร้องตนเองขณะ pending ล้มเหลว: %v", err)
	}

	updatedReq, _ := reqRepo.GetRequestByID(req.ID)
	if updatedReq.Title != "แอร์มีเสียงดังและไม่เย็น" {
		t.Errorf("ข้อมูลที่แก้ไขไม่ตรงกับที่บันทึก")
	}

	// 4. จำลอง Admin เปลี่ยนสถานะเป็น 'in_progress'
	_ = reqRepo.AdminUpdateRequest(req.ID, models.RequestStatusInProgress, "ช่างกำลังเข้าตรวจสอบ")

	// 5. User A พยายามแก้ไขคำร้องขณะเป็น in_progress ➔ ต้องถูกปฏิเสธ
	err = reqRepo.UpdateRequestDetails(req.ID, userA.ID, "แอร์", "แก้ไขอีกครั้ง", "รายละเอียด", "ช่วงเย็น")
	if err != ErrStatusNotPending {
		t.Errorf("คาดหวัง ErrStatusNotPending เมื่อแก้คำร้องที่ in_progress แต่ได้: %v", err)
	}

	// 6. User A พยายามยกเลิกคำร้องขณะเป็น in_progress ➔ ต้องถูกปฏิเสธ
	err = reqRepo.CancelRequest(req.ID, userA.ID)
	if err != ErrStatusNotPending {
		t.Errorf("คาดหวัง ErrStatusNotPending เมื่อกดยกเลิกคำร้องที่ in_progress แต่ได้: %v", err)
	}

	// 7. สร้างคำร้อง pending อีกอัน แล้วทดสอบ Soft Cancel
	req2 := &models.MaintenanceRequest{
		UserID:         userA.ID,
		RoomNo:         "A101",
		Category:       "เฟอร์นิเจอร์",
		Title:          "เก้าอี้ขาหัก",
		Description:    "หักข้างซ้าย",
		ConvenientTime: "วันศุกร์",
	}
	_ = reqRepo.CreateRequest(req2)

	err = reqRepo.CancelRequest(req2.ID, userA.ID)
	if err != nil {
		t.Fatalf("CancelRequest ล้มเหลว: %v", err)
	}

	// ตรวจสอบว่าคำร้องยังอยู่ในฐานข้อมูล แต่สถานะเปลี่ยนเป็น cancelled
	cancelledReq, err := reqRepo.GetRequestByID(req2.ID)
	if err != nil {
		t.Fatalf("ไม่พบคำร้องที่ถูกยกเลิกในฐานข้อมูล (ข้อมูลต้องไม่สูญหาย): %v", err)
	}
	if cancelledReq.Status != models.RequestStatusCancelled {
		t.Errorf("คาดหวังสถานะเป็น cancelled แต่ได้ %s", cancelledReq.Status)
	}
}

func TestRequestRepo_AdminOperationsAndStats(t *testing.T) {
	reqRepo, userRepo, cleanup := setupTestRequestRepo(t, "test_req_admin.db")
	defer cleanup()

	user := &models.User{Username: "C303", FullName: "ผู้เช่า C", Phone: "083", Role: models.RoleUser, Status: models.UserStatusActive}
	_ = userRepo.CreateUser(user)

	// สร้าง 3 คำร้อง
	r1 := &models.MaintenanceRequest{UserID: user.ID, RoomNo: "C303", Category: "ประปา", Title: "ก๊อกรั่ว", Description: "น้ำหยด", ConvenientTime: "เช้า"}
	r2 := &models.MaintenanceRequest{UserID: user.ID, RoomNo: "C303", Category: "ไฟฟ้า", Title: "ปลั๊กช็อต", Description: "ไฟสปาร์ก", ConvenientTime: "บ่าย"}
	r3 := &models.MaintenanceRequest{UserID: user.ID, RoomNo: "C303", Category: "แอร์", Title: "น้ำแอร์หยด", Description: "หยดลงเตียง", ConvenientTime: "เย็น"}
	_ = reqRepo.CreateRequest(r1)
	_ = reqRepo.CreateRequest(r2)
	_ = reqRepo.CreateRequest(r3)

	// Admin อัปเดต r1 เป็น in_progress, r2 เป็น completed
	_ = reqRepo.AdminUpdateRequest(r1.ID, models.RequestStatusInProgress, "ช่างรับเรื่องแล้ว")
	_ = reqRepo.AdminUpdateRequest(r2.ID, models.RequestStatusCompleted, "เปลี่ยนปลั๊กใหม่เรียบร้อยแล้ว")

	// ดึงสถิติ
	stats, err := reqRepo.GetRequestStats()
	if err != nil {
		t.Fatalf("GetRequestStats ล้มเหลว: %v", err)
	}

	if stats.TotalRequests != 3 {
		t.Errorf("คาดหวัง Total = 3 แต่ได้ %d", stats.TotalRequests)
	}
	if stats.PendingRequests != 1 {
		t.Errorf("คาดหวัง Pending = 1 แต่ได้ %d", stats.PendingRequests)
	}
	if stats.InProgressRequests != 1 {
		t.Errorf("คาดหวัง InProgress = 1 แต่ได้ %d", stats.InProgressRequests)
	}
	if stats.CompletedRequests != 1 {
		t.Errorf("คาดหวัง Completed = 1 แต่ได้ %d", stats.CompletedRequests)
	}
}
