package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type deskQueryRow interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func deskCanTaskAction(ctx context.Context, q deskQueryRow, d deskIdentity, taskID, action string) (bool, error) {
	if d.Admin {
		return true, nil
	}
	var allowed bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t WHERE t.id=$1 AND t.workspace_id=$2 AND (
		EXISTS(SELECT 1 FROM task_assignments ta WHERE ta.task_id=t.id AND ta.actor_id=$3 AND ta.role='owner' AND ta.status='active') OR
		EXISTS(SELECT 1 FROM role_bindings rb WHERE rb.workspace_id=t.workspace_id AND rb.actor_id=$3 AND rb.status='active' AND rb.role='project_manager' AND (rb.scope_type='workspace' OR rb.scope_type='project' AND rb.scope_id=t.project_id)) OR
		EXISTS(SELECT 1 FROM permission_grants g WHERE g.workspace_id=t.workspace_id AND g.actor_id=$3 AND g.action=$4 AND g.status='active' AND g.valid_from<=now() AND (g.valid_until IS NULL OR g.valid_until>now()) AND (g.scope_type='workspace' OR g.scope_type='project' AND g.scope_id=t.project_id OR g.scope_type='task' AND g.scope_id=t.id))
	))`, taskID, d.WorkspaceID, d.ActorID, action).Scan(&allowed)
	return allowed, err
}

func (h *handler) deskCreateGrant(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ActorID    string     `json:"actor_id"`
		Action     string     `json:"action"`
		ScopeType  string     `json:"scope_type"`
		ScopeID    string     `json:"scope_id"`
		ValidUntil *time.Time `json:"valid_until"`
		Reason     string     `json:"reason"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz yetki."})
		return
	}
	if p.Action != "assign_task" && p.Action != "reassign_task" && p.Action != "manage_task_participants" {
		writeJSON(w, 422, map[string]string{"error": "Geçersiz yetki eylemi."})
		return
	}
	if p.ScopeType != "workspace" && p.ScopeType != "project" && p.ScopeType != "task" {
		writeJSON(w, 422, map[string]string{"error": "Geçersiz yetki kapsamı."})
		return
	}
	if p.ScopeType == "workspace" {
		p.ScopeID = ""
	} else if p.ScopeID == "" {
		writeJSON(w, 422, map[string]string{"error": "Kapsam kimliği gerekli."})
		return
	}
	if p.ValidUntil != nil && !p.ValidUntil.After(time.Now()) {
		writeJSON(w, 422, map[string]string{"error": "Bitiş tarihi gelecekte olmalı."})
		return
	}
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		if !d.Admin {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		var exists bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM actors WHERE id=$1 AND workspace_id=$2 AND type='internal' AND status='active')`, p.ActorID, d.WorkspaceID).Scan(&exists)
		if err != nil {
			return 0, nil, err
		}
		if !exists {
			return 0, nil, deskError{422, "invalid_actor", ""}
		}
		if p.ScopeType != "workspace" {
			table := "projects"
			if p.ScopeType == "task" {
				table = "tasks"
			}
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=$1 AND workspace_id=$2)`, p.ScopeID, d.WorkspaceID).Scan(&exists)
			if err != nil {
				return 0, nil, err
			}
			if !exists {
				return 0, nil, deskError{422, "invalid_scope", ""}
			}
		}
		var id string
		var scopeID any
		if p.ScopeID != "" {
			scopeID = p.ScopeID
		}
		err = tx.QueryRow(ctx, `INSERT INTO permission_grants(workspace_id,actor_id,action,scope_type,scope_id,granted_by,valid_until,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, d.WorkspaceID, p.ActorID, p.Action, p.ScopeType, scopeID, d.ActorID, p.ValidUntil, p.Reason).Scan(&id)
		return 201, map[string]string{"id": id}, err
	})
}

func (h *handler) deskListGrants(w http.ResponseWriter, r *http.Request) {
	d, err := h.deskIdentity(r.Context())
	if err != nil {
		deskWriteError(w, err)
		return
	}
	actor := r.URL.Query().Get("actor_id")
	if actor == "" {
		actor = d.ActorID
	}
	if !d.Admin && actor != d.ActorID {
		writeJSON(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	rows, err := h.deskPool.Query(r.Context(), `SELECT id,action,scope_type,coalesce(scope_id::text,''),status::text,valid_until FROM permission_grants WHERE workspace_id=$1 AND actor_id=$2 ORDER BY granted_at DESC`, d.WorkspaceID, actor)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, action, scopeType, scopeID, status string
		var validUntil *time.Time
		if err = rows.Scan(&id, &action, &scopeType, &scopeID, &status, &validUntil); err != nil {
			deskWriteError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "action": action, "scope_type": scopeType, "scope_id": scopeID, "status": status, "valid_until": validUntil})
	}
	if rows.Err() != nil {
		deskWriteError(w, rows.Err())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (h *handler) deskRevokeGrant(w http.ResponseWriter, r *http.Request) {
	h.deskMutate(w, r, map[string]string{}, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		if !d.Admin {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		var id string
		err := tx.QueryRow(ctx, `UPDATE permission_grants SET status='revoked',revoked_by=$3,revoked_at=now() WHERE id=$1 AND workspace_id=$2 AND status='active' RETURNING id`, r.PathValue("id"), d.WorkspaceID, d.ActorID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, deskError{404, "grant_not_found", ""}
		}
		return 200, map[string]string{"id": id, "status": "revoked"}, err
	})
}

func (h *handler) deskAddAssignment(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ActorID string `json:"actor_id"`
		Role    string `json:"role"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz atama."})
		return
	}
	if p.Role != "contributor" && p.Role != "observer" && p.Role != "reviewer" {
		writeJSON(w, 422, map[string]string{"error": "Bu işlem yalnız katkıcı, gözlemci veya inceleyen ekler."})
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
		allowed, err := deskCanTaskAction(ctx, tx, d, id, "assign_task")
		if err != nil {
			return 0, nil, err
		}
		if !allowed {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM actors WHERE id=$1 AND workspace_id=$2 AND type='internal' AND status='active')`, p.ActorID, d.WorkspaceID).Scan(&exists)
		if err != nil {
			return 0, nil, err
		}
		if !exists {
			return 0, nil, deskError{422, "invalid_actor", ""}
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_assignments WHERE task_id=$1 AND actor_id=$2 AND role=$3 AND status='active')`, id, p.ActorID, p.Role).Scan(&exists)
		if err != nil {
			return 0, nil, err
		}
		if exists {
			return 0, nil, deskError{409, "assignment_exists", ""}
		}
		var assignmentID string
		err = tx.QueryRow(ctx, `INSERT INTO task_assignments(task_id,workspace_id,actor_id,role,assigned_by) VALUES($1,$2,$3,$4,$5) RETURNING id`, id, d.WorkspaceID, p.ActorID, p.Role, d.ActorID).Scan(&assignmentID)
		if err != nil {
			return 0, nil, err
		}
		err = deskEvent(ctx, tx, d, id, "task.participant_added", map[string]string{"assignment_id": assignmentID, "role": p.Role})
		return 201, map[string]string{"id": assignmentID}, err
	})
}

func (h *handler) deskEndAssignment(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason string `json:"reason"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz işlem."})
		return
	}
	p.Reason = strings.TrimSpace(p.Reason)
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		id := r.PathValue("id")
		var exists bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND workspace_id=$2)`, id, d.WorkspaceID).Scan(&exists)
		if err != nil {
			return 0, nil, err
		}
		if !exists {
			return 0, nil, deskError{404, "task_not_found", ""}
		}
		allowed, err := deskCanTaskAction(ctx, tx, d, id, "manage_task_participants")
		if err != nil {
			return 0, nil, err
		}
		if !allowed {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		var assignmentID string
		err = tx.QueryRow(ctx, `UPDATE task_assignments SET status='ended',ended_at=now(),end_reason=$4 WHERE id=$1 AND task_id=$2 AND workspace_id=$3 AND role IN ('contributor','observer','reviewer') AND status='active' RETURNING id`, r.PathValue("assignment_id"), id, d.WorkspaceID, p.Reason).Scan(&assignmentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, deskError{404, "assignment_not_found", ""}
		}
		if err != nil {
			return 0, nil, err
		}
		err = deskEvent(ctx, tx, d, id, "task.participant_ended", map[string]string{"assignment_id": assignmentID})
		return 200, map[string]string{"id": assignmentID, "status": "ended"}, err
	})
}
