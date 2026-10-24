package api

import (
	"fmt"
	"net/http"

	"elb/daemon/internal/config"
	"elb/daemon/internal/history"
	"elb/daemon/internal/logbuf"
	"elb/daemon/internal/modclient"
)

func registerHistoryRoutes(mux *http.ServeMux, cfg config.Config, mod *modclient.Client, logBuf *logbuf.Buffer) {

	mux.HandleFunc("POST /api/history/around", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Kind             string  `json:"kind"`
			Key              string  `json:"key"`
			EventKind        string  `json:"eventKind"`
			Playfield        string  `json:"playfield"`
			From             string  `json:"from"`
			To               string  `json:"to"`
			Distance         float64 `json:"distance"`
			HideFirst        bool    `json:"hideFirst"`
			HideOnlyPosition bool    `json:"hideOnlyPosition"`

			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		}
		if !decodeOrErr(w, r, &body) {
			return
		}
		if body.Kind != "" && body.Kind != "player" && body.Kind != "structure" {
			writeErr(w, fmt.Errorf("kind должен быть пустым, 'player' или 'structure'"))
			return
		}
		if body.EventKind != "" && body.EventKind != "player" && body.EventKind != "structure" {
			writeErr(w, fmt.Errorf("eventKind должен быть пустым, 'player' или 'structure'"))
			return
		}
		if body.Distance <= 0 {
			body.Distance = 1000
		}
		if body.Limit <= 0 {
			body.Limit = 1000
		}
		if body.Offset < 0 {
			body.Offset = 0
		}
		result, err := history.Around(cfg, history.Query{
			AnchorKind: body.Kind, AnchorKey: body.Key,
			Kind: body.EventKind, Playfield: body.Playfield,
			FromTs: body.From, ToTs: body.To,
			MaxDistance: body.Distance, HideFirst: body.HideFirst, HideOnlyPosition: body.HideOnlyPosition,
			Limit: body.Limit, Offset: body.Offset,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, result)
	})

	mux.HandleFunc("GET /api/history/counts", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if kind != "player" && kind != "structure" {
			writeErr(w, fmt.Errorf("kind должен быть 'player' или 'structure'"))
			return
		}
		counts, err := history.CountsByKind(cfg, kind)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, counts)
	})
}
