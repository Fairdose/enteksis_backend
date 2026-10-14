#!/bin/sh
set -eu

psql -h db -v ON_ERROR_STOP=1 -c 'CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())'

for file in /migrations/*.sql; do
  name=${file##*/}
  applied=$(psql -h db -v ON_ERROR_STOP=1 -At -c "SELECT 1 FROM schema_migrations WHERE name = '$name'")
  if [ "$applied" = 1 ]; then
    printf 'Geçiş zaten uygulandı: %s\n' "$name"
    continue
  fi
  printf 'Geçiş uygulanıyor: %s\n' "$name"
  staged=$(mktemp)
  trap 'rm -f "$staged"' EXIT
  sed '/^BEGIN;$/d; /^COMMIT;$/d' "$file" > "$staged"
  printf "\nINSERT INTO schema_migrations (name) VALUES ('%s');\n" "$name" >> "$staged"
  psql -h db -v ON_ERROR_STOP=1 -1 -f "$staged"
  rm -f "$staged"
  trap - EXIT
done
