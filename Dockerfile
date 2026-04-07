FROM docker.io/library/golang:1.26.1-bookworm AS builder

WORKDIR /build

# Cache dependency downloads separately from source compilation
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" \
    -o retina-generator .

# ---- runtime ----------------------------------------------------------------
# Use the root variant so the generator can write to the Docker-managed
# named volume (which is created with root ownership). The generator is a
# one-shot init container with no network exposure, so running as root here
# carries no meaningful security risk.
FROM gcr.io/distroless/static-debian12

COPY --from=builder /build/retina-generator /retina-generator

# The generator writes its output to a path supplied via --output-file.
# Mount a shared volume there so the orchestrator can read the file.
ENTRYPOINT ["/retina-generator"]
