package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapDesk connects the existing protected operator to the canonical V0.1 model.
func BootstrapDesk(ctx context.Context, pool *pgxpool.Pool, username, password string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(21001)`); err != nil {
		return err
	}
	var workspaceID, actorID, accountID string
	if err = tx.QueryRow(ctx, `INSERT INTO workspaces(name,slug) VALUES ('ent-challange','ent-challange') ON CONFLICT(slug) DO UPDATE SET name=EXCLUDED.name RETURNING id`).Scan(&workspaceID); err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT id,actor_id FROM accounts WHERE workspace_id=$1 AND email=$2 AND status='active'`, workspaceID, username).Scan(&accountID, &actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.QueryRow(ctx, `INSERT INTO actors(workspace_id,type,display_name) VALUES($1,'internal','Yönetici') RETURNING id`, workspaceID).Scan(&actorID); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `INSERT INTO accounts(workspace_id,email,actor_id) VALUES($1,$2,$3) RETURNING id`, workspaceID, username, actorID).Scan(&accountID); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = hashAdminPassword(ctx, tx, accountID, password); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO role_bindings(workspace_id,actor_id,role,granted_by) SELECT $1,$2,'workspace_admin',$2 WHERE NOT EXISTS (SELECT 1 FROM role_bindings WHERE workspace_id=$1 AND actor_id=$2 AND role='workspace_admin' AND status='active')`, workspaceID, actorID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type deskIdentity struct {
	WorkspaceID string
	ActorID     string
	SessionID   string
	Admin       bool
}
type deskError struct {
	Status              int
	Code                string
	CurrentAssignmentID string
}

func (e deskError) Error() string { return e.Code }

func (h *handler) deskIdentity(ctx context.Context) (deskIdentity, error) {
	d, ok := ctx.Value(deskIdentityKey{}).(deskIdentity)
	if !ok {
		return deskIdentity{}, deskError{401, "login_required", ""}
	}
	return d, nil
}

func (h *handler) registerDeskRoutes(mux *http.ServeMux) {
	if h.deskPool == nil {
		return
	}
	mux.HandleFunc("POST /api/v1/auth/login", h.authLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.authRefresh)
	mux.Handle("POST /api/v1/auth/logout", h.requireDesk(http.HandlerFunc(h.authLogout)))
	for _, route := range []struct {
		pattern string
		fn      http.HandlerFunc
	}{
		{"GET /api/v1/desk/context", h.deskContext},
		{"GET /api/v1/my-dashboard", h.deskDashboard},
		{"GET /api/v1/tasks/{id}", h.deskTaskDetail},
		{"POST /api/v1/actors", h.deskCreateActor},
		{"POST /api/v1/permission-grants", h.deskCreateGrant},
		{"GET /api/v1/permission-grants", h.deskListGrants},
		{"POST /api/v1/permission-grants/{id}/revoke", h.deskRevokeGrant},
		{"POST /api/v1/projects", h.deskCreateProject},
		{"POST /api/v1/tasks", h.deskCreateTask},
		{"POST /api/v1/tasks/{id}/checklist", h.deskAddChecklist},
		{"PATCH /api/v1/tasks/{id}/checklist/{item_id}", h.deskToggleChecklist},
		{"POST /api/v1/tasks/{id}/assignments", h.deskAddAssignment},
		{"POST /api/v1/tasks/{id}/assignments/{assignment_id}/end", h.deskEndAssignment},
		{"POST /api/v1/tasks/{id}/ready", h.deskTransition},
		{"POST /api/v1/tasks/{id}/start", h.deskTransition},
		{"POST /api/v1/tasks/{id}/submit", h.deskTransition},
		{"POST /api/v1/tasks/{id}/cancel", h.deskTransition},
		{"POST /api/v1/tasks/{id}/blockers", h.deskTransition},
		{"POST /api/v1/tasks/{id}/blockers/{blocker_id}/resolve", h.deskTransition},
		{"POST /api/v1/submissions/{id}/review", h.deskReview},
		{"POST /api/v1/tasks/{id}/reassign", h.deskReassign},
	} {
		mux.Handle(route.pattern, h.requireDesk(route.fn))
	}
}

func deskDecode(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("fazla JSON verisi")
	}
	return nil
}

func deskWriteError(w http.ResponseWriter, err error) {
	var de deskError
	if errors.As(err, &de) {
		body := map[string]string{"error": de.Code}
		if de.CurrentAssignmentID != "" {
			body["current_assignment_id"] = de.CurrentAssignmentID
		}
		writeJSON(w, de.Status, body)
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			writeJSON(w, 409, map[string]string{"error": "Bu kayıt zaten var."})
			return
		case "23503", "22P02":
			writeJSON(w, 422, map[string]string{"error": "Geçersiz kayıt bağlantısı."})
			return
		}
	}
	slog.Error("desk request failed", "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "İşlem tamamlanamadı."})
}

// Each business mutation and its replay response are committed in one transaction.
func (h *handler) deskMutate(w http.ResponseWriter, r *http.Request, payload any, run func(context.Context, pgx.Tx, deskIdentity) (int, any, error)) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 255 {
		writeJSON(w, 400, map[string]string{"error": "Idempotency-Key gerekli."})
		return
	}
	d, err := h.deskIdentity(r.Context())
	if err != nil {
		deskWriteError(w, err)
		return
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	tx, err := h.deskPool.Begin(r.Context())
	if err != nil {
		deskWriteError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var inserted string
	err = tx.QueryRow(r.Context(), `INSERT INTO idempotency_keys(workspace_id,actor_id,endpoint,key,request_hash) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING key`, d.WorkspaceID, d.ActorID, r.URL.Path, key, hash).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var oldHash string
		var status int
		var body []byte
		err = tx.QueryRow(r.Context(), `SELECT request_hash,response_status,response_body FROM idempotency_keys WHERE workspace_id=$1 AND actor_id=$2 AND endpoint=$3 AND key=$4`, d.WorkspaceID, d.ActorID, r.URL.Path, key).Scan(&oldHash, &status, &body)
		if err != nil {
			deskWriteError(w, err)
			return
		}
		if oldHash != hash {
			writeJSON(w, 409, map[string]string{"error": "idempotency_conflict"})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return
	}
	if err != nil {
		deskWriteError(w, err)
		return
	}
	status, result, err := run(r.Context(), tx, d)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	body, err := json.Marshal(result)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE idempotency_keys SET response_status=$5,response_body=$6 WHERE workspace_id=$1 AND actor_id=$2 AND endpoint=$3 AND key=$4`, d.WorkspaceID, d.ActorID, r.URL.Path, key, status, body)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		deskWriteError(w, err)
		return
	}
	writeJSON(w, status, result)
}

func deskEvent(ctx context.Context, tx pgx.Tx, d deskIdentity, taskID, kind string, payload any) error {
	data, _ := json.Marshal(payload)
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(workspace_id,aggregate_type,aggregate_id,event_type,actor_id,payload) VALUES($1,'task',$2,$3,$4,$5)`, d.WorkspaceID, taskID, kind, d.ActorID, data); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(workspace_id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,'task',$2,$3,$4)`, d.WorkspaceID, taskID, kind, data)
	return err
}

func (h *handler) deskContext(w http.ResponseWriter, r *http.Request) {
	d, err := h.deskIdentity(r.Context())
	if err != nil {
		deskWriteError(w, err)
		return
	}
	var name string
	if err = h.deskPool.QueryRow(r.Context(), `SELECT name FROM workspaces WHERE id=$1`, d.WorkspaceID).Scan(&name); err != nil {
		deskWriteError(w, err)
		return
	}
	actors := []map[string]string{}
	rows, err := h.deskPool.Query(r.Context(), `SELECT id,display_name FROM actors WHERE workspace_id=$1 AND type='internal' AND status='active' ORDER BY display_name`, d.WorkspaceID)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	for rows.Next() {
		var id, label string
		if err = rows.Scan(&id, &label); err != nil {
			break
		}
		actors = append(actors, map[string]string{"id": id, "name": label})
	}
	rows.Close()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	projects := []map[string]string{}
	rows, err = h.deskPool.Query(r.Context(), `SELECT id,name FROM projects WHERE workspace_id=$1 AND status='active' ORDER BY created_at DESC`, d.WorkspaceID)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	for rows.Next() {
		var id, label string
		if err = rows.Scan(&id, &label); err != nil {
			break
		}
		projects = append(projects, map[string]string{"id": id, "name": label})
	}
	rows.Close()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"workspace": map[string]string{"id": d.WorkspaceID, "name": name}, "me": map[string]any{"id": d.ActorID, "admin": d.Admin}, "actors": actors, "projects": projects})
}

func (h *handler) deskCreateActor(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz kişi."})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if len(p.Name) < 2 || len(p.Name) > 255 {
		writeJSON(w, 422, map[string]string{"error": "Kişi adı 2–255 karakter olmalı."})
		return
	}
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))
	if (p.Email == "") != (p.Password == "") || p.Email != "" && (!validEmail(p.Email) || len(p.Password) < 12) {
		writeJSON(w, 422, map[string]string{"error": "Geçerli e-posta ve en az 12 karakterli şifre girin."})
		return
	}
	var hash string
	if p.Password != "" {
		var err error
		hash, err = passwordHash(p.Password)
		if err != nil {
			deskWriteError(w, err)
			return
		}
	}
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		if !d.Admin {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO actors(workspace_id,type,display_name) VALUES($1,'internal',$2) RETURNING id`, d.WorkspaceID, p.Name).Scan(&id)
		if err == nil && p.Email != "" {
			_, err = tx.Exec(ctx, `INSERT INTO accounts(workspace_id,email,password_hash,actor_id) VALUES($1,$2,$3,$4)`, d.WorkspaceID, p.Email, hash, id)
		}
		return 201, map[string]string{"id": id, "name": p.Name}, err
	})
}

func (h *handler) deskCreateProject(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz proje."})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if len(p.Name) < 2 || len(p.Name) > 255 {
		writeJSON(w, 422, map[string]string{"error": "Proje adı 2–255 karakter olmalı."})
		return
	}
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		if !d.Admin {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO projects(workspace_id,name,description,created_by) VALUES($1,$2,$3,$4) RETURNING id`, d.WorkspaceID, p.Name, p.Description, d.ActorID).Scan(&id)
		return 201, map[string]string{"id": id, "name": p.Name}, err
	})
}

type deskCreateTaskPayload struct {
	ProjectID          string     `json:"project_id"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	Priority           string     `json:"priority"`
	DueDate            *time.Time `json:"due_date"`
	AssigneeID         string     `json:"assignee_id"`
	ReviewerID         string     `json:"reviewer_id"`
	Purpose            string     `json:"purpose"`
	DeliveryFormat     string     `json:"delivery_format"`
	AcceptanceCriteria []struct {
		Description          string `json:"description"`
		RequiredEvidenceType string `json:"required_evidence_type"`
	} `json:"acceptance_criteria"`
}

func (h *handler) deskCreateTask(w http.ResponseWriter, r *http.Request) {
	var p deskCreateTaskPayload
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz görev."})
		return
	}
	p.Title = strings.TrimSpace(p.Title)
	if len(p.Title) < 3 || len(p.Title) > 500 || p.ProjectID == "" {
		writeJSON(w, 422, map[string]string{"error": "Proje ve görev başlığı gerekli."})
		return
	}
	if p.Priority == "" {
		p.Priority = "medium"
	}
	if p.Priority != "low" && p.Priority != "medium" && p.Priority != "high" {
		writeJSON(w, 422, map[string]string{"error": "Geçersiz öncelik."})
		return
	}
	if len(p.AcceptanceCriteria) > 20 {
		writeJSON(w, 422, map[string]string{"error": "En fazla 20 kabul ölçütü eklenebilir."})
		return
	}
	for _, criterion := range p.AcceptanceCriteria {
		if len(strings.TrimSpace(criterion.Description)) < 3 || len(criterion.Description) > 1000 {
			writeJSON(w, 422, map[string]string{"error": "Kabul ölçütü 3–1000 karakter olmalı."})
			return
		}
		if criterion.RequiredEvidenceType != "" && criterion.RequiredEvidenceType != "url" && criterion.RequiredEvidenceType != "file" && criterion.RequiredEvidenceType != "pr" && criterion.RequiredEvidenceType != "screenshot" {
			writeJSON(w, 422, map[string]string{"error": "Geçersiz kanıt türü."})
			return
		}
	}
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		if !d.Admin {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND workspace_id=$2 AND status='active')`, p.ProjectID, d.WorkspaceID).Scan(&exists); err != nil {
			return 0, nil, err
		}
		if !exists {
			return 0, nil, deskError{404, "project_not_found", ""}
		}
		assignee := p.AssigneeID
		if assignee == "" {
			assignee = d.ActorID
		}
		for _, actor := range []string{assignee, p.ReviewerID} {
			if actor == "" {
				continue
			}
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM actors WHERE id=$1 AND workspace_id=$2 AND type='internal' AND status='active')`, actor, d.WorkspaceID).Scan(&exists); err != nil {
				return 0, nil, err
			}
			if !exists {
				return 0, nil, deskError{422, "invalid_actor", ""}
			}
		}
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO tasks(workspace_id,project_id,title,description,priority,due_date,created_by) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, d.WorkspaceID, p.ProjectID, p.Title, p.Description, p.Priority, p.DueDate, d.ActorID).Scan(&id)
		if err != nil {
			return 0, nil, err
		}
		for _, a := range []struct{ id, role string }{{d.ActorID, "owner"}, {assignee, "primary_assignee"}, {p.ReviewerID, "reviewer"}} {
			if a.id == "" {
				continue
			}
			_, err = tx.Exec(ctx, `INSERT INTO task_assignments(task_id,workspace_id,actor_id,role,assigned_by) VALUES($1,$2,$3,$4,$5)`, id, d.WorkspaceID, a.id, a.role, d.ActorID)
			if err != nil {
				return 0, nil, err
			}
		}
		var packageID string
		err = tx.QueryRow(ctx, `INSERT INTO task_packages(task_id,purpose,delivery_format,review_required) VALUES($1,$2,$3,$4) RETURNING id`, id, p.Purpose, p.DeliveryFormat, p.ReviewerID != "").Scan(&packageID)
		if err != nil {
			return 0, nil, err
		}
		for _, criterion := range p.AcceptanceCriteria {
			var evidenceType any
			if criterion.RequiredEvidenceType != "" {
				evidenceType = criterion.RequiredEvidenceType
			}
			_, err = tx.Exec(ctx, `INSERT INTO acceptance_criteria(task_package_id,description,required_evidence_type) VALUES($1,$2,$3)`, packageID, strings.TrimSpace(criterion.Description), evidenceType)
			if err != nil {
				return 0, nil, err
			}
		}
		err = deskEvent(ctx, tx, d, id, "task.created", map[string]any{"title": p.Title, "assignee_id": assignee})
		return 201, map[string]string{"id": id}, err
	})
}

type deskTaskItem struct {
	ID                  string     `json:"id"`
	Project             string     `json:"project"`
	Title               string     `json:"title"`
	Description         string     `json:"description"`
	Priority            string     `json:"priority"`
	OpState             string     `json:"op_state"`
	AccState            string     `json:"acc_state"`
	DueDate             *time.Time `json:"due_date"`
	Assignee            string     `json:"assignee"`
	PrimaryAssignmentID string     `json:"primary_assignment_id"`
	AllowedActions      []string   `json:"allowed_actions"`
}

func deskAllowed(op, acc string, admin, owner, assigned, reviewer, canReassign bool) []string {
	a := []string{}
	switch op {
	case "planned":
		if admin || owner {
			a = append(a, "ready")
		}
	case "ready":
		if admin || assigned {
			a = append(a, "start")
		}
	case "in_progress":
		if admin || assigned {
			a = append(a, "submit")
		}
		if admin || owner {
			a = append(a, "block")
		}
	case "blocked":
		if admin || owner {
			a = append(a, "resolve", "block")
		}
	}
	if acc == "under_review" && (admin || reviewer) {
		a = append(a, "accept", "request_revision")
	}
	if op != "closed" && op != "cancelled" {
		if admin || owner || canReassign {
			a = append(a, "reassign")
		}
		if admin || owner {
			a = append(a, "cancel")
		}
	}
	return a
}

func deskHasTaskRole(ctx context.Context, tx pgx.Tx, d deskIdentity, taskID, role string) (bool, error) {
	if d.Admin {
		return true, nil
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_assignments WHERE task_id=$1 AND workspace_id=$2 AND actor_id=$3 AND role=$4 AND status='active')`, taskID, d.WorkspaceID, d.ActorID, role).Scan(&allowed)
	return allowed, err
}

func (h *handler) deskDashboard(w http.ResponseWriter, r *http.Request) {
	d, err := h.deskIdentity(r.Context())
	if err != nil {
		deskWriteError(w, err)
		return
	}
	rows, err := h.deskPool.Query(r.Context(), `SELECT t.id,p.name,t.title,coalesce(t.description,''),t.priority,t.op_state::text,t.acc_state::text,t.due_date,coalesce(pa.id::text,''),coalesce(act.display_name,''),
		EXISTS(SELECT 1 FROM task_assignments a WHERE a.task_id=t.id AND a.actor_id=$2 AND a.role='owner' AND a.status='active'),
		EXISTS(SELECT 1 FROM task_assignments a WHERE a.task_id=t.id AND a.actor_id=$2 AND a.role='primary_assignee' AND a.status='active'),
		EXISTS(SELECT 1 FROM task_assignments a WHERE a.task_id=t.id AND a.actor_id=$2 AND a.role='reviewer' AND a.status='active'),
		EXISTS(SELECT 1 FROM task_assignments a WHERE a.task_id=t.id AND a.actor_id=$2 AND a.role='contributor' AND a.status='active'),
		coalesce(pa.actor_id::text,''),
		EXISTS(SELECT 1 FROM permission_grants g WHERE g.workspace_id=t.workspace_id AND g.actor_id=$2 AND g.action='reassign_task' AND g.status='active' AND g.valid_from<=now() AND (g.valid_until IS NULL OR g.valid_until>now()) AND (g.scope_type='workspace' OR g.scope_type='project' AND g.scope_id=t.project_id OR g.scope_type='task' AND g.scope_id=t.id)) OR
		EXISTS(SELECT 1 FROM role_bindings rb WHERE rb.workspace_id=t.workspace_id AND rb.actor_id=$2 AND rb.status='active' AND rb.role='project_manager' AND (rb.scope_type='workspace' OR rb.scope_type='project' AND rb.scope_id=t.project_id))
		FROM tasks t JOIN projects p ON p.id=t.project_id
		LEFT JOIN task_assignments pa ON pa.task_id=t.id AND pa.role='primary_assignee' AND pa.status='active'
		LEFT JOIN actors act ON act.id=pa.actor_id
		WHERE t.workspace_id=$1 AND EXISTS(SELECT 1 FROM task_assignments mine WHERE mine.task_id=t.id AND mine.actor_id=$2 AND mine.status='active')
		ORDER BY t.due_date NULLS LAST,t.created_at DESC`, d.WorkspaceID, d.ActorID)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	defer rows.Close()
	groups := map[string][]deskTaskItem{}
	for _, k := range []string{"assigned", "owned", "reviewing", "contributing", "overdue", "blocked", "due_today", "waiting_on_others"} {
		groups[k] = []deskTaskItem{}
	}
	loc, _ := time.LoadLocation("Europe/Istanbul")
	now := time.Now()
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	for rows.Next() {
		var item deskTaskItem
		var owner, assigned, reviewer, contributor, canReassign bool
		var assigneeID string
		if err = rows.Scan(&item.ID, &item.Project, &item.Title, &item.Description, &item.Priority, &item.OpState, &item.AccState, &item.DueDate, &item.PrimaryAssignmentID, &item.Assignee, &owner, &assigned, &reviewer, &contributor, &assigneeID, &canReassign); err != nil {
			deskWriteError(w, err)
			return
		}
		item.AllowedActions = deskAllowed(item.OpState, item.AccState, d.Admin, owner, assigned, reviewer, canReassign)
		active := item.OpState != "closed" && item.OpState != "cancelled"
		if assigned && (item.OpState == "ready" || item.OpState == "in_progress" || item.OpState == "blocked") {
			groups["assigned"] = append(groups["assigned"], item)
		}
		if owner && active {
			groups["owned"] = append(groups["owned"], item)
		}
		if reviewer && item.AccState == "under_review" {
			groups["reviewing"] = append(groups["reviewing"], item)
		}
		if contributor && active {
			groups["contributing"] = append(groups["contributing"], item)
		}
		if owner && active && assigneeID != "" && assigneeID != d.ActorID {
			groups["waiting_on_others"] = append(groups["waiting_on_others"], item)
		}
		if active && item.DueDate != nil {
			if item.DueDate.Before(now) {
				groups["overdue"] = append(groups["overdue"], item)
			}
			if !item.DueDate.Before(start) && item.DueDate.Before(end) {
				groups["due_today"] = append(groups["due_today"], item)
			}
		}
		if item.OpState == "blocked" {
			groups["blocked"] = append(groups["blocked"], item)
		}
	}
	if rows.Err() != nil {
		deskWriteError(w, rows.Err())
		return
	}
	writeJSON(w, 200, groups)
}

func (h *handler) deskTaskDetail(w http.ResponseWriter, r *http.Request) {
	d, err := h.deskIdentity(r.Context())
	if err != nil {
		deskWriteError(w, err)
		return
	}
	var item deskTaskItem
	err = h.deskPool.QueryRow(r.Context(), `SELECT t.id,p.name,t.title,coalesce(t.description,''),t.priority,t.op_state::text,t.acc_state::text,t.due_date FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=$1 AND t.workspace_id=$2`, r.PathValue("id"), d.WorkspaceID).Scan(&item.ID, &item.Project, &item.Title, &item.Description, &item.Priority, &item.OpState, &item.AccState, &item.DueDate)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "Görev bulunamadı."})
		return
	}
	if err != nil {
		deskWriteError(w, err)
		return
	}
	var member bool
	if err = h.deskPool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM task_assignments WHERE task_id=$1 AND actor_id=$2 AND status='active')`, item.ID, d.ActorID).Scan(&member); err != nil {
		deskWriteError(w, err)
		return
	}
	if !member && !d.Admin {
		writeJSON(w, 404, map[string]string{"error": "Görev bulunamadı."})
		return
	}
	var owner, assigned, reviewer bool
	assignments := []map[string]string{}
	rows, err := h.deskPool.Query(r.Context(), `SELECT ta.id,ta.role::text,a.display_name,ta.actor_id FROM task_assignments ta JOIN actors a ON a.id=ta.actor_id WHERE ta.task_id=$1 AND ta.workspace_id=$2 AND ta.status='active' ORDER BY ta.assigned_at`, item.ID, d.WorkspaceID)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	for rows.Next() {
		var id, role, name, actor string
		if err = rows.Scan(&id, &role, &name, &actor); err != nil {
			break
		}
		assignments = append(assignments, map[string]string{"id": id, "role": role, "name": name, "actor_id": actor})
		if actor == d.ActorID {
			switch role {
			case "owner":
				owner = true
			case "primary_assignee":
				assigned = true
			case "reviewer":
				reviewer = true
			}
		}
		if role == "primary_assignee" {
			item.PrimaryAssignmentID = id
			item.Assignee = name
		}
	}
	rows.Close()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	canReassign, err := deskCanTaskAction(r.Context(), h.deskPool, d, item.ID, "reassign_task")
	if err != nil {
		deskWriteError(w, err)
		return
	}
	item.AllowedActions = deskAllowed(item.OpState, item.AccState, d.Admin, owner, assigned, reviewer, canReassign)
	blockers := []map[string]any{}
	rows, err = h.deskPool.Query(r.Context(), `SELECT id,coalesce(reason,''),resolved_at FROM blockers WHERE task_id=$1 ORDER BY created_at DESC`, item.ID)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	for rows.Next() {
		var id, reason string
		var resolved *time.Time
		if err = rows.Scan(&id, &reason, &resolved); err != nil {
			break
		}
		blockers = append(blockers, map[string]any{"id": id, "reason": reason, "resolved": resolved != nil})
	}
	rows.Close()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	criteria := []map[string]string{}
	rows, err = h.deskPool.Query(r.Context(), `SELECT ac.id,ac.description,coalesce(ac.required_evidence_type,'') FROM acceptance_criteria ac JOIN task_packages tp ON tp.id=ac.task_package_id WHERE tp.task_id=$1 ORDER BY ac.id`, item.ID)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	for rows.Next() {
		var id, description, evidenceType string
		if err = rows.Scan(&id, &description, &evidenceType); err != nil {
			break
		}
		criteria = append(criteria, map[string]string{"id": id, "description": description, "required_evidence_type": evidenceType})
	}
	rows.Close()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	checklist := []map[string]any{}
	rows, err = h.deskPool.Query(r.Context(), `SELECT id,title,is_done FROM checklist_items WHERE task_id=$1 ORDER BY position,created_at`, item.ID)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	for rows.Next() {
		var id, title string
		var done bool
		if err = rows.Scan(&id, &title, &done); err != nil {
			break
		}
		checklist = append(checklist, map[string]any{"id": id, "title": title, "is_done": done})
	}
	rows.Close()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	submissions := []map[string]any{}
	rows, err = h.deskPool.Query(r.Context(), `SELECT s.id,s.iteration_number,coalesce(s.reviewer_notes,''),s.created_at,(SELECT sr.decision FROM submission_reviews sr WHERE sr.submission_id=s.id ORDER BY sr.created_at DESC LIMIT 1) FROM submissions s WHERE s.task_id=$1 ORDER BY s.iteration_number DESC`, item.ID)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	submissionIndex := map[string]int{}
	for rows.Next() {
		var id, note string
		var iteration int
		var created time.Time
		var decision *string
		if err = rows.Scan(&id, &iteration, &note, &created, &decision); err != nil {
			break
		}
		submissionIndex[id] = len(submissions)
		submissions = append(submissions, map[string]any{"id": id, "iteration": iteration, "note": note, "created_at": created, "decision": decision, "evidences": []map[string]string{}})
	}
	rows.Close()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	rows, err = h.deskPool.Query(r.Context(), `SELECT e.submission_id,coalesce(e.criterion_id::text,''),coalesce(e.resource_url,'') FROM evidences e JOIN submissions s ON s.id=e.submission_id WHERE s.task_id=$1 ORDER BY e.created_at`, item.ID)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	for rows.Next() {
		var submissionID, criterionID, url string
		if err = rows.Scan(&submissionID, &criterionID, &url); err != nil {
			break
		}
		i, ok := submissionIndex[submissionID]
		if ok {
			evidences := submissions[i]["evidences"].([]map[string]string)
			submissions[i]["evidences"] = append(evidences, map[string]string{"criterion_id": criterionID, "resource_url": url})
		}
	}
	rows.Close()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	canAdd, err := deskCanTaskAction(r.Context(), h.deskPool, d, item.ID, "assign_task")
	if err != nil {
		deskWriteError(w, err)
		return
	}
	canEnd, err := deskCanTaskAction(r.Context(), h.deskPool, d, item.ID, "manage_task_participants")
	if err != nil {
		deskWriteError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"task": item, "assignments": assignments, "blockers": blockers, "submissions": submissions, "criteria": criteria, "checklist": checklist, "can_add_checklist": d.Admin || owner, "can_check": d.Admin || owner || assigned, "can_add_assignment": canAdd, "can_end_assignment": canEnd})
}

func (h *handler) deskTransition(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason    string `json:"reason"`
		Note      string `json:"note"`
		Evidences []struct {
			CriterionID string `json:"criterion_id"`
			ResourceURL string `json:"resource_url"`
		} `json:"evidences"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz işlem."})
		return
	}
	if len(p.Evidences) > 20 {
		writeJSON(w, 422, map[string]string{"error": "En fazla 20 kanıt eklenebilir."})
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	if strings.Contains(action, "/blockers/") {
		action = "resolve"
	} else {
		parts := strings.Split(action, "/")
		action = parts[len(parts)-1]
	}
	if action == "blockers" {
		action = "block"
	}
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		id := r.PathValue("id")
		var op, acc string
		err := tx.QueryRow(ctx, `SELECT op_state::text,acc_state::text FROM tasks WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, id, d.WorkspaceID).Scan(&op, &acc)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, deskError{404, "task_not_found", ""}
		}
		if err != nil {
			return 0, nil, err
		}
		requiredRole := "owner"
		if action == "start" || action == "submit" {
			requiredRole = "primary_assignee"
		}
		allowed, err := deskHasTaskRole(ctx, tx, d, id, requiredRole)
		if err != nil {
			return 0, nil, err
		}
		if !allowed {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		newOp, newAcc := op, acc
		event := "task." + action
		switch action {
		case "cancel":
			if op == "closed" || op == "cancelled" {
				return 0, nil, deskError{409, "invalid_transition", ""}
			}
			newOp = "cancelled"
		case "ready":
			if op != "planned" {
				return 0, nil, deskError{409, "invalid_transition", ""}
			}
			newOp = "ready"
		case "start":
			if op != "ready" {
				return 0, nil, deskError{409, "invalid_transition", ""}
			}
			newOp = "in_progress"
		case "block":
			if (op != "in_progress" && op != "blocked") || strings.TrimSpace(p.Reason) == "" {
				return 0, nil, deskError{422, "blocker_reason_required", ""}
			}
			newOp = "blocked"
			_, err = tx.Exec(ctx, `INSERT INTO blockers(task_id,block_type,blocked_by_actor_id,reason) VALUES($1,'manual',$2,$3)`, id, d.ActorID, p.Reason)
		case "resolve":
			if op != "blocked" {
				return 0, nil, deskError{409, "invalid_transition", ""}
			}
			var blockerID string
			err = tx.QueryRow(ctx, `UPDATE blockers SET resolved_at=now() WHERE id=$1 AND task_id=$2 AND resolved_at IS NULL RETURNING id`, r.PathValue("blocker_id"), id).Scan(&blockerID)
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, nil, deskError{409, "blocker_already_resolved", ""}
			}
			if err != nil {
				return 0, nil, err
			}
			var count int
			err = tx.QueryRow(ctx, `SELECT count(*) FROM blockers WHERE task_id=$1 AND resolved_at IS NULL`, id).Scan(&count)
			if count == 0 {
				newOp = "in_progress"
			}
		case "submit":
			if op != "in_progress" || acc != "not_submitted" && acc != "revision_needed" {
				return 0, nil, deskError{409, "invalid_transition", ""}
			}
			var iteration int
			err = tx.QueryRow(ctx, `SELECT coalesce(max(iteration_number),0)+1 FROM submissions WHERE task_id=$1`, id).Scan(&iteration)
			if err != nil {
				return 0, nil, err
			}
			var submissionID string
			err = tx.QueryRow(ctx, `INSERT INTO submissions(task_id,submitted_by,iteration_number,reviewer_notes) VALUES($1,$2,$3,$4) RETURNING id`, id, d.ActorID, iteration, p.Note).Scan(&submissionID)
			if err != nil {
				return 0, nil, err
			}
			for _, evidence := range p.Evidences {
				parsed, parseErr := url.Parse(evidence.ResourceURL)
				if parseErr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
					return 0, nil, deskError{422, "invalid_evidence_url", ""}
				}
				var criterionID any
				if evidence.CriterionID != "" {
					var exists bool
					err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM acceptance_criteria ac JOIN task_packages tp ON tp.id=ac.task_package_id WHERE ac.id=$1 AND tp.task_id=$2)`, evidence.CriterionID, id).Scan(&exists)
					if err != nil {
						return 0, nil, err
					}
					if !exists {
						return 0, nil, deskError{422, "invalid_criterion", ""}
					}
					criterionID = evidence.CriterionID
				}
				_, err = tx.Exec(ctx, `INSERT INTO evidences(submission_id,criterion_id,resource_url) VALUES($1,$2,$3)`, submissionID, criterionID, evidence.ResourceURL)
				if err != nil {
					return 0, nil, err
				}
			}
			var missingRequired bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM acceptance_criteria ac JOIN task_packages tp ON tp.id=ac.task_package_id WHERE tp.task_id=$1 AND ac.required_evidence_type IS NOT NULL AND NOT EXISTS(SELECT 1 FROM evidences e WHERE e.submission_id=$2 AND e.criterion_id=ac.id))`, id, submissionID).Scan(&missingRequired)
			if err != nil {
				return 0, nil, err
			}
			if missingRequired {
				return 0, nil, deskError{422, "required_evidence_missing", ""}
			}
			var reviewRequired bool
			err = tx.QueryRow(ctx, `SELECT review_required FROM task_packages WHERE task_id=$1`, id).Scan(&reviewRequired)
			if err != nil {
				return 0, nil, err
			}
			if reviewRequired {
				newOp = "work_completed"
				newAcc = "under_review"
				_, err = tx.Exec(ctx, `INSERT INTO notifications(workspace_id,actor_id,type,payload) SELECT $1,actor_id,'submission.ready',jsonb_build_object('task_id',$2::text) FROM task_assignments WHERE task_id=$2::uuid AND role='reviewer' AND status='active'`, d.WorkspaceID, id)
			} else {
				newOp = "closed"
				newAcc = "not_required"
			}
		default:
			return 0, nil, deskError{404, "unknown_action", ""}
		}
		if err != nil {
			return 0, nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE tasks SET op_state=$2,acc_state=$3,updated_at=now() WHERE id=$1`, id, newOp, newAcc)
		if err != nil {
			return 0, nil, err
		}
		if err = deskEvent(ctx, tx, d, id, event, map[string]any{"op_state": newOp, "acc_state": newAcc}); err != nil {
			return 0, nil, err
		}
		return 200, map[string]string{"id": id, "op_state": newOp, "acc_state": newAcc}, nil
	})
}

func (h *handler) deskReview(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Action            string   `json:"action"`
		Comments          string   `json:"comments"`
		FailedCriteriaIDs []string `json:"failed_criteria_ids"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz karar."})
		return
	}
	if p.Action != "accept" && p.Action != "request_revision" {
		writeJSON(w, 422, map[string]string{"error": "Geçersiz karar."})
		return
	}
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		var taskID, acc, fin string
		err := tx.QueryRow(ctx, `SELECT t.id,t.acc_state::text,t.fin_state::text FROM submissions s JOIN tasks t ON t.id=s.task_id WHERE s.id=$1 AND t.workspace_id=$2 AND s.iteration_number=(SELECT max(iteration_number) FROM submissions WHERE task_id=t.id) FOR UPDATE OF t`, r.PathValue("id"), d.WorkspaceID).Scan(&taskID, &acc, &fin)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, deskError{404, "submission_not_found", ""}
		}
		if err != nil {
			return 0, nil, err
		}
		if acc != "under_review" {
			return 0, nil, deskError{409, "invalid_transition", ""}
		}
		allowed, err := deskHasTaskRole(ctx, tx, d, taskID, "reviewer")
		if err != nil {
			return 0, nil, err
		}
		if !allowed {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		if p.Action == "accept" && len(p.FailedCriteriaIDs) > 0 {
			return 0, nil, deskError{422, "failed_criteria_not_allowed", ""}
		}
		for _, criterionID := range p.FailedCriteriaIDs {
			var exists bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM acceptance_criteria ac JOIN task_packages tp ON tp.id=ac.task_package_id WHERE ac.id=$1 AND tp.task_id=$2)`, criterionID, taskID).Scan(&exists)
			if err != nil {
				return 0, nil, err
			}
			if !exists {
				return 0, nil, deskError{422, "invalid_criterion", ""}
			}
		}
		decision := "accepted"
		op := "work_completed"
		if fin == "not_applicable" || fin == "paid" {
			op = "closed"
		}
		if p.Action == "request_revision" {
			decision = "revision_needed"
			op = "in_progress"
		}
		_, err = tx.Exec(ctx, `INSERT INTO submission_reviews(submission_id,reviewed_by,decision,comments,failed_criteria_ids) VALUES($1,$2,$3,$4,$5)`, r.PathValue("id"), d.ActorID, decision, p.Comments, p.FailedCriteriaIDs)
		if err != nil {
			return 0, nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE tasks SET acc_state=$2,op_state=$3,updated_at=now() WHERE id=$1`, taskID, decision, op)
		if err != nil {
			return 0, nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO notifications(workspace_id,actor_id,type,payload) SELECT $1,actor_id,'submission.reviewed',jsonb_build_object('task_id',$2::text,'decision',$3::text) FROM task_assignments WHERE task_id=$2::uuid AND role='primary_assignee' AND status='active'`, d.WorkspaceID, taskID, decision)
		if err != nil {
			return 0, nil, err
		}
		err = deskEvent(ctx, tx, d, taskID, "submission.reviewed", map[string]string{"decision": decision})
		return 200, map[string]string{"task_id": taskID, "decision": decision}, err
	})
}

func (h *handler) deskReassign(w http.ResponseWriter, r *http.Request) {
	var p struct {
		NewActorID                  string `json:"new_actor_id"`
		Reason                      string `json:"reason"`
		ExpectedPrimaryAssignmentID string `json:"expected_primary_assignment_id"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz atama."})
		return
	}
	if p.NewActorID == "" || p.ExpectedPrimaryAssignmentID == "" {
		writeJSON(w, 422, map[string]string{"error": "Kişi ve güncel atama gerekli."})
		return
	}
	h.deskMutate(w, r, p, func(ctx context.Context, tx pgx.Tx, d deskIdentity) (int, any, error) {
		id := r.PathValue("id")
		var op string
		err := tx.QueryRow(ctx, `SELECT op_state::text FROM tasks WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, id, d.WorkspaceID).Scan(&op)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, deskError{404, "task_not_found", ""}
		}
		if err != nil {
			return 0, nil, err
		}
		allowed, err := deskCanTaskAction(ctx, tx, d, id, "reassign_task")
		if err != nil {
			return 0, nil, err
		}
		if !allowed {
			return 0, nil, deskError{403, "forbidden", ""}
		}
		if op == "in_progress" && strings.TrimSpace(p.Reason) == "" {
			return 0, nil, deskError{400, "reason_required", ""}
		}
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM actors WHERE id=$1 AND workspace_id=$2 AND type='internal' AND status='active')`, p.NewActorID, d.WorkspaceID).Scan(&exists)
		if err != nil {
			return 0, nil, err
		}
		if !exists {
			return 0, nil, deskError{422, "invalid_actor", ""}
		}
		var current string
		err = tx.QueryRow(ctx, `SELECT id FROM task_assignments WHERE task_id=$1 AND role='primary_assignee' AND status='active'`, id).Scan(&current)
		if errors.Is(err, pgx.ErrNoRows) {
			current = ""
		} else if err != nil {
			return 0, nil, err
		}
		if current != p.ExpectedPrimaryAssignmentID {
			return 0, nil, deskError{409, "stale_assignment", current}
		}
		_, err = tx.Exec(ctx, `UPDATE task_assignments SET status='ended',ended_at=now(),end_reason=$2 WHERE id=$1`, current, p.Reason)
		if err != nil {
			return 0, nil, err
		}
		var newID string
		err = tx.QueryRow(ctx, `INSERT INTO task_assignments(task_id,workspace_id,actor_id,role,assigned_by) VALUES($1,$2,$3,'primary_assignee',$4) RETURNING id`, id, d.WorkspaceID, p.NewActorID, d.ActorID).Scan(&newID)
		if err != nil {
			return 0, nil, err
		}
		err = deskEvent(ctx, tx, d, id, "task.reassigned", map[string]string{"previous_assignment_id": current, "assignment_id": newID})
		return 200, map[string]string{"assignment_id": newID}, err
	})
}
