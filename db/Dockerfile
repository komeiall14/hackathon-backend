# --- ビルドステージ ---
    FROM golang:1.24-alpine AS builder

    WORKDIR /app
    
    # 依存関係の取得
    COPY go.mod go.sum ./
    RUN go mod download
    
    # GoコードとFirebaseのキーファイルをコピー
    COPY term7-459800-firebase-adminsdk-fbsvc-869b36b213.json .
    COPY *.go ./
    
    # Goアプリケーションのビルド
    RUN CGO_ENABLED=0 GOOS=linux go build -v -ldflags="-s -w" -o /server .
    
    # --- 実行ステージ ---
    FROM alpine:latest
    
    RUN apk --no-cache add ca-certificates tzdata
    RUN addgroup -S appgroup && adduser -S appuser -G appgroup
    
    WORKDIR /app/
    
    # ビルド成果物とJSONキーファイルをコピー（← ここを修正）
    COPY --from=builder /server .
    COPY --from=builder /app/term7-459800-firebase-adminsdk-fbsvc-869b36b213.json .
    
    USER appuser
    EXPOSE 8080
    CMD ["./server"]
    