package todo

import (
	"fmt"
	"sort"
	"strings"
)

func (s Snapshot) UnresolvedBlockers(id string) []string {
	var dependencies []string
	completed := make(map[string]bool, len(s.Items))
	for _, item := range s.Items {
		completed[item.ID] = item.Status == StatusCompleted
		if item.ID == id {
			dependencies = item.BlockedBy
		}
	}
	var unresolved []string
	for _, dependency := range dependencies {
		if !completed[dependency] {
			unresolved = append(unresolved, dependency)
		}
	}
	return unresolved
}

func validateDependencies(items []Item) error {
	byID := make(map[string]int, len(items))
	for i, item := range items {
		byID[item.ID] = i
	}
	remaining := make([]int, len(items))
	dependents := make([][]int, len(items))
	for i := range items {
		item := &items[i]
		seen := make(map[string]bool, len(item.BlockedBy))
		for j, dependency := range item.BlockedBy {
			dependency = strings.TrimSpace(dependency)
			item.BlockedBy[j] = dependency
			if dependency == "" {
				return fmt.Errorf("item %q blocked_by[%d] must not be empty", item.ID, j)
			}
			if dependency == item.ID {
				return fmt.Errorf("item %q cannot block itself", item.ID)
			}
			if seen[dependency] {
				return fmt.Errorf("item %q has duplicate blocked_by id %q", item.ID, dependency)
			}
			seen[dependency] = true
			prerequisite, ok := byID[dependency]
			if !ok {
				return fmt.Errorf("item %q blocked_by references unknown id %q", item.ID, dependency)
			}
			remaining[i]++
			dependents[prerequisite] = append(dependents[prerequisite], i)
		}
		sort.Strings(item.BlockedBy)
	}
	queue := make([]int, 0, len(items))
	for i, count := range remaining {
		if count == 0 {
			queue = append(queue, i)
		}
	}
	for head := 0; head < len(queue); head++ {
		for _, dependent := range dependents[queue[head]] {
			remaining[dependent]--
			if remaining[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}
	if len(queue) != len(items) {
		return fmt.Errorf("blocked_by dependencies contain a cycle")
	}
	for _, item := range items {
		if item.Status == StatusPending {
			continue
		}
		for _, dependency := range item.BlockedBy {
			if items[byID[dependency]].Status != StatusCompleted {
				return fmt.Errorf("item %q cannot be %s with unfinished prerequisite %q; keep it pending", item.ID, item.Status, dependency)
			}
		}
	}
	return nil
}
