FROM golang:1.21-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .

# статическая линковка (scratch без libc) и без отладочной информации
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /api .

FROM scratch

COPY --from=builder /api /api

EXPOSE 8080
ENTRYPOINT ["/api"]
