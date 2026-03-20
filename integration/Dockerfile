FROM dunglas/frankenphp:builder AS builder

COPY --from=caddy:builder /usr/bin/xcaddy /usr/bin/xcaddy

COPY . /src/caddy-prometheus/

RUN CGO_ENABLED=1 \
    XCADDY_GO_BUILD_FLAGS="-ldflags='-w -s' -tags=nobadger,nomysql,nopgx" \
    CGO_CFLAGS="$(php-config --includes)" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs)" \
    xcaddy build \
        --output /usr/local/bin/frankenphp \
        --with github.com/dunglas/frankenphp \
        --with github.com/dunglas/frankenphp/caddy \
        --with github.com/dunglas/mercure/caddy \
        --with github.com/dunglas/vulcain/caddy \
        --with github.com/dunglas/caddy-cbrotli \
        --with github.com/henderkes/caddy-route-metrics/caddy \
        --with github.com/henderkes/caddy-prometheus=/src/caddy-prometheus

FROM node:22-slim AS ui-builder
COPY --from=builder /root/go/pkg/mod/github.com/prometheus/prometheus@v0.310.0/web/ui /ui
WORKDIR /ui
RUN npm install \
    && npm run build -w @prometheus-io/lezer-promql \
    && npm run build -w @prometheus-io/codemirror-promql \
    && npm run build -w @prometheus-io/mantine-ui \
    && mkdir -p static && mv mantine-ui/dist static/mantine-ui

FROM dunglas/frankenphp:latest

RUN apt-get update && apt-get install -y --no-install-recommends \
        curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/bin/frankenphp /usr/local/bin/frankenphp
COPY --from=ui-builder /ui/static /app/static
COPY integration/Caddyfile /etc/frankenphp/Caddyfile

RUN mkdir -p /data/prometheus

EXPOSE 80 443

WORKDIR /app
ENTRYPOINT ["frankenphp"]
CMD ["run", "--config", "/etc/frankenphp/Caddyfile"]
