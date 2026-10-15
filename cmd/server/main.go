package main

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"dormfix/internal/config"
	"dormfix/internal/database"
	"dormfix/internal/handlers"
	"dormfix/internal/middleware"
	"dormfix/internal/repository"
)

func main() {
	// 1. โหลดค่าคอนฟิก
	cfg := config.LoadConfig()

	// 2. เริ่มต้นฐานข้อมูล SQLite, Auto-migration, และ Seed Admin
	db, err := database.InitDB(cfg)
	if err != nil {
		log.Fatalf("เกิดข้อผิดพลาดในการเริ่มต้นฐานข้อมูล: %v", err)
	}
	defer db.Close()

	// 3. เริ่มต้น Repository และ Handler
	userRepo := repository.NewUserRepository(db)
	reqRepo := repository.NewRequestRepository(db)
	templateDir := filepath.Join("web", "templates")
	authHandler := handlers.NewAuthHandler(userRepo, cfg, templateDir)
	userHandler := handlers.NewUserHandler(reqRepo, cfg, templateDir)
	adminHandler := handlers.NewAdminHandler(reqRepo, userRepo, cfg, templateDir)

	mux := http.NewServeMux()

	// Static assets (CSS, JS, Images)
	fs := http.FileServer(http.Dir("web/static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprintf(w, `{"status":"ok","database":"connected","app_env":"%s"}`+"\n", cfg.AppEnv)
	})

	// Public & Authentication Routes
	mux.HandleFunc("/", authHandler.Root)
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
			http.Error(w, "Method Not Allowed (ต้องส่งคำขอด้วย POST)", http.StatusMethodNotAllowed)
			return
		}
		authHandler.Logout(w, r)
	})

	// User Routes (RequireActiveUser)
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

	// Admin Routes (RequireAdmin)
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

	// 4. ติดตั้ง Global Middleware Chain
	// SessionMiddleware จะโหลดข้อมูล User เข้า Context จากนั้น UniversalCSRFMiddleware จะตรวจสอบ Token
	handler := middleware.SessionMiddleware(userRepo)(mux)
	handler = middleware.UniversalCSRFMiddleware(cfg)(handler)

	log.Printf("[DormFix] เซิร์ฟเวอร์พร้อมทำงานที่ http://localhost:%s (โหมด: %s)", cfg.Port, cfg.AppEnv)
	if err := http.ListenAndServe(":"+cfg.Port, handler); err != nil {
		log.Fatalf("เซิร์ฟเวอร์หยุดทำงาน: %v", err)
	}
}
