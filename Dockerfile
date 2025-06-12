# --- すべてのファイルがルートディレクトリにある場合の構成 ---
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
    
    # ★★★ この1行が最終的な解決策です ★★★
    # /app ディレクトリにあるすべてのファイルの所有者を appuser に変更します。..
    RUN chown -R appuser:appgroup /app
    
    # 環境変数はCloud Run側で設定するため、ここでの設定は不要です（残しておいても害はありません）
    ENV FIREBASE_SERVICE_ACCOUNT_KEY_PATH /app/term7-459800-firebase-adminsdk-fbsvc-869b36b213.json
    
    USER appuser
    EXPOSE 8080
    CMD ["./server"]
    