# AI_LOG

## İkinci faz — 14 Ekim 2026

### Kullanılan araçlar ve kaynaklar

- Codex: Go HTTP uçları, auth, transaction sınırları, migration runner, test ve dokümantasyon.
- Context7: Vue Composition API ve pgx transaction/pool belgeleri; SQL davranışı ayrıca gerçek PostgreSQL üzerinde sınandı.
- Podman/Compose ve PostgreSQL: kanonik beş geçişin sıralı kurulumu, tekrar açılış, DDL ve v2.1.5 integrity suite.

### Teknik kararlar

- Kanonik `003`–`007` SQL içerikleri değiştirilmedi; `migrate.sh` her dosyayı ve uygulama kaydını tek transaction içinde işler. Production Compose aynı migrator'ı kullanır. Eski `service_requests` geçişleri korunur.
- `008_create_auth_sessions.sql` kanonik domain şemasına ek uygulama katmanıdır. Parola özeti Go standart kütüphanesindeki PBKDF2-SHA256 ile üretilir; access/refresh anahtarlarının yalnız SHA-256 özetleri veritabanında tutulur. Refresh anahtarları döndürülür, logout oturumu iptal eder.
- `TaskAssignment` tek sorumluluk kaynağıdır. `allowed_actions` görev rolü ve aktif grant verisiyle sunucuda hesaplanır; mutasyon anında yeniden yetkilendirme yapılır. Eski primary assignment kimliğiyle reassign `409` döner.
- Görev mutasyonları `(workspace, actor, endpoint, key)` kapsamlı idempotency kaydı ve iş sonucu ile aynı transaction içindedir. Audit/outbox da aynı işlemde yazılır. Çoklu blocker'da son açık engel çözülmeden durum ilerlemez.
- V0.1 odak akışında takım yönetimi, genel düzenleme ve yorum/ek dosya uçları açılmadı; kanonik şema bu nesneleri barındırır.

### Doğrulama

- `go test ./...`, `go vet ./...` ve container image build geçti.
- Kanonik `enteksis_v0.1_v2.1.3_ddl_tests.sql` ile `enteksis_v0.1_v2.1.5_integrity_tests.sql` PostgreSQL üzerinde PASS verdi.
- API üzerinden görev, engel, zorunlu kanıt, revizyon, yeniden teslim/kabul, aynı anahtarın tekrarı ve farklı gövde 409, iki engel çözümü, stale reassign 409, ekip hesabı giriş/refresh/logout ve yetkisiz 403 senaryoları çalıştırıldı.


## Kullanılan araçlar

- Codex: Go API, migration, Docker Compose, testler ve dokümantasyon
- Context7: pgx connection pool ve Docker Compose güncel kullanım örnekleri
- Go test: handler doğrulama ve hata senaryoları
- Podman/Compose: Docker uyumlu image build ve gerçek PostgreSQL entegrasyon doğrulaması

## Kabul edilen ve değiştirilen öneriler

- Go standart `net/http` router'ı kabul edildi; challenge ölçeğinde ek framework bağımlılığı
  eklenmedi.
- PostgreSQL erişiminde parametreli sorgular ve `pgxpool` kullanıldı.
- İlk plandaki Supabase, değerlendiricinin projeyi kendi bilgisayarında çalıştıracağı bilgisi
  üzerine kaldırıldı. PostgreSQL 18, migration ve kalıcı volume Docker Compose içine alındı.
- Client image'ı Node build + root olmayan nginx runtime ile Compose'a eklendi. Her iki repoya
  aynı `run.sh` konarak Docker Desktop, Linux ve macOS için tek giriş noktası oluşturuldu;
  Windows'ta Git Bash/WSL gereksinimi açıkça belgelendi.
- Database hatalarının istemciye ayrıntılı dönmesi reddedildi; ayrıntı yalnızca server log'unda,
  istemciye genel hata mesajı gider.
- Admin API için başlangıçta statik tutulan bilgiler son güvenlik kontrolünde environment secret'a
  taşındı; sabit süreli credential karşılaştırması korundu. İlk SMTP/Mailpit yaklaşımı kaldırıldı;
  yanıt akışı istemcide `mailto:` olarak sınırlandı.
- Uygulama ve yerel runtime kimliği kullanıcı yönlendirmesiyle `Ent Challange` olarak değiştirildi;
  mevcut GitHub repo yolları dış entegrasyonları bozmamak için korundu.
- Yönetim talepleri CRUD akışına genişletildi. `mailto:` gönderimi doğrulanamadığından otomatik cevap
  kaydı yerine kalıcı `Yeni`, `Okundu`, `Cevaplandı` durumları ve onaylı silme tercih edildi.
- Son değerlendirme kontrolünde admin bilgileri zorunlu environment secret'larına taşındı ve
  `/health` yanıtı yalnız API değil PostgreSQL erişimini de doğrulayacak şekilde değiştirildi.
- İlk fazda kullanıcı tablosu ve oturum açılmamıştı. İkinci fazda hesap, hash'lenmiş parola,
  oturum ve görev yetkileri eklendi. Self servis parola sıfırlama, 2FA ve özel SMTP hâlâ kapsam dışı;
  eski hizmet talebi yanıtı `mailto:` olarak kalır.
- Sunucu taraflı sayfalama ve gelişmiş arama, veri hacmi ve arama gereksinimleri netleşmeden indeks,
  algoritma ve cache tercihi yapmak spekülatif olacağı için uygulanmadı.

## Doğrulama kaydı

- İlk HTTP iskeletinde shutdown context için yanlış paket çağrısı test build'inde yakalandı ve
  `context.Background()` ile düzeltildi.
- pgx'in güncel sürümünün Go 1.25 gerektirdiği dependency çözümlemesinde görüldü; Go image ve
  `go.mod` birlikte 1.25'e yükseltildi.
- Handler testleri geçerli kayıt, tüm alan hataları, database hatasında başarı dönmeme ve CORS
  origin kontrolünü kapsıyor.
- Docker Compose ile PostgreSQL ve API birlikte başlatıldı; healthcheck geçti.
- Gerçek POST isteği `201 Created` döndürdü ve satır `service_requests` tablosunda sorgulandı.
- Docker Desktop testinde harici diskteki migration bind mount'unun boş bağlandığı ve gerçek form
  isteğinin `500` aldığı görüldü. Migration dosyaları PostgreSQL imajına alındı; idempotent migration
  servisi mevcut ve yeni volume'lerde API başlamadan önce çalışacak şekilde doğrulandı.
- Yetkisiz admin isteğinin `401` aldığı; doğru Basic credentials ile liste ve detay endpoint'lerinin
  çalıştığı Go testleri ve gerçek Docker API istekleriyle doğrulandı.
- Durum güncelleme, geçersiz durum reddi ve silme endpoint'leri Go handler testleriyle doğrulandı.
- İlk CRUD E2E çalıştırmasında PostgreSQL'in durum parametresini `CASE` içinde belirsiz tür olarak
  değerlendirdiği ve tarayıcı CORS politikasında `PATCH`/`DELETE` izinlerinin eksik olduğu görüldü.
  Açık SQL cast'i ve daraltılmış method allowlist ile düzeltildi; durumun API restart'ından sonra
  korunduğu ve silmenin `204` döndürdüğü gerçek PostgreSQL üzerinde doğrulandı.
- Database health başarısızlığının `503` döndürmesi handler testiyle doğrulandı.

## Görev dağılımı

Çalışma tek Codex oturumunda, alt ajan kullanılmadan gerçekleştirildi.
