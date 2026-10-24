package web

import (
	"fmt"
	"html"
	"log"
	"net/http"

	"elb/ui/internal/i18n"
)

func (s *Server) handleActionServerStart(w http.ResponseWriter, r *http.Request) {
	s.handleActionServerAction(w, r, "start")
}
func (s *Server) handleActionServerStop(w http.ResponseWriter, r *http.Request) {
	s.handleActionServerAction(w, r, "stop")
}
func (s *Server) handleActionServerRestart(w http.ResponseWriter, r *http.Request) {
	s.handleActionServerAction(w, r, "restart")
}

func (s *Server) handleActionServerAction(w http.ResponseWriter, r *http.Request, actionType string) {
	u := userFromCtx(r)
	var err error
	switch actionType {
	case "start":
		err = s.daemon.StartServer()
	case "stop":
		err = s.daemon.ManualStopServer()
	case "restart":
		err = s.daemon.ManualRestartServer()
	}
	if err != nil {
		log.Printf("action/server/%s: %v", actionType, err)
	}
	if renderErr := s.execTemplate(w, r, "topbar_status_inner", s.statusBarData(u)); renderErr != nil {
		log.Printf("action/server/%s: render: %v", actionType, renderErr)
	}
	if err != nil {
		lang := i18n.Default
		if u != nil {
			lang = u.Locale
		}

		fmt.Fprintf(w, `<div id="toast" hx-swap-oob="true">%s: %s</div>`,
			html.EscapeString(i18n.T(lang, "server.action.error")), html.EscapeString(err.Error()))
	}
}
