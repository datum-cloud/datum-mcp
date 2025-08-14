package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func ServeHTTP(s *Service, port int) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/datum/list_crds", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.ListCRDs())
	})

	mux.HandleFunc("/datum/skeleton_crd", func(w http.ResponseWriter, r *http.Request) {
		var req SkeletonReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		resp, err := s.Skeleton(req)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, resp)
	})

	mux.HandleFunc("/datum/list_supported", func(w http.ResponseWriter, r *http.Request) {
		var req ListSupReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		resp, err := s.ListSupported(req)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, resp)
	})

	mux.HandleFunc("/datum/prune_crd", func(w http.ResponseWriter, r *http.Request) {
		var req PruneReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		_, err := s.Prune(req)
		if err != nil {
			if bad, _ := IsUnsupportedRemoved(err); bad {
				http.Error(w, err.Error(), 422)
				return
			}
			http.Error(w, err.Error(), 400)
			return
		}
		resp, _ := s.Prune(req) // safe: nothing removed
		writeJSON(w, resp)
	})

	mux.HandleFunc("/datum/validate_crd", func(w http.ResponseWriter, r *http.Request) {
		var req ValReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		writeJSON(w, s.Validate(req))
	})

	mux.HandleFunc("/datum/refresh_discovery", func(w http.ResponseWriter, r *http.Request) {
		ok, count, err := s.RefreshDiscovery()
		if err != nil {
			http.Error(w, fmt.Sprintf("refresh failed: %v", err), 500)
			return
		}
		writeJSON(w, map[string]any{"ok": ok, "count": count})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	return http.ListenAndServe(addr, mux)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
