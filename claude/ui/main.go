package main

import (
	"log"
	"net/http"
)

func newRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", handleIndex)

	mux.HandleFunc("GET /projects/{name}", handleProject)

	mux.HandleFunc("GET /projects/{name}/plans/{ticket}", handlePlan)

	mux.HandleFunc("GET /projects/{name}/audit", handleAudit)

	return mux
}

func main() {
	log.Fatal(http.ListenAndServe(":7432", newRouter()))
}
