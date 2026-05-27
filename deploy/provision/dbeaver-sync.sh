#!/usr/bin/env bash
# Generates a DBeaver project archive (dexarb-vps.dbp) for the current VPS Postgres,
# into deploy/provision/dbeaver-out/. Import via DBeaver: File -> Import -> DBeaver -> Project.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

CONN_NAME="PG_dexArb_VPS"
OUT_DIR="$SCRIPT_DIR/dbeaver-out"

usage() {
  cat <<EOF
Usage: $0 [--name NAME] [--out DIR]
  --name   DBeaver connection display name (default: $CONN_NAME)
  --out    Output directory (default: $OUT_DIR)
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --name) CONN_NAME="$2"; shift 2 ;;
    --out)  OUT_DIR="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

die() { echo "ERROR: $*" >&2; exit 1; }
log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

ENV_VPS="$SCRIPT_DIR/.env.vps"
ENV_ROOT="$REPO_ROOT/.env"

[[ -f "$ENV_VPS" ]]  || die "missing $ENV_VPS — run deploy/provision/run.sh first"
[[ -f "$ENV_ROOT" ]] || die "missing $ENV_ROOT"

# shellcheck disable=SC1090
set -a; source "$ENV_VPS"; source "$ENV_ROOT"; set +a

: "${VPS_SSH_HOST:?VPS_SSH_HOST not set in $ENV_VPS}"
: "${POSTGRES_DB:?POSTGRES_DB not set in $ENV_ROOT}"
: "${POSTGRES_ADMIN_USER:?POSTGRES_ADMIN_USER not set in $ENV_ROOT}"
: "${POSTGRES_ADMIN_PASSWORD:?POSTGRES_ADMIN_PASSWORD not set in $ENV_ROOT}"

command -v python3 >/dev/null 2>&1 || die "python3 not found on PATH"

mkdir -p "$OUT_DIR"
chmod 700 "$OUT_DIR"

log "Generating DBeaver project archive"
echo "  host    : $VPS_SSH_HOST"
echo "  db      : $POSTGRES_DB"
echo "  user    : $POSTGRES_ADMIN_USER"
echo "  out dir : $OUT_DIR"

DBEAVER_HOST="$VPS_SSH_HOST" \
DBEAVER_DB="$POSTGRES_DB" \
DBEAVER_USER="$POSTGRES_ADMIN_USER" \
DBEAVER_PASSWORD="$POSTGRES_ADMIN_PASSWORD" \
DBEAVER_CONN_NAME="$CONN_NAME" \
DBEAVER_OUT_DIR="$OUT_DIR" \
python3 <<'PY'
import json, os, secrets, time, zipfile
from pathlib import Path

try:
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
except ImportError as e:
    raise SystemExit("ERROR: python3 'cryptography' package is required (pip install cryptography)") from e

host      = os.environ["DBEAVER_HOST"]
database  = os.environ["DBEAVER_DB"]
user      = os.environ["DBEAVER_USER"]
password  = os.environ["DBEAVER_PASSWORD"]
conn_name = os.environ["DBEAVER_CONN_NAME"]
out_dir   = Path(os.environ["DBEAVER_OUT_DIR"])

conn_id = f"postgres-jdbc-{int(time.time()*1000):x}-{secrets.token_hex(8)}"

data_sources = {
    "folders": {},
    "connections": {
        conn_id: {
            "provider": "postgresql",
            "driver": "postgres-jdbc",
            "name": conn_name,
            "save-password": True,
            "configuration": {
                "host": host,
                "port": "5432",
                "database": database,
                "url": f"jdbc:postgresql://{host}:5432/{database}",
                "configurationType": "MANUAL",
                "type": "dev",
                "closeIdleConnection": True,
                "provider-properties": {
                    "@dbeaver-show-non-default-db@": "true",
                },
                "auth-model": "native",
            },
        },
    },
    "connection-types": {
        "dev": {
            "name": "Development",
            "color": "255,255,255",
            "description": "Regular development database",
            "auto-commit": True,
            "confirm-execute": False,
            "confirm-data-change": False,
            "smart-commit": False,
            "smart-commit-recover": True,
            "auto-close-transactions": True,
            "close-transactions-period": 1800,
            "auto-close-connections": True,
            "close-connections-period": 14400,
        },
    },
}

# DBeaver CE static AES-128 key for the local credentials cipher.
# Source: org.jkiss.dbeaver.registry.encode.* in the DBeaver CE codebase.
# File format: 16-byte random IV || AES-128-CBC(PKCS#7 padded plaintext)
AES_KEY = bytes.fromhex("babb4a9f774ab853c96c2d653dfe544a")

creds_plain = json.dumps(
    {conn_id: {"#connection": {"user": user, "password": password}}},
    separators=(",", ":"),
).encode("utf-8")

pad_len = 16 - (len(creds_plain) % 16)
creds_padded = creds_plain + bytes([pad_len]) * pad_len

iv = secrets.token_bytes(16)
enc = Cipher(algorithms.AES(AES_KEY), modes.CBC(iv)).encryptor()
ciphertext = enc.update(creds_padded) + enc.finalize()

ds_bytes = (json.dumps(data_sources, indent="\t") + "\n").encode("utf-8")
cc_bytes = iv + ciphertext

# Bundle a DBeaver project archive (.dbp) — importable via File -> Import -> DBeaver -> Project.
# Layout dictated by org.jkiss.dbeaver.tools.project.ExportConstants:
#   META_FILENAME = "meta.xml"   (zip root)
#   DIR_PROJECTS  = "projects"   (project files go under projects/<name>/...)
project_name = "dexArbTrading-vps-import"
project_xml = (
    '<?xml version="1.0" encoding="UTF-8"?>\n'
    '<projectDescription>\n'
    f'\t<name>{project_name}</name>\n'
    '\t<comment>Imported VPS Postgres connection</comment>\n'
    '\t<projects>\n\t</projects>\n'
    '\t<buildSpec>\n\t</buildSpec>\n'
    '\t<natures>\n'
    '\t\t<nature>org.jkiss.dbeaver.DBeaverNature</nature>\n'
    '\t</natures>\n'
    '</projectDescription>\n'
).encode("utf-8")
# meta.xml must declare every file/folder as nested <resource> elements,
# otherwise ProjectImportWizard.importChildResources() never extracts them.
# Schema is from ProjectExportWizard / ProjectImportWizard disassembly.
meta_xml = (
    '<?xml version="1.0" encoding="UTF-8"?>\n'
    '<archive version="1">\n'
    f'\t<source time="{int(time.time()*1000)}" address="" host=""/>\n'
    '\t<projects>\n'
    f'\t\t<project name="{project_name}" description="VPS Postgres connection">\n'
    '\t\t\t<resource name=".dbeaver">\n'
    '\t\t\t\t<resource name="data-sources.json" charset="UTF-8"/>\n'
    '\t\t\t\t<resource name="credentials-config.json"/>\n'
    '\t\t\t</resource>\n'
    '\t\t</project>\n'
    '\t</projects>\n'
    '</archive>\n'
).encode("utf-8")

dbp_path = out_dir / "dexarb-vps.dbp"
with zipfile.ZipFile(dbp_path, "w", zipfile.ZIP_DEFLATED) as z:
    z.writestr("meta.xml", meta_xml)
    z.writestr(f"projects/{project_name}/.project", project_xml)
    # Explicit directory entries (trailing slash) are required: the importer
    # calls ZipFile.getEntry(path) then ZipEntry.isDirectory() to decide whether
    # to recurse. Without a "...​/.dbeaver/" entry the folder resource resolves to
    # null and its children are never extracted.
    z.writestr(f"projects/{project_name}/", b"")
    z.writestr(f"projects/{project_name}/.dbeaver/", b"")
    z.writestr(f"projects/{project_name}/.dbeaver/data-sources.json", ds_bytes)
    z.writestr(f"projects/{project_name}/.dbeaver/credentials-config.json", cc_bytes)

# Round-trip verification.
dec = Cipher(algorithms.AES(AES_KEY), modes.CBC(iv)).decryptor()
roundtrip = dec.update(ciphertext) + dec.finalize()
n = roundtrip[-1]
if not (1 <= n <= 16) or roundtrip[-n:] != bytes([n]) * n:
    raise SystemExit("ERROR: PKCS#7 unpad failed during round-trip check")
if roundtrip[:-n] != creds_plain:
    raise SystemExit("ERROR: ciphertext did not round-trip to the original plaintext")

print(f"  conn_id : {conn_id}")
print(f"  dexarb-vps.dbp : {dbp_path} ({dbp_path.stat().st_size} bytes)")
PY

chmod 600 "$OUT_DIR/dexarb-vps.dbp"

cat > "$OUT_DIR/README.txt" <<EOF
DBeaver project archive for the current VPS Postgres.

Import via:
  DBeaver: File -> Import -> DBeaver -> Project, then select:
    $OUT_DIR/dexarb-vps.dbp

Imports as a separate project ("dexArbTrading-vps-import"); the encrypted
password is bundled, so no prompt on first connect.

Note: the import always creates a new project. To re-import after the VPS IP
changes, delete the existing "dexArbTrading-vps-import" project first
(right-click -> Delete in DBeaver, or remove its folder from the workspace).

Connection name: $CONN_NAME
Host           : $VPS_SSH_HOST
Generated      : $(date -Iseconds)
EOF
chmod 600 "$OUT_DIR/README.txt"

log "Done"
echo "  Import file: $OUT_DIR/dexarb-vps.dbp"
