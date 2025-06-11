# hackathon-backend/db/Dockerfile

# --- ビルドステージ ---
    FROM golang:1.24-alpine AS builder

    WORKDIR /app
    
    # ビルドコンテキスト(プロジェクトルート)から見て、db/ にあるファイルをコピーする
    COPY db/go.mod db/go.sum ./
    RUN go mod download
    
    # db/ にあるキーファイルとGoのソースファイルをコピー
    COPY db/term7-459800-firebase-adminsdk-fbsvc-869b36b213.json .
    COPY db/*.go ./
    
    # アプリケーションをビルドc
    RUN CGO_ENABLED=0 GOOS=linux go build -v -ldflags="-s -w" -o /server .
    
    # --- 実行ステージ ---
    FROM alpine:latest
    
    RUN apk --no-cache add ca-certificates tzdata
    RUN addgroup -S appgroup && adduser -S appuser -G appgroup
    
    WORKDIR /app/
    
    # ビルドステージから実行ファイルとキーファイルをコピーする
    COPY --from=builder /server .
    COPY --from=builder /app/term7-459800-firebase-adminsdk-fbsvc-869b213.json .
    
    # アプリケーションを実行するユーザーを指定
    USER appuser
    
    EXPOSE 8080
    
    CMD ["./server"]
    