# hackathon-backend/Dockerfile

# --- ビルドステージ ---
# Goのバージョンを実際のプロジェクトに合わせてください
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 依存関係を先にコピーしてキャッシュを活用
COPY go.mod go.sum ./
RUN go mod download

# ★★★ 修正点1: Firebaseキーファイルをビルドステージにコピー ★★★
# このファイル名はご自身のキーファイル名に合わせてください
COPY term7-459800-firebase-adminsdk-fbsvc-869b36b213.json .

# ソースコードをコピー
COPY *.go ./

# アプリケーションをビルド
RUN CGO_ENABLED=0 GOOS=linux go build -v -ldflags="-s -w" -o /server .

# --- 実行ステージ ---
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app/ 

# ★★★ 修正点2: ビルドステージから実行ファイルとキーファイルをコピー ★★★
COPY --from=builder /server .
COPY --from=builder /term7-459800-firebase-adminsdk-fbsvc-869b36b213.json .

# アプリケーションを実行するユーザーを指定
USER appuser

EXPOSE 8080

CMD ["./server"]
