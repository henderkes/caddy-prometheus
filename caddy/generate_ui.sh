#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROM_VERSION=$(grep 'github.com/prometheus/prometheus ' "$SCRIPT_DIR/../go.mod" | awk '{print $2}')
PROM_UI_SRC="$(go env GOMODCACHE)/github.com/prometheus/prometheus@${PROM_VERSION}/web/ui"

if [ ! -d "$PROM_UI_SRC" ]; then
    echo "Prometheus UI source not found at $PROM_UI_SRC"
    echo "Run 'go mod download' first."
    exit 1
fi

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

echo "Copying UI source from $PROM_UI_SRC..."
cp -a "$PROM_UI_SRC/." "$TMPDIR/"
find "$TMPDIR" -type f -exec chmod u+w {} +
find "$TMPDIR" -type d -exec chmod u+wx {} +

cd "$TMPDIR"

npm install --silent
npm run build -w @prometheus-io/lezer-promql --silent
npm run build -w @prometheus-io/codemirror-promql --silent
npm run build -w @prometheus-io/mantine-ui --silent

echo "Compressing assets..."
mkdir -p static
mv mantine-ui/dist static/mantine-ui
find static -type f ! -name '*.gz' -exec gzip -fkn {} \;

echo "Copying to $SCRIPT_DIR/static/..."
rm -rf "$SCRIPT_DIR/static"
mkdir -p "$SCRIPT_DIR/static/mantine-ui/assets"
cp static/mantine-ui/index.html static/mantine-ui/index.html.gz \
   static/mantine-ui/favicon.svg static/mantine-ui/favicon.svg.gz \
   "$SCRIPT_DIR/static/mantine-ui/"
cp static/mantine-ui/assets/* "$SCRIPT_DIR/static/mantine-ui/assets/"

echo "UI assets at $SCRIPT_DIR/static/ ($(du -sh "$SCRIPT_DIR/static/" | cut -f1))"
