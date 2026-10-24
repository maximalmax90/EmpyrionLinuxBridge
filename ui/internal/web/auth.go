package web

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"

	"elb/ui/internal/store"
)

type ctxKey int

const userCtxKey ctxKey = 0

func (s *Server) sessionUser(r *http.Request) *userInfo {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	u, err := s.store.Session(c.Value)
	if err != nil || u == nil {
		return nil
	}
	return &userInfo{
		Username:           u.Username,
		MustChangePassword: u.MustChangePassword,
		Role:               u.Role,
		Theme:              u.Theme,
		Locale:             u.Locale,
		token:              c.Value,
	}
}

type userInfo struct {
	Username           string
	MustChangePassword bool
	Role               string
	Theme              string
	Locale             string
	token              string
}

func (u *userInfo) IsAdmin() bool { return u.Role == store.RoleAdmin }

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.sessionUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, u)))
	})
}

func (s *Server) requireFullAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.sessionUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if u.MustChangePassword {
			http.Redirect(w, r, "/change-password", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, u)))
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.requireFullAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r)
		if !u.IsAdmin() {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			http.Error(w, "Доступ только для администраторов панели", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) requirePermission(check func(store.ModeratorPermissions) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return s.requireFullAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := userFromCtx(r)
			if !u.IsAdmin() && !check(s.store.ModeratorPermissions()) {
				if r.Method == http.MethodGet {
					http.Redirect(w, r, "/", http.StatusSeeOther)
					return
				}
				http.Error(w, "Недостаточно прав", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

func (s *Server) handleActionProfilePreferences(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	theme := r.FormValue("theme")
	if theme != "dark" {
		theme = "light"
	}
	locale := r.FormValue("locale")
	valid := false
	for _, l := range availableLocales() {
		if l == locale {
			valid = true
			break
		}
	}
	if !valid {
		locale = u.Locale
	}
	if err := s.store.SetPreferences(u.Username, theme, locale); err != nil {
		log.Printf("profile: preferences: %v", err)
	}
	redirectTo := r.Referer()
	if redirectTo == "" {
		redirectTo = "/"
	}
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}

func userFromCtx(r *http.Request) *userInfo {
	u, _ := r.Context().Value(userCtxKey).(*userInfo)
	return u
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.sessionUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, r, "")
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, errMsg string) {
	s.execTemplate(w, r, "login.html", map[string]any{"Error": errMsg})
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if first = strings.TrimSpace(first); first != "" {
				return first
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) recordLoginAttempt(username string, success bool, ip string) {
	go func() {
		if err := s.daemon.RecordLogin(username, success, ip); err != nil {
			log.Printf("audit: record login: %v", err)
		}
	}()
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.trustProxyHeaders)
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, r, "Некорректная форма")
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	u, err := s.store.Authenticate(username, password)
	if err != nil {
		log.Printf("login: %q from %s: %v", username, ip, err)
		s.renderLogin(w, r, "Внутренняя ошибка")
		return
	}
	if u == nil {
		s.recordLoginAttempt(username, false, ip)
		s.renderLogin(w, r, "Неверный логин или пароль")
		return
	}
	s.recordLoginAttempt(u.Username, true, ip)

	token, err := s.store.CreateSession(u.Username)
	if err != nil {
		s.renderLogin(w, r, "Внутренняя ошибка")
		return
	}
	setSessionCookie(w, r, token)

	if u.MustChangePassword {
		http.Redirect(w, r, "/change-password", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleChangePasswordPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	s.renderChangePassword(w, r, u.Username, u.MustChangePassword, "")
}

func (s *Server) renderChangePassword(w http.ResponseWriter, r *http.Request, username string, mustChange bool, errMsg string) {
	s.execTemplate(w, r, "change_password.html", map[string]any{
		"Username":           username,
		"MustChangePassword": mustChange,
		"Error":              errMsg,
	})
}

func (s *Server) handleChangePasswordSubmit(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if err := r.ParseForm(); err != nil {
		s.renderChangePassword(w, r, u.Username, u.MustChangePassword, "Некорректная форма")
		return
	}
	currentPassword := r.FormValue("currentPassword")
	newUsername := r.FormValue("username")
	password := r.FormValue("password")
	password2 := r.FormValue("password2")

	if authUser, err := s.store.Authenticate(u.Username, currentPassword); err != nil {
		s.renderChangePassword(w, r, u.Username, u.MustChangePassword, "Внутренняя ошибка")
		return
	} else if authUser == nil {
		s.renderChangePassword(w, r, u.Username, u.MustChangePassword, "Неверный текущий пароль")
		return
	}
	if newUsername == "" {
		s.renderChangePassword(w, r, u.Username, u.MustChangePassword, "Логин не может быть пустым")
		return
	}
	if password != password2 {
		s.renderChangePassword(w, r, u.Username, u.MustChangePassword, "Пароли не совпадают")
		return
	}
	if len(password) < 6 {
		s.renderChangePassword(w, r, u.Username, u.MustChangePassword, "Пароль должен быть не короче 6 символов")
		return
	}

	if err := s.store.SetPassword(u.Username, newUsername, password); err != nil {
		s.renderChangePassword(w, r, u.Username, u.MustChangePassword, err.Error())
		return
	}
	log.Printf("password: changed for %q (was %q) from %s", newUsername, u.Username, clientIP(r, s.trustProxyHeaders))

	token, err := s.store.CreateSession(newUsername)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	setSessionCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}
