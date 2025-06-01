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
    
    # 非rootユーザーで実行するための準備 (セキュリティ向上)
    RUN addgroup -S appgroup && adduser -S appuser -G appgroup
    USER appuser
    
    # CA証明書をインストール (HTTPS通信などに必要になる場合がある)
    RUN apk --no-cache add ca-certificates tzdata
    
    WORKDIR /app/ # 実行ユーザーのホームディレクトリ以外が良い場合もある
    
    # ビルドステージから実行可能ファイルをコピー
    COPY --from=builder /server .
    
    # 環境変数 PORT をDockerコンテナ内で利用可能にする (Cloud Runが自動で設定する)
    # EXPOSE ディレクティブはドキュメンテーション目的であり、実際にポートを公開するのは
    # `docker run -p` やCloud Runの設定
    # EXPOSE ${PORT} # PORTはビルド時には確定しないため、実行時に環境変数で渡す
    # ただし、Cloud Runでは8080がデフォルトなので、そちらに合わせるか、
    # main.go側で環境変数PORTを読み取るようにする（上記main.goで対応済み）
    
    # コンテナ起動時に実行するコマンド
    CMD ["./server"]