# --- すべてのファイルがルートディレクトリにある場合の構成 ---
    FROM golang:1.24-alpine AS builder

    WORKDIR /app
    # ★★★ 修正点: 'db/' のプレフィックスをすべて削除 ★★★
    # ファイルがルートディレクトリに移動したため、パス指定が不要になります。
    COPY go.mod go.sum ./
    RUN go mod download
    COPY *.go ./
    RUN CGO_ENABLED=0 GOOS=linux go build -v -ldflags="-s -w" -o /server .
    
    FROM alpine:latest
    RUN apk --no-cache add ca-certificates tzdata
    RUN addgroup -S appgroup && adduser -S appuser -G appgroup
    WORKDIR /app/
    COPY --from=builder /server .
    # ★★★ 修正点: 'db/' のプレフィックスをすべて削除 ★★★
    COPY term7-459800-firebase-adminsdk-fbsvc-869b36b213.json .
    
    # 環境変数を設定して、Goアプリケーションがファイルのフルパスを認識できるようにします。
    ENV FIREBASE_SERVICE_ACCOUNT_KEY_PATH /app/term7-459800-firebase-adminsdk-fbsvc-869b36b213.json
    
    USER appuser
    EXPOSE 8080
    CMD ["./server"]
    