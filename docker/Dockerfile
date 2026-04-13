FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=arm64
ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-s -w -X main.version=${VERSION}" \
    -o /bin/meili-ha-proxy ./cmd/meili-ha-proxy/

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /bin/meili-ha-proxy /meili-ha-proxy

EXPOSE 7700 9090
ENTRYPOINT ["/meili-ha-proxy"]
