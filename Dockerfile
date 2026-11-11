# syntax=docker/dockerfile:1

# ---- build ----------------------------------------------------------------
# modernc.org/sqlite is a pure-Go driver, so the binary builds with CGO_ENABLED=0
# and links statically. That is what allows the distroless runtime stage below.
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/training-mcp ./cmd/training-mcp

# Pre-create the data directory with the runtime UID. A named Docker volume
# mounted at /data inherits this ownership on first creation, which is what
# lets the non-root process write the SQLite file.
RUN mkdir -p /data && chown 65532:65532 /data

# ---- coach engine ---------------------------------------------------------
# The in-app coach's engine (engine/, docs/coach.md) runs on Node with the Pi
# SDK. It is installed in every image but only started when TRAINING_COACH=1;
# with that unset nothing in /app/engine is read. Install scripts are not run.
FROM node:24-slim AS engine
WORKDIR /app/engine
COPY engine/package.json engine/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts && npm cache clean --force
COPY engine/main.ts engine/protocol.ts ./
# COPY keeps the build context's modes, and the server's checkout is made under
# umask 0077: the non-root user must still be able to read the engine.
RUN chmod -R a+rX,go-w /app/engine

# ---- runtime --------------------------------------------------------------
# distroless with Node: still no shell and no package manager. The static Go
# binary runs here unchanged; Node is only there for the coach engine.
FROM gcr.io/distroless/nodejs24-debian12:nonroot

COPY --from=build /out/training-mcp /usr/local/bin/training-mcp
COPY --from=build --chown=65532:65532 /data /data
COPY --from=engine /app/engine /app/engine

# The default DB path resolves against $HOME, which does not exist in a
# distroless image. It must be set explicitly.
ENV TRAINING_DB_PATH=/data/training.db
# Where the coach finds Node and its engine, used only with TRAINING_COACH=1.
ENV TRAINING_COACH_NODE=/nodejs/bin/node
ENV TRAINING_COACH_ENGINE=/app/engine/main.ts

USER 65532:65532
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/training-mcp"]
CMD ["--addr", ":8080"]
