package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"dormfix/internal/config"
	"dormfix/internal/models"
	"dormfix/internal/repository"
)

type contextKey string

const (
	UserContextKey    contextKey = "user"
	SessionContextKey contextKey = "session"
	CSRFContextKey    contextKey = "csrf_token"

	CSRFCookieName    = "dormfix_csrf"
	SessionCookieName = "dormfix_session"
)

// GenerateCSRFToken สร้าง Token ที่ประกอบด้วย Nonce สุ่มและลายเซ็น HMAC จาก Server Secret
func GenerateCSRFToken(secret string) string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback กรณี crypto/rand มีปัญหา
		bytes = []byte(time.Now().Format(time.RFC3339Nano))
	}
	nonce := hex.EncodeToString(bytes)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(nonce))
	sig := hex.EncodeToString(h.Sum(nil))

	return nonce + "." + sig
}

// ValidateCSRFToken ตรวจสอบว่า Token มีรูปแบบถูกต้องและตรงกับลายเซ็น HMAC ของ Server Secret
func ValidateCSRFToken(token, secret string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
		return false
	}
	nonce := parts[0]
	sig := parts[1]

	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(nonce))
	expectedSig := hex.EncodeToString(h.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(sig), []byte(expectedSig)) == 1
}

// UniversalCSRFMiddleware ตรวจสอบ CSRF Token สำหรับทุกคำขอแบบ POST รวมถึง /login และ /register
func UniversalCSRFMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var token string

			// อ่าน CSRF Token จาก Cookie เดิม
			cookie, err := r.Cookie(CSRFCookieName)
			if err == nil && ValidateCSRFToken(cookie.Value, cfg.SessionSecret) {
				token = cookie.Value
			} else {
				// สร้าง Token ใหม่พร้อมลายเซ็น HMAC ป้องกันการปลอมแปลง Cookie
				token = GenerateCSRFToken(cfg.SessionSecret)
				http.SetCookie(w, &http.Cookie{
					Name:     CSRFCookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: true,
					Secure:   cfg.SecureCookie,
					SameSite: http.SameSiteLaxMode,
					Expires:  time.Now().Add(24 * time.Hour),
				})
			}

			// แนบ CSRF Token ลงใน Context เพื่อให้ Handler และ Template ดึงไปแสดงในฟอร์มได้
			ctx := context.WithValue(r.Context(), CSRFContextKey, token)
			r = r.WithContext(ctx)

			// ตรวจสอบเข้มงวดเมื่อมีการส่งข้อมูลผ่าน POST, PUT, DELETE, PATCH
			if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete || r.Method == http.MethodPatch {
				formToken := r.FormValue("csrf_token")
				if formToken == "" {
					formToken = r.Header.Get("X-CSRF-Token")
				}

				cookieToken := ""
				if c, err := r.Cookie(CSRFCookieName); err == nil {
					cookieToken = c.Value
				}

				// 1. ต้องมี Token ทั้งใน Form และใน Cookie
				// 2. Token ใน Form ต้องตรงกับใน Cookie
				// 3. Token ต้องมีลายเซ็น HMAC ที่ถูกต้องตาม Server Secret
				if formToken == "" || cookieToken == "" ||
					subtle.ConstantTimeCompare([]byte(formToken), []byte(cookieToken)) != 1 ||
					!ValidateCSRFToken(formToken, cfg.SessionSecret) {
					http.Error(w, "คำขอถูกปฏิเสธ: CSRF Token ไม่ถูกต้องหรือไม่ได้รับอนุญาต (403 Forbidden)", http.StatusForbidden)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetCSRFToken ดึง Token จาก Context สำหรับส่งต่อไปยัง Template
func GetCSRFToken(r *http.Request) string {
	if token, ok := r.Context().Value(CSRFContextKey).(string); ok {
		return token
	}
	return ""
}

// SessionMiddleware ตรวจสอบ Session Cookie และโหลด User เข้าสู่ Context หากมีเซสชันที่ยังไม่หมดอายุ
func SessionMiddleware(repo *repository.UserRepository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err == nil && cookie.Value != "" {
				sess, err := repo.GetSessionByToken(cookie.Value)
				if err == nil && sess != nil {
					user, err := repo.GetUserByID(sess.UserID)
					if err == nil && user != nil {
						ctx := context.WithValue(r.Context(), UserContextKey, user)
						ctx = context.WithValue(ctx, SessionContextKey, sess)
						r = r.WithContext(ctx)
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetCurrentUser ดึง User จาก Context
func GetCurrentUser(r *http.Request) *models.User {
	if u, ok := r.Context().Value(UserContextKey).(*models.User); ok {
		return u
	}
	return nil
}

// RequireAuth บังคับว่าต้องเข้าสู่ระบบก่อน จึงจะเข้าถึงได้
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := GetCurrentUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireActiveUser ตรวจสอบสิทธิ์ฝั่งผู้เช่า: ต้องล็อกอิน, สถานะต้องเป็น active, และ role เป็น user
func RequireActiveUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := GetCurrentUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if u.Status == models.UserStatusPending {
			http.Redirect(w, r, "/pending", http.StatusSeeOther)
			return
		}

		if u.Status == models.UserStatusSuspended {
			http.Error(w, "บัญชีห้องพักของท่านถูกระงับการใช้งาน กรุณาติดต่อสำนักงานหอพัก (403 Forbidden)", http.StatusForbidden)
			return
		}

		if u.Role != models.RoleUser {
			http.Error(w, "คุณไม่มีสิทธิ์เข้าถึงส่วนนี้ (403 Forbidden)", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RequireAdmin ตรวจสอบสิทธิ์ฝั่งแอดมิน: ต้องล็อกอิน, สถานะต้องเป็น active, และ role เป็น admin
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := GetCurrentUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if u.Role != models.RoleAdmin || u.Status != models.UserStatusActive {
			http.Error(w, "คุณไม่มีสิทธิ์เข้าถึงส่วนของผู้ดูแลระบบ (403 Forbidden)", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// SetSessionCookie สร้าง Cookie สำหรับเก็บ Session Token อย่างปลอดภัย
func SetSessionCookie(w http.ResponseWriter, token string, expires time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
}

// ClearSessionCookie เคลียร์ Cookie เมื่อ Logout
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}
