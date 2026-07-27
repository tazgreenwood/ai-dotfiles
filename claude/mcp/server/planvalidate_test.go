package main

import "testing"

func TestValidatePlanSteps_AsyncOverlappingFiles_Errors(t *testing.T) {
	steps := []any{
		map[string]any{
			"title":          "step one",
			"execution":      "async",
			"parallel_group": "group-a",
			"files":          []any{"foo.go", "bar.go"},
		},
		map[string]any{
			"title":          "step two",
			"execution":      "async",
			"parallel_group": "group-a",
			"files":          []any{"bar.go", "baz.go"},
		},
	}
	if err := validatePlanSteps(steps); err == nil {
		t.Fatal("want error for async steps sharing a parallel_group with overlapping files, got nil")
	}
}

func TestValidatePlanSteps_AsyncDisjointFiles_NoError(t *testing.T) {
	steps := []any{
		map[string]any{
			"title":          "step one",
			"execution":      "async",
			"parallel_group": "group-a",
			"files":          []any{"foo.go"},
		},
		map[string]any{
			"title":          "step two",
			"execution":      "async",
			"parallel_group": "group-a",
			"files":          []any{"bar.go"},
		},
	}
	if err := validatePlanSteps(steps); err != nil {
		t.Fatalf("want nil for disjoint files, got %v", err)
	}
}

func TestValidatePlanSteps_SyncStepsNeverChecked(t *testing.T) {
	steps := []any{
		map[string]any{
			"title":     "step one",
			"execution": "sync",
			"files":     []any{"foo.go"},
		},
		map[string]any{
			"title":     "step two",
			"execution": "sync",
			"files":     []any{"foo.go"},
		},
	}
	if err := validatePlanSteps(steps); err != nil {
		t.Fatalf("want nil for sync steps regardless of overlap, got %v", err)
	}
}

func TestValidatePlanSteps_NoExecutionFieldNeverChecked(t *testing.T) {
	steps := []any{
		map[string]any{
			"title": "step one",
			"files": []any{"foo.go"},
		},
		map[string]any{
			"title": "step two",
			"files": []any{"foo.go"},
		},
	}
	if err := validatePlanSteps(steps); err != nil {
		t.Fatalf("want nil for steps with no execution field, got %v", err)
	}
}

func TestValidatePlanSteps_AsyncMissingFiles_Errors(t *testing.T) {
	steps := []any{
		map[string]any{
			"title":          "step one",
			"execution":      "async",
			"parallel_group": "group-a",
			"files":          []any{"foo.go"},
		},
		map[string]any{
			"title":          "step two",
			"execution":      "async",
			"parallel_group": "group-a",
			// no files field at all
		},
	}
	if err := validatePlanSteps(steps); err == nil {
		t.Fatal("want error for async step with missing files, got nil")
	}
}

func TestValidatePlanSteps_AsyncEmptyFiles_Errors(t *testing.T) {
	steps := []any{
		map[string]any{
			"title":          "step one",
			"execution":      "async",
			"parallel_group": "group-a",
			"files":          []any{"foo.go"},
		},
		map[string]any{
			"title":          "step two",
			"execution":      "async",
			"parallel_group": "group-a",
			"files":          []any{},
		},
	}
	if err := validatePlanSteps(steps); err == nil {
		t.Fatal("want error for async step with empty files, got nil")
	}
}

func TestValidatePlanSteps_DifferentParallelGroups_NoOverlapCheck(t *testing.T) {
	steps := []any{
		map[string]any{
			"title":          "step one",
			"execution":      "async",
			"parallel_group": "group-a",
			"files":          []any{"foo.go"},
		},
		map[string]any{
			"title":          "step two",
			"execution":      "async",
			"parallel_group": "group-b",
			"files":          []any{"foo.go"},
		},
	}
	if err := validatePlanSteps(steps); err != nil {
		t.Fatalf("want nil for async steps in different parallel_groups even with overlapping files, got %v", err)
	}
}
