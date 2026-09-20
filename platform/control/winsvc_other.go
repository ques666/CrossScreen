//go:build !windows

package control

import "net/http"

// The secure-desktop service only exists on Windows.
func (a *App) handleWinService(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Supported bool `json:"supported"`
	}{Supported: false})
}
