# ent-challange Backend

ent-challange Benim Masam V0.1 ve hizmet talep formu için Go HTTP API'si ve PostgreSQL veritabanı.

## Gereksinimler

- Docker Engine 24+ ve Docker Compose v2
- Linux/macOS üzerinde Bash; Windows üzerinde Git Bash veya WSL
- Repoların aynı üst dizinde `enteksis_client` ve `enteksis_backend` adlarıyla bulunması
- Docker kullanmadan geliştirmek isteyenler için Go 1.25+

## Hızlı başlangıç

Client, API ve PostgreSQL'i birlikte başlatın:

```sh
cp .env.example .env
# .env içindeki admin kimlik bilgilerini teslim kanalındaki değerlerle değiştirin.
./run.sh
```

Windows'ta Git Bash veya WSL terminalinden `bash ./run.sh` kullanın. Aynı `run.sh` dosyası
client deposunda da bulunur ve aynı Compose projesini yönetir.

`migrations/001` ve `002` eski hizmet talebi şemasını korur. `003`–`007`, ikinci fazın kanonik v2.1.1–v2.1.5 geçişleridir; kaynak paketle SHA-256 eşleşir. `008`, hesap oturumları için uygulamaya ait ek tablodur. Geçişler `schema_migrations` kaydıyla yalnız bir kez ve her dosya atomik olarak uygulanır. Veriler `postgres-data` volume'ünde kalır.

- Client: `http://localhost:5173`
- API: `http://localhost:8080`
- PostgreSQL bağlantısını da sınayan sağlık kontrolü: `GET http://localhost:8080/health`
- PostgreSQL: `localhost:5432`
- Admin arayüzü: `http://localhost:5173/admin`
- Benim Masam: `http://localhost:5173/admin/masa`

Kontrol etmek için:

```sh
curl http://localhost:8080/health
```

Servisleri durdurmak ve veriyi korumak için `./run.sh stop`, loglar için `./run.sh logs`, mevcut
durum için `./run.sh status` kullanın. Script veri volume'ünü silen bir komut içermez.

## Yapılandırma

Varsayılan geliştirme değerleri `compose.yaml` içinde güvenli olmayan yerel değerlerdir. İsterseniz
`.env.example` dosyasını `.env` olarak kopyalayıp portları, veritabanı parolasını ve zorunlu admin
kimlik bilgilerini değiştirmelisiniz.

| Değişken | Varsayılan | Açıklama |
| --- | --- | --- |
| `CLIENT_PORT` | `5173` | Host üzerindeki frontend portu |
| `HTTP_PORT` | `8080` | Host üzerindeki API portu |
| `POSTGRES_PORT` | `5432` | Host üzerindeki PostgreSQL portu |
| `POSTGRES_DB` | `ent_challange` | Veritabanı adı |
| `POSTGRES_USER` | `ent_challange` | Veritabanı kullanıcısı |
| `POSTGRES_PASSWORD` | `ent_challange_dev_password` | Yalnızca yerel geliştirme parolası |
| `ALLOWED_ORIGINS` | `http://localhost:5173` | Virgülle ayrılmış CORS origin listesi |
| `ADMIN_USERNAME` | — | Admin giriş e-postası; zorunlu secret |
| `ADMIN_PASSWORD` | — | Admin parolası; zorunlu secret |

`CLIENT_PORT` değiştirildiğinde `ALLOWED_ORIGINS` değerini de yeni origin ile birlikte değiştirin.

Admin kimlik bilgileri environment üzerinden alınır ve repoya yazılmaz. Değerlendirme bilgileri
teslim kanalıyla ayrıca paylaşılır.

## Endpoint'ler

### `POST /api/v1/requests`

```json
{
  "name": "Ada Lovelace",
  "email": "ada@example.com",
  "serviceType": "software-development",
  "description": "Yeni bir web uygulaması geliştirmek istiyorum."
}
```

Kayıt ancak PostgreSQL insert işlemi başarıyla tamamlanırsa `201 Created` döner. Alan hataları
`422 Unprocessable Entity`, geçersiz JSON `400 Bad Request`, kayıt hatası ise ayrıntı sızdırmadan
`500 Internal Server Error` döndürür.

### Admin endpoint'leri

- `GET /api/v1/admin/requests`
- `GET /api/v1/admin/requests/{id}`
- `PATCH /api/v1/admin/requests/{id}` — `new`, `read` veya `replied` durumunu kaydeder
- `DELETE /api/v1/admin/requests/{id}`

Admin endpoint'leri HTTP Basic kimlik doğrulaması gerektirir. Kimlik bilgileri yalnız environment
üzerinden sağlanır; production trafiği HTTPS üzerinden sunulur.

### Benim Masam V0.1

- `POST /api/v1/auth/login`, `/auth/refresh`, `/auth/logout`: ekip hesapları ve yenilenen oturumlar.
- `GET /api/v1/desk/context`, `/my-dashboard`, `/tasks/{id}`: çalışma alanı, sekiz masa grubu ve ayrıntı.
- `POST /api/v1/actors`, `/projects`, `/tasks`: yönetici tarafından ekip, proje ve görev oluşturma.
- `POST /api/v1/tasks/{id}/ready`, `/start`, `/submit`, `/cancel`, `/blockers`, `/blockers/{blocker_id}/resolve`, `/reassign`; `POST /api/v1/submissions/{id}/review`: ayrı ve yetkilendirilmiş durum geçişleri.
- `POST /api/v1/tasks/{id}/assignments`, `/assignments/{assignment_id}/end`, `/checklist`; `PATCH /api/v1/tasks/{id}/checklist/{item_id}`: katılım ve kontrol listesi.
- `POST /api/v1/permission-grants`, `GET /api/v1/permission-grants`, `POST /api/v1/permission-grants/{id}/revoke`: süreli ve geri alınabilir görev yetkileri.

Görev mutasyonları `Idempotency-Key` başlığı ister. İstemci işlem anahtarını her yeni eylem için üretir; tekrar gönderimde aynı yanıt döner. Yeniden atama gövdesindeki `expected_primary_assignment_id` güncel değilse `409 stale_assignment` ve `current_assignment_id` döner. Görev ve üyelikler fiziksel silinmez; olaylar `audit_events` ve `outbox_events` kayıtlarına aynı transaction içinde yazılır. Benim Masam yetkisi aktif rol ve atamalardan sunucuda hesaplanır. Çalışma alanı günü `Europe/Istanbul` sınırlarıyla değerlendirilir.

## Test

Yerel Go kurulumu ile:

```sh
go test ./...
```

Go kurulu değilse:

```sh
docker compose build api
```

API image build aşaması `go test ./...` çalıştırır ve testler başarısızsa image üretmez.

## Bilinen eksikler

- Eski talep yönetimi HTTP Basic kullanır. Benim Masam için veritabanı oturumu ve PBKDF2 parola özeti vardır; self servis parola sıfırlama ve ikinci faktör bu sürümde yoktur.
- Kanonik sözleşmedeki takım yönetimi, genel proje/görev düzenleme, rol bağlama ve yorum/ek dosya uçları henüz ürün arayüzüne açılmamıştır. V0.1 gösterimi görevden teslim kararına uzanan dikey akışa odaklanır.
- Public form production Nginx üzerinde IP bazlı rate limiting ile korunur; yerel Compose ortamında
  bu reverse proxy katmanı bulunmaz.
- Production Nginx admin API denemelerine daha sıkı, IP bazlı ayrı bir rate limit uygular.
- E-posta yanıtı frontend'de `mailto:` ile cihazın posta uygulamasına aktarılır; gönderim durumu
  otomatik doğrulanamaz. Mevcut SMTP servisi özel altyapıda çalıştığı için bu projeye açılmadı.
  Mailpit ile test edilebilirdi ancak sunucu güvenliği ve mTLS entegrasyonu challenge kapsamını
  genişleteceğinden uygulanmadı; yönetici durumu açıkça `Cevaplandı` olarak işaretler.
- API liste endpoint'i küçük challenge veri setini tek seferde döndürür; sunucu taraflı sayfalama ve
  gelişmiş arama yoktur. Çözüm beklenen hacim ve aranan alanlara göre indeks, arama algoritması ve
  gerekirse cache seçimi gerektirdiğinden gereksinim oluşmadan spekülatif altyapı eklenmedi.
