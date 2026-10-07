# Ent Challange Backend

Ent Challange hizmet talep formu için Go HTTP API'si ve PostgreSQL veritabanı.

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

`migrations/001_create_service_requests.sql` PostgreSQL imajına alınır ve idempotent migration
servisi tarafından her başlangıçta uygulanır. Bu yaklaşım Docker Desktop'ın harici disk bind mount
kısıtlarından etkilenmez. Veriler `postgres-data` adlı Docker volume'ünde kalıcı olarak saklanır.

- Client: `http://localhost:5173`
- API: `http://localhost:8080`
- PostgreSQL bağlantısını da sınayan sağlık kontrolü: `GET http://localhost:8080/health`
- PostgreSQL: `localhost:5432`
- Admin arayüzü: `http://localhost:5173/admin`

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

- Admin kimlik doğrulaması yerel challenge kapsamı için HTTP Basic kullanır; production için
  kullanıcı tablosu, parola hash'i, güvenli session ve yetkilendirme rolleri gerekir.
- Public form production Nginx üzerinde IP bazlı rate limiting ile korunur; yerel Compose ortamında
  bu reverse proxy katmanı bulunmaz.
- Production Nginx admin API denemelerine daha sıkı, IP bazlı ayrı bir rate limit uygular.
- E-posta yanıtı frontend'de `mailto:` ile cihazın posta uygulamasına aktarılır; gönderim durumu
  otomatik doğrulanamaz. Yönetici, talebi gönderimden sonra açıkça `Cevaplandı` olarak işaretler.
