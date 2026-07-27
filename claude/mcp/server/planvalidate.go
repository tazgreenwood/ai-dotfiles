package main

import "fmt"

// validatePlanSteps checks plan_steps for unsafe async groupings before a
// plan is persisted. Steps with execution=="async" are grouped by their
// parallel_group; within each group, every pair of steps must have disjoint
// file sets (missing/empty files count as unknown overlap and are rejected).
// Sync steps (or steps with no execution field) are never checked.
func validatePlanSteps(steps []any) error {
	groups := map[string][]map[string]any{}

	for _, s := range steps {
		step, ok := s.(map[string]any)
		if !ok {
			continue
		}
		execution, _ := step["execution"].(string)
		if execution != "async" {
			continue
		}
		group, _ := step["parallel_group"].(string)
		groups[group] = append(groups[group], step)
	}

	for group, groupSteps := range groups {
		fileSets := make([]map[string]bool, len(groupSteps))
		for i, step := range groupSteps {
			files, ok := step["files"].([]any)
			if !ok || len(files) == 0 {
				title, _ := step["title"].(string)
				return fmt.Errorf("async step %q in parallel_group %q has missing or empty files; cannot rule out overlap", title, group)
			}
			set := make(map[string]bool, len(files))
			for _, f := range files {
				if fs, ok := f.(string); ok {
					set[fs] = true
				}
			}
			fileSets[i] = set
		}

		for i := 0; i < len(fileSets); i++ {
			for j := i + 1; j < len(fileSets); j++ {
				for f := range fileSets[i] {
					if fileSets[j][f] {
						titleI, _ := groupSteps[i]["title"].(string)
						titleJ, _ := groupSteps[j]["title"].(string)
						return fmt.Errorf("async steps %q and %q in parallel_group %q both touch file %q", titleI, titleJ, group, f)
					}
				}
			}
		}
	}

	return nil
}
