package pages

import (
	"fmt"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type TokenSettingsView struct {
	Projects   []db.Project
	Selections map[string][]int64
	Form       repository.TokenScope
	Name       string
}

func tokenProjectNames(ids []int64, projects []db.Project) string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		name := fmt.Sprintf("Project %d", id)
		for _, project := range projects {
			if project.ID == id {
				name = project.Name
				break
			}
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}
