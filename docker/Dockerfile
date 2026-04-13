FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-s -w -X main.version=${VERSION}" \
    -o /bin/meili-ha-proxy ./cmd/meili-ha-proxy/

FROM --platform=${TARGETPLATFORM} alpine:3.20
RUN apk --no-cache add ca-certificates wget
COPY --from=builder /bin/meili-ha-proxy /meili-ha-proxy

EXPOSE 7700 9090
ENTRYPOINT ["/meili-ha-proxy"]
