package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func newRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><body><h1>Registry</h1></body></html>"))
	})

	mux.HandleFunc("GET /projects/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		proj, err := ReadProject(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(proj)
	})

	mux.HandleFunc("GET /projects/{name}/plans/{ticket}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		ticket := r.PathValue("ticket")
		plan, err := ReadPlan(name, ticket)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(plan)
	})

	mux.HandleFunc("GET /projects/{name}/audit", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, err := ReadProject(name); err != nil {
			http.NotFound(w, r)
			return
		}
		since := r.URL.Query().Get("since")
		until := r.URL.Query().Get("until")
		entries, err := ReadAudit(name, since, until)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"entries": entries})
	})

	return mux
}

func main() {
	log.Fatal(http.ListenAndServe(":7432", newRouter()))
}
