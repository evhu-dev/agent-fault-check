FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/afc ./cmd/afc

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates python3 && rm -rf /var/lib/apt/lists/* \
    && useradd -r -u 10001 -m afc
COPY --from=builder /out/afc /usr/local/bin/afc
USER afc
ENTRYPOINT ["afc"]
