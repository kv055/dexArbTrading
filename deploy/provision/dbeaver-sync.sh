#!/usr/bin/env bash
# Generates a DBeaver project archive (dexarb-vps.dbp) for the current VPS Postgres,
# into deploy/provision/dbeaver-out/. Import via DBeaver: File -> Import -> DBeaver -> Project.
#
# ============================================================================
# DBeaver "Import Project" gotchas — learned the hard way (DBeaver CE 26.1)
# ============================================================================
# These are the non-obvious behaviours that make a *correct* .dbp still appear
# "broken" after import. If you change how this archive is built, keep them in
# mind — most of this script's odd choices exist to satisfy them.
#
# 1. IMPORT IS KEYED ON THE INTERNAL PROJECT NAME, NOT THE .dbp FILENAME.
#    DBeaver reads the project name from meta.xml's <project name="..."> (and the
#    per-project .project file) inside the archive. The .dbp file on disk can be
#    called anything — renaming it changes nothing.
#
# 2. IMPORTING A PROJECT WHOSE NAME ALREADY EXISTS SILENTLY NO-OPS.
#    DBeaver does NOT overwrite, merge, or suffix ("... (2)") an existing project
#    of the same name — it just keeps the old one and the import appears to do
#    nothing. Symptom we hit: regenerating the archive and re-importing kept a
#    stale, pre-tunnel connection (host = the now-firewalled public IP) and threw
#    "Connection refused", because the good connection never actually landed.
#    FIX (below): the project name is timestamped so every archive imports as a
#    brand-new project. Old ones must be deleted from the navigator by hand.
#
# 3. DBeaver DOES NOT RE-SCAN A PROJECT IT ALREADY KNOWS.
#    Editing a known project's .dbeaver/data-sources.json on disk (out from under
#    a running DBeaver, or even between launches once the project is registered)
#    does NOT make the connection re-appear/refresh — the navigator is driven by
#    DBeaver's own workspace registry, not a re-read of the file. So you cannot
#    "fix" an imported connection by patching its JSON; you must re-import as a
#    new project (point 2) or edit it through the GUI. (This is why hand-writing
#    into ~/snap/dbeaver-ce/.../workspace6/<proj>/.dbeaver/ is unreliable.)
#
# 4. THE ARCHIVE LAYOUT IS RIGID (see the zip-building section for specifics):
#    meta.xml must declare every file/folder as nested <resource> elements, and
#    directories need explicit trailing-slash zip entries, or the importer never
#    extracts the children. Get this wrong and the project imports empty.
#
# 5. SSH-TUNNEL HANDLER FORMAT (see data_sources below) is validated against the
#    SSH plugin's schema: handler id "ssh_tunnel", handler-level keys "type",
#    "enabled", "user", "save-password" (HYPHENATED — camelCase "savePassword" is
#    silently ignored), and a "properties" object whose keys (host, port,
#    authType=PUBLIC_KEY, keyPath, implementation) come from the networkHandler
#    extension in org.jkiss.dbeaver.net.ssh's plugin.xml. An unrecognised handler
#    id makes the serializer drop the tunnel ("Can't find network handler '<id>'")
#    and fall back to a direct connection — which is firewalled shut.
#
# Verified against snap dbeaver-ce rev 493 (v26.1): serializer
# org.jkiss.dbeaver.registry.DataSourceSerializerModern, constants
# org.jkiss.dbeaver.model.net.ssh.SSHConstants. Schema can drift between
# versions; if a future DBeaver ignores the tunnel, re-check those two classes.
# ============================================================================
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
: "${VPS_SSH_USER:?VPS_SSH_USER not set in $ENV_VPS}"
: "${VPS_SSH_PRIVATE_KEY:?VPS_SSH_PRIVATE_KEY not set in $ENV_VPS}"
: "${POSTGRES_DB:?POSTGRES_DB not set in $ENV_ROOT}"
: "${POSTGRES_ADMIN_USER:?POSTGRES_ADMIN_USER not set in $ENV_ROOT}"
: "${POSTGRES_ADMIN_PASSWORD:?POSTGRES_ADMIN_PASSWORD not set in $ENV_ROOT}"

command -v python3 >/dev/null 2>&1 || die "python3 not found on PATH"

mkdir -p "$OUT_DIR"
chmod 700 "$OUT_DIR"

# Write the SSH private key alongside the archive so the bundle is self-contained.
# DBeaver's connection references it by PATH (not embedded in the archive): the
# .dbp credentials blob is encrypted with DBeaver's *public static* AES key, so
# embedding a private key there would be effectively plaintext. A path keeps the
# key in this 600-perm file under dbeaver-out/ (gitignored) instead.
KEY_PATH="$(cd "$OUT_DIR" && pwd)/vps_ssh_key"
( umask 077; printf '%s\n' "$VPS_SSH_PRIVATE_KEY" > "$KEY_PATH" )
chmod 600 "$KEY_PATH"

log "Generating DBeaver project archive (SSH-tunnelled)"
echo "  ssh host : $VPS_SSH_HOST"
echo "  ssh user : $VPS_SSH_USER"
echo "  ssh key  : $KEY_PATH"
echo "  db       : $POSTGRES_DB (via tunnel → localhost:5432)"
echo "  db user  : $POSTGRES_ADMIN_USER"
echo "  out dir  : $OUT_DIR"

DBEAVER_DB="$POSTGRES_DB" \
DBEAVER_USER="$POSTGRES_ADMIN_USER" \
DBEAVER_PASSWORD="$POSTGRES_ADMIN_PASSWORD" \
DBEAVER_CONN_NAME="$CONN_NAME" \
DBEAVER_OUT_DIR="$OUT_DIR" \
DBEAVER_SSH_HOST="$VPS_SSH_HOST" \
DBEAVER_SSH_USER="$VPS_SSH_USER" \
DBEAVER_SSH_KEYPATH="$KEY_PATH" \
python3 <<'PY'
import json, os, secrets, time, zipfile
from pathlib import Path

try:
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
except ImportError as e:
    raise SystemExit("ERROR: python3 'cryptography' package is required (pip install cryptography)") from e

database  = os.environ["DBEAVER_DB"]
user      = os.environ["DBEAVER_USER"]
password  = os.environ["DBEAVER_PASSWORD"]
conn_name = os.environ["DBEAVER_CONN_NAME"]
out_dir   = Path(os.environ["DBEAVER_OUT_DIR"])
ssh_host  = os.environ["DBEAVER_SSH_HOST"]
ssh_user  = os.environ["DBEAVER_SSH_USER"]
ssh_key   = os.environ["DBEAVER_SSH_KEYPATH"]

conn_id = f"postgres-jdbc-{int(time.time()*1000):x}-{secrets.token_hex(8)}"

# Postgres is bound to 127.0.0.1 on the VPS and closed at the firewall, so the
# JDBC target is localhost *as seen from the SSH server*; DBeaver's ssh_tunnel
# handler forwards a local port through SSH to it. Auth is by SSH key (survives
# ISP IP rotation) and the tunnel encrypts the otherwise-cleartext DB traffic.
data_sources = {
    "folders": {},
    "connections": {
        conn_id: {
            "provider": "postgresql",
            "driver": "postgres-jdbc",
            "name": conn_name,
            "save-password": True,
            "configuration": {
                "host": "localhost",
                "port": "5432",
                "database": database,
                "url": f"jdbc:postgresql://localhost:5432/{database}",
                "configurationType": "MANUAL",
                "type": "dev",
                "closeIdleConnection": True,
                "provider-properties": {
                    "@dbeaver-show-non-default-db@": "true",
                },
                "auth-model": "native",
                "handlers": {
                    "ssh_tunnel": {
                        "type": "TUNNEL",
                        "enabled": True,
                        "user": ssh_user,
                        "save-password": True,
                        "properties": {
                            "host": ssh_host,
                            "port": "22",
                            "authType": "PUBLIC_KEY",
                            "keyPath": ssh_key,
                            "implementation": "sshj",
                        },
                    },
                },
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
#
# Timestamped so every regenerated archive imports as a NEW project — see the
# "DBeaver Import gotchas" header, points 1 & 2: import is keyed on this internal
# name (not the filename) and silently no-ops if the name already exists.
project_name = "dexArbTrading-vps-import-" + time.strftime("%Y%m%d-%H%M%S")
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
DBeaver project archive for the current VPS Postgres (via SSH tunnel).

Import via:
  DBeaver: File -> Import -> DBeaver -> Project, then select:
    $OUT_DIR/dexarb-vps.dbp

The connection tunnels through SSH: Postgres is bound to 127.0.0.1 on the VPS
and closed at the firewall, so DBeaver connects over an SSH tunnel
(${VPS_SSH_USER}@${VPS_SSH_HOST}:22) authenticated with the bundled key. SSH-key
auth means it keeps working when your ISP rotates your IP, and the tunnel
encrypts the DB traffic. The DB password is bundled (encrypted), so no prompt on
first connect.

IMPORTANT — the SSH private key lives next to this file:
    $KEY_PATH
Keep it here (600 perms, gitignored). The connection references it by absolute
path, so do not move/delete it or the tunnel will fail. If you use a snap/flatpak
DBeaver, it must be allowed to read this path (it is under your home dir).

Each generated archive imports as its OWN project (the internal project name is
timestamped), so you can just re-import after regenerating — no need to delete
anything first. DBeaver's import silently ignores an archive whose project name
already exists, which is why a fixed name made re-imports appear to "do nothing".
Old projects pile up in the navigator; delete the stale ones (right-click ->
Delete) once the new one connects. NB: renaming the .dbp file has no effect —
DBeaver keys on the internal project name, not the filename.

If DBeaver ever fails to read the tunnel config (its data-sources schema can vary
by version), open the connection's SSH tab and confirm: host=$VPS_SSH_HOST,
port=22, user=$VPS_SSH_USER, auth=Public Key, key=$KEY_PATH.

Connection name: $CONN_NAME
SSH host       : $VPS_SSH_HOST
SSH user       : $VPS_SSH_USER
Generated      : $(date -Iseconds)
EOF
chmod 600 "$OUT_DIR/README.txt"

log "Done"
echo "  Import file: $OUT_DIR/dexarb-vps.dbp"
echo "  SSH key    : $KEY_PATH (keep in place; referenced by absolute path)"
