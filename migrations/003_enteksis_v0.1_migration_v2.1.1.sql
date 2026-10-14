-- =====================================================================
-- ENTEKSIS PROJE YONETIM MODULU
-- V0.1 Brutal MVP -- PostgreSQL Migration DDL v2.1.1
-- Kaynak: Domain Contract v2.1 + 15 maddelik teknik duzeltme turu
-- Tarih: 04 Eylul 2026
-- =====================================================================
-- Bu dosya, onceki DDL taslaginda tespit edilen 15 sorunu cozer:
--  (1) TaskAssignment tek kaynak -- tasks tablosunda owner/assignee/
--      reviewer FK yok.
--  (2) Partial unique index ile tek aktif Owner / Primary Assignee.
--  (3) Actor ailesi: internal | external_human | organization |
--      ai_agent | automation; organization_actor_id gercek self-FK.
--  (4) role_bindings (yapisal rol) ile permission_grants (delege
--      edilen, gecici yetki) ayrildi.
--  (5) workspaces gercek tablo, tum workspace_id kolonlari FK.
--  (6) team_memberships icin partial unique index (tek aktif uyelik).
--  (7) Subtask (tasks.parent_task_id, tam Task) ile checklist_items
--      (basit checkbox) ayrildi.
--  (8) V0.1'de minimal Submission + Review dongusu fiziksel; agir
--      Decision/Change Request V0.5'e birakildi.
--  (9) op_state icin ayri 'ready' gecisi (planned -> ready) korunur;
--      /tasks/:id/ready endpoint'i ile eslesir (bkz. API Contract).
--  (10) Ayni gorevde birden fazla aktif blocker desteklenir.
--  (11) Hard delete yok; tum tarihce tasiyan tablolar append/soft-
--       kapama ile yonetilir (ON DELETE CASCADE kullanimi sinirli).
--  (12) invitations V0.1'de fiziksel ACILMAZ (K-18); V0.6'ya birakildi.
--  (13) notifications tablosu (kullanici okuma durumu) eklendi.
--  (14) outbox_events'e workspace_id + correlation_id + causation_id
--       eklendi.
--  (15) idempotency_keys tablosu; mutasyon endpoint'leri
--       Idempotency-Key header'i zorunlu kosar (bkz. API Contract).
-- =====================================================================

CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid() icin

-- =====================================================================
-- 0. ENUM TIPLERI
-- =====================================================================

CREATE TYPE auth_type        AS ENUM ('password', 'api_key', 'service_account');
CREATE TYPE actor_type       AS ENUM ('internal', 'external_human', 'organization', 'ai_agent', 'automation');
CREATE TYPE assignment_role  AS ENUM ('owner', 'primary_assignee', 'contributor', 'observer', 'reviewer');
CREATE TYPE assignment_status AS ENUM ('active', 'ended');
CREATE TYPE op_state_t       AS ENUM ('planned', 'ready', 'in_progress', 'work_completed', 'blocked', 'cancelled', 'closed');
CREATE TYPE acc_state_t      AS ENUM ('not_submitted', 'submitted', 'under_review', 'revision_needed', 'accepted', 'not_required');
CREATE TYPE fin_state_t      AS ENUM ('not_applicable', 'pending_payment', 'paid', 'disputed');
CREATE TYPE block_type_t     AS ENUM ('dependency', 'manual');
CREATE TYPE grant_status_t   AS ENUM ('active', 'revoked', 'expired');
CREATE TYPE project_status_t AS ENUM ('active', 'archived');

-- =====================================================================
-- 1. WORKSPACE  (fix #5 -- gercek tenant tablosu)
-- =====================================================================

CREATE TABLE workspaces (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    slug        VARCHAR(100) NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =====================================================================
-- 2. ACCOUNT & ACTOR  (fix #3 -- actor ailesi genisletildi)
-- =====================================================================

CREATE TABLE accounts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID NOT NULL REFERENCES workspaces(id),
    email          VARCHAR(255) NOT NULL,
    password_hash  VARCHAR(255),
    auth_type      auth_type NOT NULL DEFAULT 'password',
    status         VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, email)
);

CREATE TABLE actors (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id          UUID NOT NULL REFERENCES workspaces(id),
    account_id            UUID REFERENCES accounts(id),      -- nullable: organization actor'un hesabi olmayabilir
    type                  actor_type NOT NULL,
    organization_actor_id UUID REFERENCES actors(id),        -- gercek self-FK (fix #3): external_human -> organization
    display_name          VARCHAR(255) NOT NULL,
    max_autonomy_level     VARCHAR(50),                       -- yalniz ai_agent icin anlamli, V0.7'de aktive
    status                VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_org_link CHECK (
        -- yalniz external_human bir organization'a baglanabilir
        (type = 'external_human') OR (organization_actor_id IS NULL)
    )
);

CREATE INDEX ix_actors_workspace ON actors (workspace_id);
CREATE INDEX ix_actors_org ON actors (organization_actor_id);

-- =====================================================================
-- 3. TEAM  (V0.1 cekirdegine dahil)
-- =====================================================================

CREATE TABLE teams (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspaces(id),
    name          VARCHAR(255) NOT NULL,
    description   TEXT,
    created_by    UUID NOT NULL REFERENCES actors(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE team_memberships (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     UUID NOT NULL REFERENCES teams(id),
    actor_id    UUID NOT NULL REFERENCES actors(id),
    role        VARCHAR(20) NOT NULL DEFAULT 'member', -- lead | member
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    left_at     TIMESTAMPTZ
);

-- fix #6: ayni actor ayni takimda birden fazla AKTIF uyelige sahip olamaz
CREATE UNIQUE INDEX ux_team_membership_active
    ON team_memberships (team_id, actor_id) WHERE left_at IS NULL;

-- =====================================================================
-- 4. ROLE BINDINGS & PERMISSION GRANTS  (fix #4 -- ayristirildi)
-- =====================================================================

-- Yapisal / kalici rol atamalari: "Bu actor Workspace Admin / PM / Team
-- Lead mi?" -- API'deki rol kontrollerinin veritabani karsiligi budur.
CREATE TABLE role_bindings (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspaces(id),
    actor_id      UUID NOT NULL REFERENCES actors(id),
    role          VARCHAR(50) NOT NULL,   -- workspace_admin | project_manager | team_lead | member
    scope_type    VARCHAR(20) NOT NULL DEFAULT 'workspace', -- workspace | project | team
    scope_id      UUID,                    -- scope_type != workspace ise zorunlu
    granted_by    UUID REFERENCES actors(id),
    granted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_by    UUID REFERENCES actors(id),
    revoked_at    TIMESTAMPTZ,
    status        grant_status_t NOT NULL DEFAULT 'active',
    CONSTRAINT chk_role_scope CHECK (
        (scope_type = 'workspace' AND scope_id IS NULL) OR
        (scope_type <> 'workspace' AND scope_id IS NOT NULL)
    )
);

CREATE INDEX ix_role_bindings_actor ON role_bindings (actor_id) WHERE status = 'active';

-- Delege edilen, gecici, geri alinabilir yetkiler (orn. "bu freelancer
-- proje X kapsaminda assign_task yapabilir"). Rol atamasindan farkli
-- olarak zaman sinirlidir (valid_from/valid_until) ve gerekce tasir.
CREATE TABLE permission_grants (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspaces(id),
    actor_id      UUID NOT NULL REFERENCES actors(id),
    action        VARCHAR(100) NOT NULL,   -- assign_task | reassign_task | manage_task_participants | invite_external_actor | ...
    scope_type    VARCHAR(20) NOT NULL,    -- workspace | team | project | task
    scope_id      UUID,
    granted_by    UUID NOT NULL REFERENCES actors(id),
    granted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_from    TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_until   TIMESTAMPTZ,             -- NULL = suresiz, elle iptal beklenir
    revoked_by    UUID REFERENCES actors(id),
    revoked_at    TIMESTAMPTZ,
    status        grant_status_t NOT NULL DEFAULT 'active',
    reason        TEXT
);

CREATE INDEX ix_permission_grants_actor ON permission_grants (actor_id) WHERE status = 'active';

-- =====================================================================
-- 5. PROJECT  (minimal -- Stage/Milestone V0.1 disi, sadece FK hedefi)
-- =====================================================================

CREATE TABLE projects (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspaces(id),
    name          VARCHAR(255) NOT NULL,
    description   TEXT,
    status        project_status_t NOT NULL DEFAULT 'active',
    created_by    UUID NOT NULL REFERENCES actors(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =====================================================================
-- 6. TASK & TASK ASSIGNMENT  (fix #1, #2, #7, #9, #11)
-- =====================================================================

CREATE TABLE tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspaces(id),
    project_id      UUID NOT NULL REFERENCES projects(id),
    parent_task_id  UUID REFERENCES tasks(id),   -- fix #7: gercek Subtask (kendi assignment/state'i olur)
    title           VARCHAR(500) NOT NULL,
    description     TEXT,
    op_state        op_state_t  NOT NULL DEFAULT 'planned',
    acc_state       acc_state_t NOT NULL DEFAULT 'not_submitted',
    fin_state       fin_state_t NOT NULL DEFAULT 'not_applicable',
    ui_state        VARCHAR(50) NOT NULL DEFAULT 'todo',
    priority        VARCHAR(20) NOT NULL DEFAULT 'medium',
    due_date        TIMESTAMPTZ,
    created_by      UUID NOT NULL REFERENCES actors(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
    -- NOT: owner_id / assignee_id / reviewer_id kasitli olarak YOK.
    -- Tum sorumluluk iliskileri task_assignments uzerinden yuruyor (fix #1).
);

CREATE INDEX ix_tasks_project ON tasks (project_id);
CREATE INDEX ix_tasks_parent ON tasks (parent_task_id);
CREATE INDEX ix_tasks_op_state ON tasks (op_state);

-- Basit checkbox alt-oge (fix #7: gercek Subtask'tan ayri)
CREATE TABLE checklist_items (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id     UUID NOT NULL REFERENCES tasks(id),
    title       VARCHAR(500) NOT NULL,
    is_done     BOOLEAN NOT NULL DEFAULT false,
    position    INT NOT NULL DEFAULT 0,
    created_by  UUID NOT NULL REFERENCES actors(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    done_at     TIMESTAMPTZ
);

CREATE TABLE task_assignments (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID NOT NULL REFERENCES tasks(id),
    actor_id      UUID NOT NULL REFERENCES actors(id),
    role          assignment_role NOT NULL,
    status        assignment_status NOT NULL DEFAULT 'active',
    assigned_by   UUID NOT NULL REFERENCES actors(id),
    assigned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at   TIMESTAMPTZ,
    ended_at      TIMESTAMPTZ,
    end_reason    TEXT
);

CREATE INDEX ix_task_assignments_task ON task_assignments (task_id);
CREATE INDEX ix_task_assignments_actor ON task_assignments (actor_id) WHERE status = 'active';

-- fix #2: partial unique index -- inline "UNIQUE ... WHERE" gecerli
-- bir PostgreSQL sozdizimi degildir, bu yuzden CREATE UNIQUE INDEX kullanilir.
CREATE UNIQUE INDEX ux_task_one_active_owner
    ON task_assignments (task_id) WHERE role = 'owner' AND status = 'active';

CREATE UNIQUE INDEX ux_task_one_active_primary_assignee
    ON task_assignments (task_id) WHERE role = 'primary_assignee' AND status = 'active';

-- =====================================================================
-- 7. TASK PACKAGE & ACCEPTANCE CRITERIA  (fix #8 -- V0.1 icin minimal)
-- =====================================================================

CREATE TABLE task_packages (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id           UUID NOT NULL REFERENCES tasks(id),
    purpose           TEXT,
    context           TEXT,
    scope_in          JSONB,
    scope_out         JSONB,
    delivery_format   VARCHAR(200),
    review_required   BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
    -- NOT: immutable_rules / resources / dependencies (agir siniflandirma)
    -- V0.5'te "tam Task Package" ile birlikte eklenir (K-18).
);

CREATE TABLE acceptance_criteria (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_package_id        UUID NOT NULL REFERENCES task_packages(id),
    description            TEXT NOT NULL,
    required_evidence_type VARCHAR(50), -- url | file | pr | screenshot
    status                 VARCHAR(20) NOT NULL DEFAULT 'pending' -- pending | passed | failed
);

-- =====================================================================
-- 8. BLOCKERS  (fix #10 -- coklu aktif blocker)
-- =====================================================================

CREATE TABLE blockers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id             UUID NOT NULL REFERENCES tasks(id),
    block_type          block_type_t NOT NULL,
    blocked_by_actor_id UUID REFERENCES actors(id),  -- dependency ise NULL
    reason              TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at         TIMESTAMPTZ
);

CREATE INDEX ix_blockers_task_active ON blockers (task_id) WHERE resolved_at IS NULL;

-- =====================================================================
-- 9. SUBMISSION & EVIDENCE  (fix #8 -- minimal ama gercek dongu)
-- =====================================================================

CREATE TABLE submissions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id          UUID NOT NULL REFERENCES tasks(id),
    submitted_by     UUID NOT NULL REFERENCES actors(id),
    iteration_number INT NOT NULL,
    status           VARCHAR(30) NOT NULL DEFAULT 'under_review', -- under_review | accepted | revision_needed
    reviewer_notes   TEXT,
    reviewed_by      UUID REFERENCES actors(id),
    reviewed_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, iteration_number)
    -- NOT: submissions hicbir zaman UPDATE ile silinmez/degistirilmez
    -- (status/reviewer_notes disinda); K-04.
);

CREATE TABLE evidences (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    submission_id  UUID NOT NULL REFERENCES submissions(id),
    criterion_id   UUID REFERENCES acceptance_criteria(id),
    resource_url   TEXT,
    payload        JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =====================================================================
-- 10. AUDIT & OUTBOX  (fix #14 -- correlation/causation eklendi)
-- =====================================================================

CREATE TABLE audit_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspaces(id),
    aggregate_type  VARCHAR(50) NOT NULL,
    aggregate_id    UUID NOT NULL,
    event_type      VARCHAR(100) NOT NULL,
    actor_id        UUID NOT NULL REFERENCES actors(id),
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
    -- Append-only: uygulama katmaninda UPDATE/DELETE izni verilmez (I0-5, K-11).
);

CREATE INDEX ix_audit_aggregate ON audit_events (aggregate_type, aggregate_id);

CREATE TABLE outbox_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspaces(id),
    aggregate_type  VARCHAR(50) NOT NULL,
    aggregate_id    UUID NOT NULL,
    event_type      VARCHAR(100) NOT NULL,
    payload         JSONB NOT NULL,
    correlation_id  UUID,   -- ayni is akisina (orn. tek bir reassign islemi) ait tum event'leri baglar
    causation_id    UUID,   -- bu event'i dogrudan tetikleyen onceki event'in id'si
    status          VARCHAR(20) NOT NULL DEFAULT 'pending', -- pending | published | failed
    retry_count     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ
);

CREATE INDEX ix_outbox_pending ON outbox_events (status) WHERE status = 'pending';
CREATE INDEX ix_outbox_correlation ON outbox_events (correlation_id);

-- =====================================================================
-- 11. NOTIFICATIONS  (fix #13 -- kullanici bazli okuma durumu)
-- =====================================================================

CREATE TABLE notifications (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID NOT NULL REFERENCES workspaces(id),
    actor_id         UUID NOT NULL REFERENCES actors(id),  -- alici
    outbox_event_id  UUID REFERENCES outbox_events(id),
    type             VARCHAR(100) NOT NULL,
    payload          JSONB,
    read_at          TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ix_notifications_unread ON notifications (actor_id) WHERE read_at IS NULL;

-- =====================================================================
-- 12. IDEMPOTENCY  (fix #15)
-- =====================================================================

CREATE TABLE idempotency_keys (
    key              VARCHAR(255) PRIMARY KEY,
    workspace_id     UUID NOT NULL REFERENCES workspaces(id),
    actor_id         UUID NOT NULL REFERENCES actors(id),
    endpoint         VARCHAR(255) NOT NULL,
    request_hash     VARCHAR(64) NOT NULL,   -- istek govdesinin hash'i; ayni key farkli govde ile gelirse 409
    response_status  INT,
    response_body    JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =====================================================================
-- NOT: invitations tablosu bilincli olarak bu migration'da YOK (fix #12).
-- Domain Contract v2.1 Bolum 3.6'da sozlesmede tanimlidir; fiziksel
-- tablo V0.6 migration'inda acilir (K-18).
-- =====================================================================
