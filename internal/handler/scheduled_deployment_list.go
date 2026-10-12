package handler

import (
	"net/http"
	"strconv"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

func scheduleOffset(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("offset")
	if raw == "" {
		return 0, nil
	}
	offset, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, strconv.ErrRange
	}
	return offset, nil
}

func (h *ScheduledDeploymentHandler) loadSchedulePage(
	r *http.Request, projectID int64,
) (pages.ScheduleListPage, error) {
	const pageLimit int64 = 50
	offset, err := scheduleOffset(r)
	if err != nil {
		return pages.ScheduleListPage{}, err
	}
	total, err := h.repo.Queries.CountScheduledDeploymentsByProject(
		r.Context(), projectID,
	)
	if err != nil {
		return pages.ScheduleListPage{}, err
	}
	// Keep the last page usable after deleting its final item.
	if offset >= total {
		offset = max(0, (total-1)/pageLimit) * pageLimit
	}
	rows, err := h.repo.Queries.ListScheduledDeploymentDetails(r.Context(),
		db.ListScheduledDeploymentDetailsParams{
			ProjectID: projectID, Limit: pageLimit, Offset: offset,
		})
	if err != nil {
		return pages.ScheduleListPage{}, err
	}
	page := pages.ScheduleListPage{
		Offset: offset, Limit: pageLimit, Total: total,
		Items: make([]pages.ScheduledDeploymentListItem, 0, len(rows)),
	}
	for _, row := range rows {
		page.Items = append(page.Items, pages.ScheduledDeploymentListItem{
			Schedule:        row.ScheduledDeployment,
			ReleaseVersion:  row.ReleaseVersion,
			EnvironmentName: row.EnvironmentName,
			Offset:          offset,
		})
	}
	return page, nil
}
