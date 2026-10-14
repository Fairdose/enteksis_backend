package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (h *handler) deskAddChecklist(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Title string `json:"title"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz kontrol maddesi."})
		return
	}
	p.Title = strings.TrimSpace(p.Title)
	if len(p.Title) < 3 || len(p.Title) > 500 {
		writeJSON(w, 422, map[string]string{"error": "Madde 3–500 karakter olmalı."})
		return
	}
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		id := r.PathValue("id")
		var state string
		err := tx.QueryRow(ctx, `SELECT op_state::text FROM tasks WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, id, d.WorkspaceID).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, deskError{404, "task_not_found", ""}
		}
		if err != nil {
			return 0, nil, err
		}
		if state == "closed" || state == "cancelled" {
			return 0, nil, deskError{409, "invalid_transition", ""}
		}
		allowed, err := deskHasTaskRole(ctx, tx, d, id, "owner")
		if err != nil {
			return 0, nil, err
		}
		if !allowed {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		var itemID string
		err = tx.QueryRow(ctx, `INSERT INTO checklist_items(task_id,title,position,created_by) VALUES($1,$2,(SELECT count(*) FROM checklist_items WHERE task_id=$1),$3) RETURNING id`, id, p.Title, d.ActorID).Scan(&itemID)
		if err != nil {
			return 0, nil, err
		}
		err = deskEvent(ctx, tx, d, id, "task.checklist_added", map[string]string{"item_id": itemID})
		return 201, map[string]string{"id": itemID, "title": p.Title}, err
	})
}

func (h *handler) deskToggleChecklist(w http.ResponseWriter, r *http.Request) {
	var p struct {
		IsDone *bool `json:"is_done"`
	}
	if deskDecode(w, r, &p) != nil || p.IsDone == nil {
		writeJSON(w, 400, map[string]string{"error": "Durum gerekli."})
		return
	}
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		id := r.PathValue("id")
		var state string
		err := tx.QueryRow(ctx, `SELECT op_state::text FROM tasks WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, id, d.WorkspaceID).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, deskError{404, "task_not_found", ""}
		}
		if err != nil {
			return 0, nil, err
		}
		if state == "closed" || state == "cancelled" {
			return 0, nil, deskError{409, "invalid_transition", ""}
		}
		owner, err := deskHasTaskRole(ctx, tx, d, id, "owner")
		if err != nil {
			return 0, nil, err
		}
		assigned, err := deskHasTaskRole(ctx, tx, d, id, "primary_assignee")
		if err != nil {
			return 0, nil, err
		}
		if !owner && !assigned {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		var itemID string
		err = tx.QueryRow(ctx, `UPDATE checklist_items SET is_done=$3,done_at=CASE WHEN $3 THEN now() ELSE NULL END WHERE id=$1 AND task_id=$2 RETURNING id`, r.PathValue("item_id"), id, *p.IsDone).Scan(&itemID)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, deskError{404, "checklist_item_not_found", ""}
		}
		if err != nil {
			return 0, nil, err
		}
		err = deskEvent(ctx, tx, d, id, "task.checklist_updated", map[string]any{"item_id": itemID, "is_done": *p.IsDone})
		return 200, map[string]any{"id": itemID, "is_done": *p.IsDone}, err
	})
}
