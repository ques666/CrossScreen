//go:build windows

package control

import (
	"encoding/json"
	"net/http"

	"crossscreen/platform/winsec"
)

// winServiceView is the UI-facing state of the secure-input service.
type winServiceView struct {
	Supported bool `json:"supported"`
	winsec.ServiceStatusInfo
}

func (a *App) handleWinService(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		info, err := winsec.QueryService()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, winServiceView{Supported: true, ServiceStatusInfo: info})
	case http.MethodPost:
		var req struct {
			Action string `json:"action"` // "install" | "uninstall"
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		var err error
		switch req.Action {
		case "install":
			err = winsec.InstallService()
		case "uninstall":
			err = winsec.UninstallService()
		default:
			writeErr(w, http.StatusBadRequest, "action must be install or uninstall")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		info, qErr := winsec.QueryService()
		if qErr != nil {
			writeJSON(w, http.StatusOK, winServiceView{Supported: true})
			return
		}
		writeJSON(w, http.StatusOK, winServiceView{Supported: true, ServiceStatusInfo: info})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}
