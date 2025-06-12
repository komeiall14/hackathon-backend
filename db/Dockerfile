# --- 実行ステージでだけJSONを使うなら、こうするのがシンプル. ---
    FROM golang:1.24-alpine AS builder

    # ★★★ 修正点: COPY元のパスに 'db/' を追加 ★★★
    # ビルドコンテキストのルートから見たファイルの場所を指定します。
    WORKDIR /app
    COPY db/go.mod db/go.sum ./
    RUN go mod download
    COPY db/*.go ./
    RUN CGO_ENABLED=0 GOOS=linux go build -v -ldflags="-s -w" -o /server .
    
    FROM alpine:latest
    RUN apk --no-cache add ca-certificates tzdata
    RUN addgroup -S appgroup && adduser -S appuser -G appgroup
    WORKDIR /app/
    COPY --from=builder /server .
    # ★★★ 修正点: COPY元のパスに 'db/' を追加 ★★★
    COPY db/term7-459800-firebase-adminsdk-fbsvc-869b36b213.json .
    
    # 環境変数を設定して、Goアプリケーションがファイルのフルパスを認識できるようにします。.
    # この行は前の修正から引き継いでいます。
    ENV FIREBASE_SERVICE_ACCOUNT_KEY_PATH /app/term7-459800-firebase-adminsdk-fbsvc-869b36b213.json
    
    USER appuser
    EXPOSE 8080
    CMD ["./server"]
    