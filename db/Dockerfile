# --- 実行ステージでだけJSONを使うなら、こうするのがシンプル ---
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -v -ldflags="-s -w" -o /server .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
WORKDIR /app/
COPY --from=builder /server .
COPY term7-459800-firebase-adminsdk-fbsvc-869b36b213.json . 
USER appuser
EXPOSE 8080
CMD ["./server"]
