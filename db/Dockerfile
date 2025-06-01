# hackathon-backend/Dockerfile

# --- ビルドステージ ---
    FROM golang:1.24-alpine AS builder

    # ビルドに必要なツールをインストール (もしあれば)
    # RUN apk add --no-cache git gcc libc-dev
    
    WORKDIR /app
    
    # 依存関係を先にコピーしてキャッシュを活用
    COPY go.mod go.sum ./
    RUN go mod download
    # go mod verify # 依存関係の整合性を確認 (任意)
    
    # ソースコードをコピー
    # .dockerignore を使って不要なファイルを除外することを推奨
    COPY *.go ./
    # もしサブディレクトリにソースがあるなら COPY . . / など
    
    # アプリケーションをビルド
    # CGO_ENABLED=0 で静的リンクし、OS依存を減らす
    # -ldflags="-s -w" でデバッグ情報を削除しバイナリサイズを削減
    RUN CGO_ENABLED=0 GOOS=linux go build -v -ldflags="-s -w" -o /server .

    # --- 実行ステージ ---
    FROM alpine:latest

    # CA証明書とタイムゾーン情報を先にインストール (root権限が必要)
    RUN apk --no-cache add ca-certificates tzdata

    # 非rootユーザーで実行するための準備 (セキュリティ向上)
    RUN addgroup -S appgroup && adduser -S appuser -G appgroup
    # USER appuser # USER命令はCMDの直前など、実際にアプリケーションを実行する直前に配置するのが一般的

    WORKDIR /app/ 

    # ビルドステージから実行可能ファイルをコピー
    COPY --from=builder /server .

    # アプリケーションを実行するユーザーを指定
    USER appuser

    # ポート8080を公開 (ドキュメンテーション目的)
    EXPOSE 8080

    # コンテナ起動時に実行するコマンド
    CMD ["./server"]