#!/bin/bash
# Grant Accessibility + Input Monitoring for localvoice via TCC database.
# Requires Terminal to have Full Disk Access (System Settings → Privacy → Full Disk Access → Terminal ON).

BINARY="$(cd "$(dirname "$0")" && pwd)/localvoice"
TCC_DB="$HOME/Library/Application Support/com.apple.TCC/TCC.db"

if [ ! -f "$BINARY" ]; then
  echo "ERROR: $BINARY not found. Run: make build"
  exit 1
fi

echo "Granting permissions for: $BINARY"

grant() {
  local service="$1"
  sqlite3 "$TCC_DB" \
    "DELETE FROM access WHERE service='$service' AND client='$BINARY';" 2>/dev/null
  sqlite3 "$TCC_DB" \
    "INSERT INTO access VALUES('$service','$BINARY',1,2,3,1,NULL,NULL,NULL,'UNUSED',NULL,0,cast(strftime('%s','now') as int));" 2>/dev/null
  if [ $? -eq 0 ]; then
    echo "  [OK] $service"
  else
    echo "  [FAIL] $service — Terminal needs Full Disk Access"
  fi
}

grant "kTCCServiceAccessibility"
grant "kTCCServiceListenEvent"

echo ""
echo "Done. Restart localvoice for changes to take effect."
echo "If it still fails: System Settings → Full Disk Access → add Terminal → re-run this script."
