-- =====================================================================
-- ENTEKSIS PROJE YONETIM MODULU
-- V0.1 -- DDL v2.1.2 PATCH (uygulanir uzerine: enteksis_v0.1_migration_v2.1.1.sql)
-- Kaynak: V1.2 Nihai Yol Haritasi, Bolum 05 hardening paketi
-- Tarih: 04 Eylul 2026
-- =====================================================================
-- Bu patch, v2.1.1'de zaten dogru olan maddelere DOKUNMAZ (TaskAssignment,
-- partial unique index'ler, Team, RoleBinding/PermissionGrant ayrimi,
-- Subtask/Checklist ayrimi, Idempotency -- bunlarin hepsi v2.1.1'de zaten var).
-- Yalniz gercekten YENI/EKSIK olan maddeleri isler:
--   (3) accounts - actor iliski yonu (bir Actor'un birden fazla principal'i olabilir)
--   (5) cross-workspace invariant (tenant izolasyonu)
--   (7) Comment + Attachment tablolari (V0.1'e geri eklendi)
--   (8) ProjectStage tablosu
--  (10) SubmissionReview (append-only review sonucu, Submission'dan ayri)
--  (12) System/Automation Actor seed (system-generated event'ler icin)
-- =====================================================================

BEGIN;

-- ---------------------------------------------------------------------
-- FIX #3: accounts -> actor iliski yonu
-- Eskiden: actors.account_id (bir actor en fazla bir account'a sahip olabilirdi)
-- Yeni: accounts.actor_id (bir actor zaman icinde birden fazla account/principal
--       edinebilir -- parola girisi + sonradan eklenen API anahtari gibi)
-- ---------------------------------------------------------------------

ALTER TABLE accounts ADD COLUMN actor_id UUID REFERENCES actors(id);

-- Var olan actors.account_id verisini yeni yone tasi (varsa)
UPDATE accounts a
SET actor_id = act.id
FROM actors act
WHERE act.account_id = a.id;

-- Eski yonu kaldir
ALTER TABLE actors DROP COLUMN account_id;

CREATE INDEX ix_accounts_actor ON accounts (actor_id);

COMMENT ON COLUMN accounts.actor_id IS
  'Bir Actor zaman icinde birden fazla Account/principal edinebilir (fix #3). '
  'Yon actors->accounts degil, accounts->actors.';

-- ---------------------------------------------------------------------
-- FIX #5: Cross-workspace invariant (tenant izolasyonu yalniz UI'a birakilmaz)
-- Composite FK yaklasimi: her alt tablo, kendi workspace_id'sini ust tablonun
-- workspace_id'si ile ES ZAMANLI dogrulayan bir (id, workspace_id) composite
-- unique kisitina sahip ust tablo + composite FK ile baglanir.
-- ---------------------------------------------------------------------

-- Ust tablolara composite unique ekleniyor (composite FK hedefi olabilmek icin)
ALTER TABLE projects ADD CONSTRAINT ux_projects_id_ws UNIQUE (id, workspace_id);
ALTER TABLE actors   ADD CONSTRAINT ux_actors_id_ws   UNIQUE (id, workspace_id);
ALTER TABLE teams    ADD CONSTRAINT ux_teams_id_ws    UNIQUE (id, workspace_id);
ALTER TABLE tasks    ADD CONSTRAINT ux_tasks_id_ws    UNIQUE (id, workspace_id);

-- tasks.project_id artik composite FK ile ayni workspace'e zorlanir
ALTER TABLE tasks ADD COLUMN workspace_id_check UUID; -- gecici degil; asagida gercek kolonla degistirilecek
ALTER TABLE tasks DROP COLUMN workspace_id_check;

ALTER TABLE tasks
  ADD CONSTRAINT fk_tasks_project_same_ws
  FOREIGN KEY (project_id, workspace_id) REFERENCES projects (id, workspace_id);

-- task_assignments.actor_id ayni workspace'e ait olmali -> once workspace_id kolonu eklenir
ALTER TABLE task_assignments ADD COLUMN workspace_id UUID;
UPDATE task_assignments ta SET workspace_id = t.workspace_id
  FROM tasks t WHERE t.id = ta.task_id;
ALTER TABLE task_assignments ALTER COLUMN workspace_id SET NOT NULL;

ALTER TABLE task_assignments
  ADD CONSTRAINT fk_ta_task_same_ws
  FOREIGN KEY (task_id, workspace_id) REFERENCES tasks (id, workspace_id);

ALTER TABLE task_assignments
  ADD CONSTRAINT fk_ta_actor_same_ws
  FOREIGN KEY (actor_id, workspace_id) REFERENCES actors (id, workspace_id);

-- team_memberships.actor_id ayni workspace'e ait olmali
ALTER TABLE team_memberships ADD COLUMN workspace_id UUID;
UPDATE team_memberships tm SET workspace_id = tt.workspace_id
  FROM teams tt WHERE tt.id = tm.team_id;
ALTER TABLE team_memberships ALTER COLUMN workspace_id SET NOT NULL;

ALTER TABLE team_memberships
  ADD CONSTRAINT fk_tm_team_same_ws
  FOREIGN KEY (team_id, workspace_id) REFERENCES teams (id, workspace_id);

ALTER TABLE team_memberships
  ADD CONSTRAINT fk_tm_actor_same_ws
  FOREIGN KEY (actor_id, workspace_id) REFERENCES actors (id, workspace_id);

COMMENT ON CONSTRAINT fk_ta_actor_same_ws ON task_assignments IS
  'Fix #5: bir actor, kendi workspace''i disindaki bir goreve atanamaz -- DB seviyesinde garanti.';

-- ---------------------------------------------------------------------
-- FIX #7: Comment + Attachment (V0.1 temel akisina geri eklendi)
-- ---------------------------------------------------------------------

CREATE TYPE commentable_type_t AS ENUM ('task', 'submission', 'project');

CREATE TABLE comments (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID NOT NULL REFERENCES workspaces(id),
    commentable_type commentable_type_t NOT NULL,
    commentable_id   UUID NOT NULL,
    body             TEXT NOT NULL,
    created_by       UUID NOT NULL REFERENCES actors(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    edited_at        TIMESTAMPTZ
    -- NOT: silinmez; UI'da "kaldir" istenirse body '[silindi]' ile degistirilir
    -- ve edited_at set edilir -- ham tarihce korunur (I0-3 ile tutarli).
);

CREATE INDEX ix_comments_target ON comments (commentable_type, commentable_id);

CREATE TABLE attachments (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID NOT NULL REFERENCES workspaces(id),
    commentable_type commentable_type_t NOT NULL,  -- ayni hedef turleri (task/submission/project)
    commentable_id   UUID NOT NULL,
    file_name        VARCHAR(500) NOT NULL,
    mime_type        VARCHAR(150) NOT NULL,
    size_bytes       BIGINT,
    storage_url      TEXT NOT NULL,      -- object storage referansi
    uploaded_by      UUID NOT NULL REFERENCES actors(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ix_attachments_target ON attachments (commentable_type, commentable_id);

-- ---------------------------------------------------------------------
-- FIX #8: ProjectStage (V0.1'de en az Stage korunur)
-- ---------------------------------------------------------------------

CREATE TABLE project_stages (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    project_id   UUID NOT NULL REFERENCES projects(id),
    name         VARCHAR(255) NOT NULL,
    position     INT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE tasks ADD COLUMN project_stage_id UUID REFERENCES project_stages(id);

-- ---------------------------------------------------------------------
-- FIX #10: SubmissionReview (Submission ham teslimdir; review sonucu
-- append-only ayri kayittir -- Submission'in status alanini UPDATE ile
-- degistirmek yerine, her review karari yeni bir satir olarak eklenir.)
-- ---------------------------------------------------------------------

CREATE TABLE submission_reviews (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    submission_id  UUID NOT NULL REFERENCES submissions(id),
    reviewed_by    UUID NOT NULL REFERENCES actors(id),
    decision       VARCHAR(30) NOT NULL,  -- accepted | revision_needed
    comments       TEXT,
    failed_criteria_ids UUID[],           -- acceptance_criteria.id listesi (revision_needed ise)
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
    -- Append-only: bir submission uzerinde birden fazla review olayi olabilir
    -- (orn. once yanlislikla yanlis karar verilip yeni bir review eklenmesi
    -- gerekirse), ama hicbir review satiri UPDATE/DELETE edilmez.
);

CREATE INDEX ix_submission_reviews_submission ON submission_reviews (submission_id);

COMMENT ON TABLE submissions IS
  'Ham teslim anidir. submissions.status/reviewer_notes alanlari V0.1 basitligi '
  'icin tutulur, ama otoriter review tarihcesi submission_reviews tablosundadir (fix #10).';

-- ---------------------------------------------------------------------
-- FIX #12: System / Automation Actor seed
-- Sistem tarafindan tetiklenen gecisler (orn. submitted -> under_review,
-- System (auto)) audit_events.actor_id icin NOT NULL bir Actor gerektirir.
-- Her workspace icin bir System Actor tohumlanir.
-- ---------------------------------------------------------------------

INSERT INTO actors (id, workspace_id, type, display_name, status)
SELECT gen_random_uuid(), w.id, 'automation', 'System', 'active'
FROM workspaces w
WHERE NOT EXISTS (
    SELECT 1 FROM actors a
    WHERE a.workspace_id = w.id AND a.type = 'automation' AND a.display_name = 'System'
);

COMMENT ON TABLE actors IS
  'Her workspace icin type=automation, display_name=System olan bir satir '
  'tohumlanmis olmalidir -- system-generated audit/outbox event''lerinin '
  'actor_id''si bu satiri referans alir (fix #12).';

COMMIT;

-- =====================================================================
-- NOT: item #13 (API listeleme/pagination) ve #14 (outbox causation
-- semantiginin normatif aciklamasi) fiziksel DDL degil, API Contract
-- ve Domain Contract dokumantasyon eklemeleridir; bu patch dosyasinin
-- kapsami disindadir.
-- =====================================================================
