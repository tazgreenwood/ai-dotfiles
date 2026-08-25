package main

import (
	"log"
	"net/http"
)

func newRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", handleDashboardKanban)

	mux.HandleFunc("GET /reviews", handleDashboardReviews)

	mux.HandleFunc("GET /audits", handleDashboardAudits)

	mux.HandleFunc("GET /projects/{name}", handleProject)

	mux.HandleFunc("GET /projects/{name}/plans/{ticket}", handlePlan)

	mux.HandleFunc("GET /projects/{name}/audit", handleAudit)

	mux.HandleFunc("GET /projects/{name}/deploy-checks", handleDeployChecks)

	mux.HandleFunc("GET /projects/{name}/issues", handleIssues)

	mux.HandleFunc("GET /projects/{name}/reviews", handleReviewsList)

	mux.HandleFunc("GET /projects/{name}/reviews/{id}", handleReviewDetail)

	mux.HandleFunc("GET /projects/{name}/events", handleEvents)

	return mux
}

func main() {
	log.Fatal(http.ListenAndServe(":7432", newRouter()))
}
