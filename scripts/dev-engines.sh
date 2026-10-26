#!/usr/bin/env bash
# Local database engines for development where Docker is missing (Claude's
# sandbox is Ubuntu 24.04 without Docker). CI uses scripts/ci-engine.sh.
#
#   scripts/dev-engines.sh setup     install and initialise everything (once)
#   scripts/dev-engines.sh pg        run PostgreSQL 16 in the foreground
#   scripts/dev-engines.sh mysql     run MySQL 8.0 in the foreground
#   scripts/dev-engines.sh mariadb   run MariaDB 10.11 in the foreground
#   scripts/dev-engines.sh env       print the BARESQL_TEST_* variables
#   scripts/dev-engines.sh run CMD   start all three, run CMD with the
#                                    variables set, stop them again
#
# Either start each server as a background job and
# eval "$(scripts/dev-engines.sh env)", or wrap a command in "run".
set -euo pipefail
D=${BARESQL_DEV_DIR:-/var/tmp/baresql-dev}
PGBIN=/usr/lib/postgresql/16/bin
MARIADB=/opt/mariadb

setup() {
  local need=()
  [ -x $PGBIN/postgres ] || need+=(postgresql-16)
  command -v mysqld >/dev/null || need+=(mysql-server-core-8.0 mysql-client-core-8.0)
  command -v sqlite3 >/dev/null || need+=(sqlite3)
  if [ ${#need[@]} -gt 0 ]; then
    apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${need[@]}"
  fi
  # MariaDB can't be installed next to MySQL, so its packages are unpacked
  # into /opt/mariadb instead.
  if [ ! -x $MARIADB/usr/sbin/mariadbd ]; then
    mkdir -p $D/mariadb-debs && (cd $D/mariadb-debs && apt-get download mariadb-server-core mariadb-client-core liburing2)
    for f in $D/mariadb-debs/*.deb; do dpkg -x "$f" $MARIADB; done
  fi

  # PostgreSQL 16 with TLS (a self-signed certificate for localhost, which
  # doubles as its own CA for the verify-full tests).
  if [ ! -f $D/pg16/data/PG_VERSION ]; then
    mkdir -p $D/pg16 $D/pgsock && chown postgres:postgres $D/pg16 $D/pgsock
    su postgres -s /bin/bash -c "$PGBIN/initdb -D $D/pg16/data -U postgres --auth-local=trust --auth-host=md5 --encoding=UTF8 --locale=C.UTF-8 >/dev/null"
    openssl req -x509 -newkey rsa:2048 -nodes -keyout $D/pg16/server.key -out $D/pg16/server.crt -days 3650 \
      -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" 2>/dev/null
    chown postgres:postgres $D/pg16/server.key $D/pg16/server.crt && chmod 600 $D/pg16/server.key
    cat >> $D/pg16/data/postgresql.conf <<CONF
listen_addresses = '127.0.0.1'
port = 5432
unix_socket_directories = '$D/pgsock'
ssl = on
ssl_cert_file = '$D/pg16/server.crt'
ssl_key_file = '$D/pg16/server.key'
max_connections = 200
fsync = off
synchronous_commit = off
full_page_writes = off
log_min_messages = warning
CONF
    # baresql_cleartext exists so the client's cleartext password path has a
    # real server to talk to; every other TCP login uses md5 (or SCRAM when
    # the stored password is a SCRAM verifier).
    cat > $D/pg16/data/pg_hba.conf <<HBA
local all all trust
host all baresql_cleartext 127.0.0.1/32 password
host all all 127.0.0.1/32 md5
HBA
  fi

  if [ ! -d $D/mysql80/data/mysql ]; then
    mkdir -p $D/mysql80/data $D/mysql80/run && chown -R mysql:mysql $D/mysql80
    mysqld --initialize-insecure --user=mysql --datadir=$D/mysql80/data 2>$D/mysql80/init.log
  fi
  if [ ! -d $D/mariadb/data/mysql ]; then
    mkdir -p $D/mariadb/data $D/mariadb/run && chown -R mysql:mysql $D/mariadb
    LD_LIBRARY_PATH=$MARIADB/usr/lib/x86_64-linux-gnu $MARIADB/usr/bin/mariadb-install-db --no-defaults \
      --user=mysql --basedir=$MARIADB/usr --datadir=$D/mariadb/data --auth-root-authentication-method=normal >$D/mariadb/init.log 2>&1
  fi
  echo "setup done; start the servers, then run: $0 passwords"
}

# Sets the root passwords and removes MariaDB's anonymous accounts (they
# shadow user@'%' logins over TCP). Run once, with both servers running.
passwords() {
  mysql --socket=$D/mysql80/run/mysqld.sock -uroot -e \
    "ALTER USER 'root'@'localhost' IDENTIFIED WITH caching_sha2_password BY 'root'; CREATE USER IF NOT EXISTS 'root'@'%' IDENTIFIED WITH caching_sha2_password BY 'root'; GRANT ALL ON *.* TO 'root'@'%' WITH GRANT OPTION;" 2>/dev/null ||
    echo "mysql: root already has a password"
  LD_LIBRARY_PATH=$MARIADB/usr/lib/x86_64-linux-gnu $MARIADB/usr/bin/mariadb --no-defaults --socket=$D/mariadb/run/mariadb.sock -uroot -e \
    "DROP USER IF EXISTS ''@'localhost'; DROP USER IF EXISTS ''@'$(hostname)'; ALTER USER 'root'@'localhost' IDENTIFIED BY 'root'; CREATE USER IF NOT EXISTS 'root'@'%' IDENTIFIED BY 'root'; GRANT ALL ON *.* TO 'root'@'%' WITH GRANT OPTION;" 2>/dev/null ||
    echo "mariadb: root already has a password"
  su postgres -s /bin/bash -c "psql -h $D/pgsock -U postgres -qc \"ALTER USER postgres PASSWORD 'postgres'\""
}

# run starts the three servers for the length of one command.
# up reports whether all three servers accept logins.
up() {
  $PGBIN/pg_isready -q -h 127.0.0.1 -p 5432 &&
    mysql -h127.0.0.1 -P3306 -uroot -proot -e 'select 1' >/dev/null 2>&1 &&
    mysql -h127.0.0.1 -P3307 -uroot -proot -e 'select 1' >/dev/null 2>&1
}

# stop_servers shuts the three servers down and waits until they are gone.
stop_servers() {
  su postgres -s /bin/bash -c "$PGBIN/pg_ctl -D $D/pg16/data stop -m fast" >/dev/null 2>&1 || true
  mysqladmin --socket=$D/mysql80/run/mysqld.sock -uroot -proot shutdown 2>/dev/null || true
  LD_LIBRARY_PATH=$MARIADB/usr/lib/x86_64-linux-gnu $MARIADB/usr/bin/mariadb --no-defaults \
    --socket=$D/mariadb/run/mariadb.sock -uroot -proot -e SHUTDOWN 2>/dev/null || true
  for _ in $(seq 1 60); do
    [ -e $D/mysql80/run/mysqld.pid ] || [ -e $D/mariadb/run/mariadb.pid ] || [ -e $D/pg16/data/postmaster.pid ] || return 0
    sleep 0.5
  done
}

# run CMD: runs CMD with the three servers up and BARESQL_TEST_* set. Runs
# may overlap (two sessions or agents at once): the first starts the
# servers, the last one to finish stops them. A lock keeps starting and
# stopping apart; $D/runs holds one file per run in progress.
run() {
  mkdir -p $D/runs
  exec 9>$D/run.lock
  flock 9
  echo $$ > $D/runs/$$
  if ! up; then
    "$0" pg >>$D/pg16.log 2>&1 &
    "$0" mysql >/dev/null 2>&1 &
    "$0" mariadb >/dev/null 2>&1 &
  fi
  flock -u 9
  finish() {
    flock 9
    rm -f $D/runs/$$
    local f others=0
    for f in $D/runs/*; do
      [ -e "$f" ] || continue
      if kill -0 "${f##*/}" 2>/dev/null; then others=1; else rm -f "$f"; fi
    done
    [ "$others" = 0 ] && stop_servers
    flock -u 9
  }
  trap finish EXIT
  for _ in $(seq 1 60); do
    if up; then
      eval "$(env_vars)"
      "$@"
      return
    fi
    sleep 1
  done
  echo "dev-engines: the servers did not start" >&2
  return 1
}

env_vars() {
  cat <<ENV
export BARESQL_TEST_PG='postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable'
export BARESQL_TEST_PG_CA='$D/pg16/server.crt'
export BARESQL_TEST_PG_SOCKET='$D/pgsock'
export BARESQL_TEST_MYSQL='mysql://root:root@127.0.0.1:3306/'
export BARESQL_TEST_MYSQL_SOCKET='$D/mysql80/run/mysqld.sock'
export BARESQL_TEST_MARIADB='mariadb://root:root@127.0.0.1:3307/'
ENV
}

case ${1:-} in
setup) setup ;;
run) shift; run "$@" ;;
passwords) passwords ;;
pg) exec su postgres -s /bin/bash -c "exec $PGBIN/postgres -D $D/pg16/data" ;;
mysql)
  exec mysqld --user=mysql --datadir=$D/mysql80/data --port=3306 --bind-address=127.0.0.1 \
    --socket=$D/mysql80/run/mysqld.sock --pid-file=$D/mysql80/run/mysqld.pid --mysqlx=OFF \
    --log-error=$D/mysql80/error.log ;;
mariadb)
  LD_LIBRARY_PATH=$MARIADB/usr/lib/x86_64-linux-gnu exec $MARIADB/usr/sbin/mariadbd --no-defaults --user=mysql \
    --basedir=$MARIADB/usr --datadir=$D/mariadb/data --port=3307 --bind-address=127.0.0.1 \
    --socket=$D/mariadb/run/mariadb.sock --pid-file=$D/mariadb/run/mariadb.pid \
    --log-error=$D/mariadb/error.log --lc-messages-dir=$MARIADB/usr/share/mysql ;;
env) env_vars ;;
*) sed -n '2,15p' "$0"; exit 2 ;;
esac
