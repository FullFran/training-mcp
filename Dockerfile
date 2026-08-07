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

# ---- runtime --------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/training-mcp /usr/local/bin/training-mcp
COPY --from=build --chown=65532:65532 /data /data

# The default DB path resolves against $HOME, which does not exist in a
# distroless image. It must be set explicitly.
ENV TRAINING_DB_PATH=/data/training.db

USER 65532:65532
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/training-mcp"]
CMD ["--addr", ":8080"]
