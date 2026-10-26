#!/usr/bin/env bash
# Starts one database engine in Docker for CI and waits until it accepts
# logins. Writes the BARESQL_TEST_* variables to $GITHUB_ENV (or prints them
# when run elsewhere).
#
#   scripts/ci-engine.sh postgres 16
#   scripts/ci-engine.sh mysql 8.4
#   scripts/ci-engine.sh mariadb 11.8
set -euo pipefail
engine=$1 version=$2
out=${GITHUB_ENV:-/dev/stdout}

wait_for() { # name, command...
  local name=$1; shift
  for _ in $(seq 1 120); do
    if "$@" >/dev/null 2>&1; then echo "$name is up"; return 0; fi
    sleep 2
  done
  echo "::error::$name did not start: $(docker logs --tail 20 "$name" 2>&1 | tr '\n' ' ')"
  exit 1
}

# pull fetches an image, trying three times: Docker Hub fails now and then,
# and a failed docker run only says "exit code 125".
pull() {
  local err
  for i in 1 2 3; do
    if err=$(docker pull -q "$1" 2>&1 >/dev/null); then return 0; fi
    echo "docker pull $1 failed (try $i): $err"
    sleep $((i * 10))
  done
  echo "::error::docker pull $1 failed three times: $(echo "$err" | tr '\n' ' ')"
  exit 1
}

# start runs a container and annotates docker's own error.
start() {
  local err
  if ! err=$(docker run -d "$@" 2>&1 >/dev/null); then
    echo "::error::docker run failed: $(echo "$err" | tr '\n' ' ')"
    exit 1
  fi
}

case $engine in
postgres)
  # A throwaway CA and a certificate for localhost, so the client's
  # verify-full path is tested against a real server.
  certs="$PWD/.ci-certs"
  mkdir -p "$certs"
  openssl req -x509 -newkey rsa:2048 -nodes -days 2 -subj "/CN=BareSQL CI CA" \
    -keyout "$certs/ca.key" -out "$certs/ca.crt" 2>/dev/null
  openssl req -newkey rsa:2048 -nodes -subj "/CN=localhost" \
    -keyout "$certs/server.key" -out "$certs/server.csr" 2>/dev/null
  printf 'subjectAltName=DNS:localhost,IP:127.0.0.1\n' > "$certs/san.ext"
  openssl x509 -req -in "$certs/server.csr" -CA "$certs/ca.crt" -CAkey "$certs/ca.key" -CAcreateserial \
    -days 2 -extfile "$certs/san.ext" -out "$certs/server.crt" 2>/dev/null
  # The server runs as uid 999 and refuses a key others can read.
  sudo chown 999:999 "$certs/server.key" "$certs/server.crt"
  sudo chmod 600 "$certs/server.key"
  # md5 in pg_hba lets both md5 and SCRAM passwords log in, so the client is
  # tested with both.
  pull "postgres:$version"
  start --name baresql-pg -p 5432:5432 \
    -e POSTGRES_PASSWORD=postgres -e POSTGRES_HOST_AUTH_METHOD=md5 \
    -v "$certs:/certs:ro" "postgres:$version" \
    -c ssl=on -c ssl_cert_file=/certs/server.crt -c ssl_key_file=/certs/server.key \
    -c max_connections=200 -c fsync=off
  wait_for baresql-pg docker exec baresql-pg pg_isready -h 127.0.0.1 -U postgres
  echo "BARESQL_TEST_PG=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" >> "$out"
  echo "BARESQL_TEST_PG_CA=$certs/ca.crt" >> "$out"
  ;;
mysql)
  extra=()
  # 8.4 ships mysql_native_password switched off; turn it on so that plugin
  # is still tested. 9.x and later have no such plugin.
  [ "$version" = 8.4 ] && extra=(--mysql-native-password=ON)
  pull "mysql:$version"
  start --name baresql-mysql -p 3306:3306 -e MYSQL_ROOT_PASSWORD=root \
    "mysql:$version" "${extra[@]}"
  wait_for baresql-mysql docker exec baresql-mysql mysql -h127.0.0.1 -uroot -proot -e 'select 1'
  echo "BARESQL_TEST_MYSQL=mysql://root:root@127.0.0.1:3306/" >> "$out"
  ;;
mariadb)
  pull "mariadb:$version"
  start --name baresql-mariadb -p 3307:3306 -e MARIADB_ROOT_PASSWORD=root \
    "mariadb:$version"
  wait_for baresql-mariadb docker exec baresql-mariadb mariadb -h127.0.0.1 -uroot -proot -e 'select 1'
  echo "BARESQL_TEST_MARIADB=mariadb://root:root@127.0.0.1:3307/" >> "$out"
  ;;
*)
  echo "::error::unknown engine $engine"; exit 2 ;;
esac
