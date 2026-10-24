package web

import (
	"fmt"
	"log"
	"net/http"

	"elb/ui/internal/store"
)

func (s *Server) handleAccountsPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)

	users, err := s.store.ListUsers()
	if err != nil {
		log.Printf("accounts: list: %v", err)
	}

	data := map[string]any{"Username": u.Username}
	for k, v := range s.accountsListData(users) {
		data[k] = v
	}
	for k, v := range s.statusBarData(u) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "accounts.html", data); err != nil {
		log.Printf("accounts: render: %v", err)
	}
}

func (s *Server) handleActionAccountsCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	role := r.FormValue("role")
	if role != store.RoleAdmin && role != store.RoleModerator {
		role = store.RoleModerator
	}
	if len(password) < 6 {
		writeToast(w, "Пароль должен быть не короче 6 символов")
		return
	}
	if err := s.store.CreateUser(username, password, role); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось создать пользователя: %v", err))
		return
	}
	s.renderAccountsList(w, r)
}

func (s *Server) handleActionAccountsDelete(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	username := r.PathValue("username")
	if username == u.Username {
		writeToast(w, "Нельзя удалить свою же учётную запись")
		return
	}
	if err := s.store.DeleteUser(username); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось удалить пользователя: %v", err))
		return
	}
	s.renderAccountsList(w, r)
}

func (s *Server) handleActionAccountsSetRole(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	username := r.PathValue("username")
	role := r.FormValue("role")
	if role != store.RoleAdmin && role != store.RoleModerator {
		writeToast(w, "Неизвестная роль")
		return
	}
	if err := s.store.SetUserRole(username, role); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось изменить роль: %v", err))
		return
	}
	s.renderAccountsList(w, r)
}

func (s *Server) handleActionAccountsModeratorPermissions(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeToast(w, "Некорректный запрос")
		return
	}
	p := store.ModeratorPermissions{
		ChatbotBasic: r.FormValue("permChatbotBasic") == "on",
		PanelBasic:   r.FormValue("permPanelBasic") == "on",
		Structures:   r.FormValue("permStructures") == "on",
		History:      r.FormValue("permHistory") == "on",
		ChatHistory:  r.FormValue("permChatHistory") == "on",
		Journal:      r.FormValue("permJournal") == "on",
	}
	if err := s.store.SetModeratorPermissions(p); err != nil {
		writeToast(w, fmt.Sprintf("Не удалось сохранить права: %v", err))
		return
	}

	s.renderAccountsList(w, r)
}

func (s *Server) accountsListData(users []store.User) map[string]any {
	hasModerator := false
	for _, acc := range users {
		if acc.Role == store.RoleModerator {
			hasModerator = true
			break
		}
	}
	return map[string]any{
		"Accounts":     users,
		"HasModerator": hasModerator,

		"ModPerms": s.store.ModeratorPermissions(),
	}
}

func (s *Server) renderAccountsList(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	users, err := s.store.ListUsers()
	if err != nil {
		log.Printf("accounts: list: %v", err)
		return
	}
	data := map[string]any{"Username": u.Username}
	for k, v := range s.accountsListData(users) {
		data[k] = v
	}
	if err := s.execTemplate(w, r, "accounts_list_fragment", data); err != nil {
		log.Printf("accounts: render list: %v", err)
	}
}
