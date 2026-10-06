#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT

# Inspect the real targets without starting or replacing any database.
for target in postgres mssql; do
    case "$target" in
    postgres) port=5432; dsn=postgres://localhost/fixture ;;
    mssql) port=1433; dsn='sqlserver://localhost?database=fixture' ;;
    esac
    output=$(make --silent --dry-run -C "$root" -o dev \
        "dev-$target" DEV_CONTAINER_ENGINE=podman)
    grep -Fq -- "-p 127.0.0.1:$port:$port" <<<"$output" || {
        echo "FAIL: $target must publish only on loopback" >&2
        exit 1
    }

    # Missing credentials must fail before any container can be replaced.
    if output=$(env -u DURPDEPLOY_DB -u POSTGRES_PASSWORD -u MSSQL_SA_PASSWORD \
        make --silent -C "$root" "dev-$target" DEV_CONTAINER_ENGINE=false 2>&1); then
        echo "FAIL: $target accepted missing credentials" >&2
        exit 1
    fi
    grep -Fq 'Set ' <<<"$output"
    if grep -Eq -- '-P |POSTGRES_PASSWORD=|MSSQL_SA_PASSWORD=' <<<"$output"; then
        echo "FAIL: $target exposes a password argument" >&2
        exit 1
    fi

    if output=$(env -u DURPDEPLOY_DB bash \
        "$root/scripts/e2e_db_test.sh" "$target" 2>&1); then
        echo "FAIL: $target accepted an implicit database DSN" >&2
        exit 1
    fi
    grep -Fq 'Set DURPDEPLOY_DB' <<<"$output"

    # An explicit DSN must pass validation and reach the server health check.
    mkdir -p "$tmp/bin"
    printf '#!/usr/bin/env bash\nexit 1\n' > "$tmp/bin/curl"
    chmod +x "$tmp/bin/curl"
    if output=$(PATH="$tmp/bin:$PATH" DURPDEPLOY_DB="$dsn" SQLCMDPASSWORD=contract-only bash \
        "$root/scripts/e2e_db_test.sh" "$target" 2>&1); then
        echo "FAIL: $target ignored the unavailable test server" >&2
        exit 1
    fi
    grep -Fq 'server is unavailable' <<<"$output"

    if [[ "$target" == mssql ]]; then
        if output=$(env -u SQLCMDPASSWORD PATH="$tmp/bin:$PATH" \
            DURPDEPLOY_DB="$dsn" MSSQL_PASSWORD=contract-only bash \
            "$root/scripts/e2e_db_test.sh" sqlserver 2>&1); then
            echo 'FAIL: SQL Server ignored the unavailable test server' >&2
            exit 1
        fi
        grep -Fq 'server is unavailable' <<<"$output"
    fi
done

printf '%s\n' 'Development database boundary contract: PASS'
