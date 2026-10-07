# AI_LOG

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
- Kullanıcı tablosu, session, RBAC ve parola sıfırlama challenge kapsamını ve bütçelenemeyen
  operasyon yükünü büyütmemek için eklenmedi. Genişletilmiş üretim tasarımında hash'lenmiş parola,
  SMTP, reCAPTCHA ve 2FA birlikte ele alınmalıydı. Özel SMTP servisi açılmadığı; Mailpit, mTLS ve
  sunucu güvenliği de kapsamı büyüteceği için `mailto:` akışı korundu.
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
