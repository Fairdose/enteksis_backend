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
./run.sh
```

Windows'ta Git Bash veya WSL terminalinden `bash ./run.sh` kullanın. Aynı `run.sh` dosyası
client deposunda da bulunur ve aynı Compose projesini yönetir.

`migrations/001_create_service_requests.sql` PostgreSQL imajına alınır ve idempotent migration
servisi tarafından her başlangıçta uygulanır. Bu yaklaşım Docker Desktop'ın harici disk bind mount
kısıtlarından etkilenmez. Veriler `postgres-data` adlı Docker volume'ünde kalıcı olarak saklanır.

- Client: `http://localhost:5173`
- API: `http://localhost:8080`
- Sağlık kontrolü: `GET http://localhost:8080/health`
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
`.env.example` dosyasını `.env` olarak kopyalayıp portları ve parolayı değiştirebilirsiniz.

| Değişken | Varsayılan | Açıklama |
| --- | --- | --- |
| `CLIENT_PORT` | `5173` | Host üzerindeki frontend portu |
| `HTTP_PORT` | `8080` | Host üzerindeki API portu |
| `POSTGRES_PORT` | `5432` | Host üzerindeki PostgreSQL portu |
| `POSTGRES_DB` | `ent_challange` | Veritabanı adı |
| `POSTGRES_USER` | `ent_challange` | Veritabanı kullanıcısı |
| `POSTGRES_PASSWORD` | `ent_challange_dev_password` | Yalnızca yerel geliştirme parolası |
| `ALLOWED_ORIGINS` | `http://localhost:5173` | Virgülle ayrılmış CORS origin listesi |

`CLIENT_PORT` değiştirildiğinde `ALLOWED_ORIGINS` değerini de yeni origin ile birlikte değiştirin.

Admin girişi challenge gereği statiktir: kullanıcı adı `admin`, şifre `123456admin`. Bu yaklaşım
üretim ortamı için uygun değildir.

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

Admin endpoint'leri HTTP Basic kimlik doğrulaması gerektirir. Compose varsayılanları yalnızca yerel
değerlendirme içindir; ortak veya production ortamında güçlü ve benzersiz değerlerle değiştirilmesi,
trafiğin HTTPS üzerinden sunulması gerekir.

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

- Genel kullanıma açık bir form için production ortamında IP bazlı rate limiting eklenmelidir.
- Admin kimlik doğrulaması yerel challenge kapsamı için HTTP Basic kullanır; production için
  kullanıcı tablosu, parola hash'i, güvenli session ve yetkilendirme rolleri gerekir.
- E-posta yanıtı frontend'de `mailto:` ile cihazın posta uygulamasına aktarılır; gönderim durumu
  sunucu tarafından izlenmez.
