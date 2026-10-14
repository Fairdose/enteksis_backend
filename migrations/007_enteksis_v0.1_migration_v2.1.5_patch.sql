-- =====================================================================
-- ENTEKSIS PROJE YONETIM MODULU
-- V0.1 -- DDL v2.1.5 PATCH (uygulanir uzerine: v2.1.1+v2.1.2+v2.1.3+v2.1.4)
-- Kaynak: AI-C-001 Kimi Final Release Audit -- PASS WITH MINOR FIXES
--         (7 MINOR bulgu: KIMI-001..007)
-- Tarih: 04 Eylul 2026
-- =====================================================================
-- Bu patch YALNIZ audit'te bulunan 7 MINOR bulguyu kapatir:
--   KIMI-001..003: fiziksel tenant/butunluk bosluklarini composite FK
--                   ile kapatir (yeni tablo/trigger/urun karari yok).
--   KIMI-004..007: yalniz COMMENT ON ile schema dokumantasyonunu
--                   netlestirir (kolon DROP, trigger, backward
--                   migration YOK).
-- =====================================================================

BEGIN;

-- ---------------------------------------------------------------------
-- KIMI-001: tasks.parent_task_id cross-workspace invariant
-- Hedef unique zaten v2.1.2'de var: ux_tasks_id_ws UNIQUE(id, workspace_id)
-- parent_task_id NULL olabilir; composite FK'de herhangi bir kolon NULL
-- ise PostgreSQL varsayilan MATCH SIMPLE kontrolu atlar -- ek bir urun
-- karari (orn. "ayni project olmali") icat edilmedi, yalniz istenen
-- cross-workspace izolasyonu uygulanir.
-- ---------------------------------------------------------------------

ALTER TABLE tasks
  ADD CONSTRAINT fk_tasks_parent_same_ws
  FOREIGN KEY (parent_task_id, workspace_id) REFERENCES tasks (id, workspace_id);

COMMENT ON CONSTRAINT fk_tasks_parent_same_ws ON tasks IS
  'KIMI-001: bir Task, kendi workspace''i disindaki bir Task''i parent '
  'olarak secemez. parent_task_id NULL olabilir (stagesiz/root task); '
  'bu durumda composite FK kontrolu atlanir. Ek urun karari yoktur '
  '(orn. ayni proje zorunlulugu) -- yalniz tenant izolasyonu.';

-- ---------------------------------------------------------------------
-- KIMI-002: project_stages.project_id tenant invariant
-- Hedef unique zaten v2.1.2'de var: ux_projects_id_ws UNIQUE(id, workspace_id)
-- ---------------------------------------------------------------------

ALTER TABLE project_stages
  ADD CONSTRAINT fk_stages_project_same_ws
  FOREIGN KEY (project_id, workspace_id) REFERENCES projects (id, workspace_id);

COMMENT ON CONSTRAINT fk_stages_project_same_ws ON project_stages IS
  'KIMI-002: bir ProjectStage, kendi workspace''i disindaki bir Project''e '
  'baglanamaz -- DB seviyesinde garanti.';

-- ---------------------------------------------------------------------
-- KIMI-003: task / project_stage butunlugu
-- Bir Task'in project_stage_id'si HEM ayni workspace'e HEM DE Task'in
-- kendi project_id'si ile ayni Project'e ait olmalidir. Tek bir composite
-- FK ile (uc alan birden) bu invariant DB seviyesinde garanti edilir.
-- project_stage_id NULL olabilir (stagesiz Task V0.1'de gecerlidir);
-- composite FK bu durumda atlanir.
-- ---------------------------------------------------------------------

ALTER TABLE project_stages
  ADD CONSTRAINT ux_stages_id_project_ws UNIQUE (id, project_id, workspace_id);

ALTER TABLE tasks
  ADD CONSTRAINT fk_tasks_stage_same_project_ws
  FOREIGN KEY (project_stage_id, project_id, workspace_id)
  REFERENCES project_stages (id, project_id, workspace_id);

COMMENT ON CONSTRAINT fk_tasks_stage_same_project_ws ON tasks IS
  'KIMI-003: bir Task''in project_stage_id''si, hem Task ile ayni '
  'workspace''e hem de Task''in kendi project_id''si ile ayni Project''e '
  'ait bir ProjectStage''i gostermek ZORUNDADIR. Tek composite FK ile '
  'her iki kosul birden garanti edilir. project_stage_id NULL olabilir '
  '(stagesiz Task V0.1''de gecerli kalir).';

-- ---------------------------------------------------------------------
-- KIMI-004: Submission immutability -- yalniz dokumantasyon (COMMENT)
-- Yeni tablo/trigger/backward migration YOK. submissions.status/
-- reviewer_notes/reviewed_by/reviewed_at legacy/non-authoritative
-- alanlardir; yetkili review tarihcesi submission_reviews'dur (zaten
-- v2.1.2'de append-only olarak kurulmustu).
-- ---------------------------------------------------------------------

COMMENT ON COLUMN submissions.status IS
  'KIMI-004: Legacy/non-authoritative. V0.1 servis katmani review '
  'history icin bu alani KULLANMAZ. Yetkili kaynak: '
  'submission_reviews.decision. Submission ham teslim anidir, '
  'olusturulduktan sonra overwrite edilmez; yeni teslim = yeni iteration.';

COMMENT ON COLUMN submissions.reviewer_notes IS
  'KIMI-004: Legacy/non-authoritative. Yetkili review notlari icin '
  'submission_reviews.comments kullanilir.';

COMMENT ON COLUMN submissions.reviewed_by IS
  'KIMI-004: Legacy/non-authoritative. Yetkili kaynak: '
  'submission_reviews.reviewed_by.';

COMMENT ON COLUMN submissions.reviewed_at IS
  'KIMI-004: Legacy/non-authoritative. Yetkili kaynak: '
  'submission_reviews.created_at.';

-- ---------------------------------------------------------------------
-- KIMI-005: Blocker resolver attribution -- yeni kolon YOK
-- Blocker'i kimin cozdugu audit_events.actor_id uzerinden izlenir;
-- blockers tablosuna resolved_by kolonu eklenmez.
-- ---------------------------------------------------------------------

COMMENT ON TABLE blockers IS
  'KIMI-005: blocker''in operasyonel varligini ve resolved_at zamanini '
  'tutar. Blocker''i KIMIN cozdugu (resolver attribution) icin ayri bir '
  'resolved_by kolonu YOKTUR ve eklenmeyecektir -- bu bilgi '
  'audit_events.actor_id uzerinden (ilgili task.blocker_resolved event''i '
  'ile) izlenir (V0.1 canonical karar).';

-- ---------------------------------------------------------------------
-- KIMI-006: acceptance_criteria.status -- derived/read-only summary
-- Yeni state motoru YOK. Yetkili kaynak submission_reviews.
-- ---------------------------------------------------------------------

COMMENT ON COLUMN acceptance_criteria.status IS
  'KIMI-006: Derived/read-only summary alanidir, authoritative review '
  'tarihcesi DEGILDIR. Yetkili kaynak: submission_reviews.decision + '
  'submission_reviews.failed_criteria_ids. Servis katmani bu alani '
  'gecmisin tek kaynagi olarak KULLANAMAZ.';

-- ---------------------------------------------------------------------
-- KIMI-007: tasks.ui_state -- V0.1 domain state degil
-- ui_state = op_state senkronizasyon motoru KURULMAYACAK.
-- ---------------------------------------------------------------------

COMMENT ON COLUMN tasks.ui_state IS
  'KIMI-007: V0.1 domain state DEGILDIR; kullanilmayan/reserved legacy '
  'alan olarak kabul edilir. "ui_state = op_state" senkronizasyon '
  'motoru KURULMAYACAKTIR. Gercek domain state: op_state + acc_state '
  '(+ kapanis kuralinda fin_state).';

COMMIT;

-- =====================================================================
-- Bu patch'te KASITLI OLARAK yapilmayanlar (Council talimati geregi):
--   - Yeni feature / yeni mimari yok.
--   - Onceki hash'lenmis migration/API dosyalari degistirilmedi.
--   - Kolon DROP edilmedi, trigger eklenmedi, backward migration yok.
--   - "parent task ayni project olmali" gibi yeni urun karari icat
--     edilmedi -- KIMI-001 yalniz cross-workspace izolasyonudur.
--   - blockers.resolved_by gibi yeni kolon eklenmedi (KIMI-005).
-- =====================================================================
