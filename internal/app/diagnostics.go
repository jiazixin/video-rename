package app

import "net/http"

func (a *App) logDiagnostics(w http.ResponseWriter, r *http.Request) {
	var request, response, processing string
	err := a.DB.Read().QueryRowContext(r.Context(), "SELECT raw_request,raw_response,processing_log FROM events WHERE id=? AND category IN ('success','failure')", number(r.PathValue("id"))).Scan(&request, &response, &processing)
	if err != nil {
		failure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	send(w, 200, map[string]string{"request": request, "response": response, "processing": processing})
}
