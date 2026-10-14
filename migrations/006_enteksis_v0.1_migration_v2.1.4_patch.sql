-- =====================================================================
-- ENTEKSIS PROJE YONETIM MODULU
-- V0.1 -- DDL v2.1.4 PATCH (uygulanir uzerine: v2.1.1 + v2.1.2_patch + v2.1.3_patch)
-- Kaynak: F-07 paketleme/tutarlilik celiskisinin cozumu
-- Tarih: 04 Eylul 2026
-- =====================================================================
-- BAGLAM: enteksis_v0.1_migration_v2.1.3_patch.sql zaten yayinlanmis ve
-- hash'lenmis bir dosyadir (SHA-256: 89c0e086100ad84a56c0548842fd9c4b
-- b901d91fb13a0ebd5fa0e6367b16388a). O dosyanin kendi metni F-07'yi
-- ("composite index") acikca kapsam disi birakir. F-07, v2.1.3
-- kilitlendikten SONRA Council tarafindan onaylanmistir. Zaten
-- hash'lenmis/paylasilmis bir dosya geriye donuk sessizce degistirilmez
-- (bu, tam olarak audit zincirinin korumaya calistigi seydir) -- bu
-- yuzden F-07 forward-only, ayri ve minimal bir patch olarak burada
-- uygulanir.
--
-- Bu patch YALNIZ F-07'yi icerir. Baska hicbir yeni ozellik veya
-- mimari degisiklik yoktur.
-- =====================================================================

CREATE INDEX IF NOT EXISTS ix_ta_actor_role
    ON task_assignments (actor_id, role)
    WHERE status = 'active';

COMMENT ON INDEX ix_ta_actor_role IS
    'Fix F-07 (v2.1.4 forward-only patch): Benim Masam dashboard '
    'sorgularinin tamami actor_id + role + status=active ile filtreliyor; '
    'v2.1.1''deki ix_task_assignments_actor indexi yalniz actor_id''ye '
    'gore partial''di. F-07, v2.1.3 zaten hash''lenip yayinlandiktan '
    'sonra onaylandigi icin bu ayri forward-only patch''te uygulanir.';
