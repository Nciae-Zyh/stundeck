package httpapi

import "net/http"

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.Events(r.Context(), 100)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
