# syntax=docker/dockerfile:1

FROM node:22-bookworm-slim AS frontend
WORKDIR /src/web

COPY web/package.json web/yarn.lock web/.yarnrc.yml ./
RUN corepack enable && corepack prepare yarn@4.14.1 --activate
RUN yarn install --immutable
COPY web/ ./
RUN yarn build

FROM golang:1.26-bookworm AS backend
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/tsumugi-pay ./cmd/server

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install --no-install-recommends -y ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --create-home --uid 10001 tsumugi
WORKDIR /app
COPY --from=backend /out/tsumugi-pay /usr/local/bin/tsumugi-pay
USER tsumugi
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/tsumugi-pay"]
