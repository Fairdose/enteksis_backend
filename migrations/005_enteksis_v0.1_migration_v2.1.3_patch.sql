-- =====================================================================
-- ENTEKSIS PROJE YONETIM MODULU
-- V0.1 -- DDL v2.1.3 PATCH (uygulanir uzerine: v2.1.1 + v2.1.2_patch)
-- Kaynak: AI-C-001-CLAUDE "Benim Masam" backend verification bulgulari
-- Tarih: 04 Eylul 2026
-- =====================================================================
-- Ucu bulgu, ucu de BLOCKER, ucu de gercek PostgreSQL'de reprodüksiyon +
-- fix dogrulamasi yapilmistir (bkz. AI-C-001-CLAUDE-deliverable.md, Bolum F).
--   F-02: System Actor seed'i trigger'a baglaniyor (tek seferlik backfill degil)
--   F-03: idempotency_keys PK'si (workspace_id, actor_id, endpoint, key) oluyor
--   F-04: task_packages.task_id UNIQUE oluyor (v1.2 madde 9'un fiziksel eksigi)
-- =====================================================================

BEGIN;

-- ---------------------------------------------------------------------
-- F-02: System/Automation Actor -- trigger tabanli otomatik seed
-- ---------------------------------------------------------------------

CREATE OR REPLACE FUNCTION seed_system_actor() RETURNS TRIGGER AS $$
BEGIN
  INSERT INTO actors (workspace_id, type, display_name, status)
  VALUES (NEW.id, 'automation', 'System', 'active');
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_seed_system_actor ON workspaces;
CREATE TRIGGER trg_seed_system_actor
  AFTER INSERT ON workspaces
  FOR EACH ROW EXECUTE FUNCTION seed_system_actor();

COMMENT ON FUNCTION seed_system_actor() IS
  'Fix F-02: her yeni workspace icin otomatik olarak type=automation, '
  'display_name=System bir Actor tohumlar. Onceki v2.1.2 patch''teki '
  'INSERT...SELECT tek seferlikti; bu trigger sonraki tum workspace''leri kapsar.';

-- ---------------------------------------------------------------------
-- F-03: idempotency_keys -- composite primary key
-- ---------------------------------------------------------------------

ALTER TABLE idempotency_keys DROP CONSTRAINT IF EXISTS idempotency_keys_pkey;
ALTER TABLE idempotency_keys
  ADD PRIMARY KEY (workspace_id, actor_id, endpoint, key);

COMMENT ON CONSTRAINT idempotency_keys_pkey ON idempotency_keys IS
  'Fix F-03: API Contract''in vaat ettigi (workspace_id, endpoint, Idempotency-Key) '
  'uclusune actor_id de eklenerek fiziksel olarak kilitlendi. Onceki halinde PK '
  'yalniz "key" idi; farkli actor/endpoint''ler ayni key string''ini kullanirsa '
  'yanlislikla carpisiyordu.';

-- ---------------------------------------------------------------------
-- F-04: task_packages -- tek gorev, tek paket
-- ---------------------------------------------------------------------

ALTER TABLE task_packages
  ADD CONSTRAINT ux_task_packages_task UNIQUE (task_id);

COMMENT ON CONSTRAINT ux_task_packages_task ON task_packages IS
  'Fix F-04: v1.2 hardening paketi madde 9''da "TaskPackage task_id tekil" '
  'zaten karar altina alinmisti; bu constraint o karari fiziksel olarak uygular.';

COMMIT;

-- =====================================================================
-- NOT: F-01 (delegated_waiting_for semantigi) ve F-05 (reassign icin
-- expected_assignment_id) bu patch'in kapsami disindadir -- ikisi de
-- sema degisikligi degil, sorgu/API sozlesmesi degisikligidir ve Alex'in
-- Council degerlendirmesini bekler. F-06 (blocker->op_state DB guard)
-- ve F-07 (composite index) SUGGESTION seviyesinde, bu patch'e dahil
-- edilmedi -- onaylanirsa ayri bir kucuk patch olarak eklenebilir.
-- =====================================================================
