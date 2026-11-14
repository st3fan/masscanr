# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/masscanr ./cmd/masscanr

FROM alpine:3.24
RUN apk add --no-cache ca-certificates \
    && addgroup -g 10001 masscanr \
    && adduser -D -u 10001 -G masscanr masscanr \
    && mkdir -p /data \
    && chown masscanr:masscanr /data \
    && chmod 0700 /data
COPY --from=build /out/masscanr /usr/local/bin/masscanr
USER 10001:10001
WORKDIR /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/masscanr"]
CMD ["serve", "--listen", "0.0.0.0:8080", "--data-dir", "/data"]
