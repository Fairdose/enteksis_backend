package httpapi

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const passwordIterations = 600000

type deskIdentityKey struct{}

func passwordHash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return "", err
	}
	return "pbkdf2-sha256$" + strconv.Itoa(passwordIterations) + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(key), nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 100000 || iterations > 1000000 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil || len(want) != 32 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func newToken() (string, string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (h *handler) requireDesk(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("Authorization")
		var d deskIdentity
		if len(provided) == len(h.adminAuth) && subtle.ConstantTimeCompare([]byte(provided), []byte(h.adminAuth)) == 1 {
			err := h.deskPool.QueryRow(r.Context(), `SELECT workspace_id,actor_id FROM accounts WHERE email=$1 AND status='active' AND actor_id IS NOT NULL AND workspace_id=(SELECT id FROM workspaces WHERE slug='ent-challange')`, h.adminUsername).Scan(&d.WorkspaceID, &d.ActorID)
			if err != nil {
				deskWriteError(w, err)
				return
			}
			d.Admin = true
		} else if strings.HasPrefix(provided, "Bearer ") {
			var accountStatus, actorStatus, actorType string
			err := h.deskPool.QueryRow(r.Context(), `SELECT s.id,s.workspace_id,a.actor_id,a.status,act.status,act.type::text FROM auth_sessions s JOIN accounts a ON a.id=s.account_id AND a.workspace_id=s.workspace_id JOIN actors act ON act.id=a.actor_id AND act.workspace_id=s.workspace_id WHERE s.access_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now()`, tokenHash(strings.TrimPrefix(provided, "Bearer "))).Scan(&d.SessionID, &d.WorkspaceID, &d.ActorID, &accountStatus, &actorStatus, &actorType)
			if err != nil || accountStatus != "active" || actorStatus != "active" || actorType != "internal" {
				writeJSON(w, 401, map[string]string{"error": "Oturum geçersiz veya süresi dolmuş."})
				return
			}
			_ = h.deskPool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM role_bindings WHERE workspace_id=$1 AND actor_id=$2 AND role='workspace_admin' AND status='active')`, d.WorkspaceID, d.ActorID).Scan(&d.Admin)
		} else {
			writeJSON(w, 401, map[string]string{"error": "Giriş gerekli."})
			return
		}
		ctx := context.WithValue(r.Context(), deskIdentityKey{}, d)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *handler) authLogin(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if deskDecode(w, r, &p) != nil {
		writeJSON(w, 400, map[string]string{"error": "Geçersiz giriş bilgisi."})
		return
	}
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))
	var accountID, workspaceID, actorID, hash, status, actorStatus, actorType string
	err := h.deskPool.QueryRow(r.Context(), `SELECT a.id,a.workspace_id,a.actor_id,coalesce(a.password_hash,''),a.status,act.status,act.type::text FROM accounts a JOIN actors act ON act.id=a.actor_id AND act.workspace_id=a.workspace_id WHERE a.email=$1 AND a.workspace_id=(SELECT id FROM workspaces WHERE slug='ent-challange')`, p.Email).Scan(&accountID, &workspaceID, &actorID, &hash, &status, &actorStatus, &actorType)
	if err != nil || status != "active" || actorStatus != "active" || actorType != "internal" || !verifyPassword(p.Password, hash) {
		writeJSON(w, 401, map[string]string{"error": "E-posta veya şifre hatalı."})
		return
	}
	access, accessHash, err := newToken()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	refresh, refreshHash, err := newToken()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	_, err = h.deskPool.Exec(r.Context(), `INSERT INTO auth_sessions(workspace_id,account_id,access_hash,refresh_hash,expires_at,refresh_expires_at) VALUES($1,$2,$3,$4,now()+interval '8 hours',now()+interval '30 days')`, workspaceID, accountID, accessHash, refreshHash)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 28800, "actor_id": actorID})
}

func (h *handler) authRefresh(w http.ResponseWriter, r *http.Request) {
	var p struct {
		RefreshToken string `json:"refresh_token"`
	}
	if deskDecode(w, r, &p) != nil || p.RefreshToken == "" {
		writeJSON(w, 400, map[string]string{"error": "Yenileme anahtarı gerekli."})
		return
	}
	tx, err := h.deskPool.Begin(r.Context())
	if err != nil {
		deskWriteError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var sessionID, actorID, accountStatus, actorStatus string
	err = tx.QueryRow(r.Context(), `SELECT s.id,a.actor_id,a.status,act.status FROM auth_sessions s JOIN accounts a ON a.id=s.account_id AND a.workspace_id=s.workspace_id JOIN actors act ON act.id=a.actor_id AND act.workspace_id=s.workspace_id WHERE s.refresh_hash=$1 AND s.revoked_at IS NULL AND s.refresh_expires_at>now() FOR UPDATE OF s`, tokenHash(p.RefreshToken)).Scan(&sessionID, &actorID, &accountStatus, &actorStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 401, map[string]string{"error": "Oturum yenilenemedi."})
		return
	}
	if err != nil {
		deskWriteError(w, err)
		return
	}
	if accountStatus != "active" || actorStatus != "active" {
		writeJSON(w, 401, map[string]string{"error": "Oturum yenilenemedi."})
		return
	}
	access, accessHash, err := newToken()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	refresh, refreshHash, err := newToken()
	if err != nil {
		deskWriteError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE auth_sessions SET access_hash=$2,refresh_hash=$3,expires_at=now()+interval '8 hours',refresh_expires_at=now()+interval '30 days' WHERE id=$1`, sessionID, accessHash, refreshHash)
	if err != nil {
		deskWriteError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		deskWriteError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 28800, "actor_id": actorID})
}

func (h *handler) authLogout(w http.ResponseWriter, r *http.Request) {
	d, err := h.deskIdentity(r.Context())
	if err != nil {
		deskWriteError(w, err)
		return
	}
	if d.SessionID != "" {
		_, err = h.deskPool.Exec(r.Context(), `UPDATE auth_sessions SET revoked_at=now() WHERE id=$1 AND workspace_id=$2 AND revoked_at IS NULL`, d.SessionID, d.WorkspaceID)
		if err != nil {
			deskWriteError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func hashAdminPassword(ctx context.Context, tx pgx.Tx, accountID, password string) error {
	hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE accounts SET password_hash=$2,updated_at=now() WHERE id=$1`, accountID, hash)
	return err
}
