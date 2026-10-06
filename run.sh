#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)"
PROJECT_ROOT="$(cd -- "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd -P)"
CLIENT_DIR="$PROJECT_ROOT/enteksis_client"
BACKEND_DIR="$PROJECT_ROOT/enteksis_backend"
COMPOSE_FILE="$BACKEND_DIR/compose.yaml"

fail() {
  printf 'Hata: %s\n' "$*" >&2
  exit 1
}

select_engine() {
  if [[ -n "${CONTAINER_ENGINE:-}" ]]; then
    command -v "$CONTAINER_ENGINE" >/dev/null 2>&1 ||
      fail "CONTAINER_ENGINE='$CONTAINER_ENGINE' bulunamadı."
    "$CONTAINER_ENGINE" info >/dev/null 2>&1 ||
      fail "$CONTAINER_ENGINE çalışıyor ancak daemon erişilebilir değil."
    printf '%s' "$CONTAINER_ENGINE"
    return
  fi

  if command -v docker >/dev/null 2>&1; then
    docker info >/dev/null 2>&1 ||
      fail "Docker bulundu ancak çalışmıyor. Docker Desktop veya Docker Engine'i başlatın."
    printf 'docker'
    return
  fi

  if command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
    printf 'podman'
    return
  fi

  fail "Docker bulunamadı. Docker Desktop/Engine kurup tekrar deneyin."
}

usage() {
  cat <<'EOF'
Kullanım: ./run.sh [komut]

Komutlar:
  start, up   Client, API ve PostgreSQL'i build edip arka planda başlatır (varsayılan).
  stop, down  Container'ları durdurur; PostgreSQL volume'ünü korur.
  restart     Tüm servisleri yeniden build edip başlatır.
  logs        Tüm servislerin loglarını takip eder.
  status      Container durumlarını gösterir.
  help        Bu yardımı gösterir.

Ortam değişkenleri .env ile veya komut önünde verilebilir:
  CLIENT_PORT, HTTP_PORT, POSTGRES_PORT, POSTGRES_DB,
  POSTGRES_USER, POSTGRES_PASSWORD, ALLOWED_ORIGINS
EOF
}

[[ -d "$CLIENT_DIR" ]] ||
  fail "Kardeş dizin bulunamadı: $CLIENT_DIR"
[[ -d "$BACKEND_DIR" ]] ||
  fail "Kardeş dizin bulunamadı: $BACKEND_DIR"
[[ -f "$COMPOSE_FILE" ]] ||
  fail "Compose dosyası bulunamadı: $COMPOSE_FILE"

ENGINE="$(select_engine)"
"$ENGINE" compose version >/dev/null 2>&1 ||
  fail "'$ENGINE compose' kullanılamıyor. Compose v2 kurulumunu kontrol edin."

COMPOSE=(
  "$ENGINE" compose
  --project-name ent-challange
  --file "$COMPOSE_FILE"
)

cd -- "$BACKEND_DIR"

COMMAND="${1:-start}"

case "$COMMAND" in
  start|up)
    "${COMPOSE[@]}" up --build --detach --remove-orphans
    "${COMPOSE[@]}" ps
    printf '\nEnt Challange hazır:\n'
    printf '  Client:     http://localhost:%s\n' "${CLIENT_PORT:-5173}"
    printf '  API:        http://localhost:%s\n' "${HTTP_PORT:-8080}"
    printf '  PostgreSQL: localhost:%s\n' "${POSTGRES_PORT:-5432}"
    ;;
  stop|down)
    "${COMPOSE[@]}" down
    printf 'Servisler durduruldu; PostgreSQL volume korundu.\n'
    ;;
  restart)
    "${COMPOSE[@]}" down
    "${COMPOSE[@]}" up --build --detach --remove-orphans
    "${COMPOSE[@]}" ps
    ;;
  logs)
    "${COMPOSE[@]}" logs --follow
    ;;
  status)
    "${COMPOSE[@]}" ps
    ;;
  help|-h|--help)
    usage
    ;;
  *)
    usage >&2
    fail "Bilinmeyen komut: $COMMAND"
    ;;
esac
