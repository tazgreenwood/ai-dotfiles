package main

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates
var templateFS embed.FS

var tmpl = template.Must(template.ParseFS(templateFS, "templates/*.html"))

type breadcrumb struct {
	Label string
	URL   string
}

type indexData struct {
	Breadcrumbs []breadcrumb
	Projects    []projectSummary
}

type projectSummary struct {
	Name       string
	PlanCount  int
	AuditCount int
}

type projectData struct {
	Breadcrumbs []breadcrumb
	Project     Project
	Plans       []planSummary
	RecentAudit []AuditEntry
}

type planSummary struct {
	PlanMeta
	DoneSteps  int
	TotalSteps int
}

func handleIndex(w http.ResponseWriter, _ *http.Request) {
	projects, err := ReadProjects()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	summaries := make([]projectSummary, 0, len(projects))
	for _, p := range projects {
		plans, _ := ReadPlans(p.Name)
		audit, _ := ReadAudit(p.Name, "", "")
		summaries = append(summaries, projectSummary{
			Name:       p.Name,
			PlanCount:  len(plans),
			AuditCount: len(audit),
		})
	}

	data := indexData{
		Breadcrumbs: []breadcrumb{{Label: "Registry", URL: "/"}},
		Projects:    summaries,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func handleProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	proj, err := ReadProject(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	metas, _ := ReadPlans(name)
	plans := make([]planSummary, 0, len(metas))
	for _, m := range metas {
		full, err := ReadPlan(name, m.Ticket)
		done := 0
		total := 0
		if err == nil {
			total = len(full.PlanSteps)
			for _, s := range full.PlanSteps {
				if s.Status == "done" {
					done++
				}
			}
		}
		plans = append(plans, planSummary{
			PlanMeta:   m,
			DoneSteps:  done,
			TotalSteps: total,
		})
	}

	allAudit, _ := ReadAudit(name, "", "")
	recent := allAudit
	if len(recent) > 5 {
		recent = recent[len(recent)-5:]
	}

	data := projectData{
		Breadcrumbs: []breadcrumb{
			{Label: "Registry", URL: "/"},
			{Label: name},
		},
		Project:     proj,
		Plans:       plans,
		RecentAudit: recent,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}
