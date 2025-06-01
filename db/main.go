package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"github.com/oklog/ulid/v2"
)

type User struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// ① GoプログラムからMySQLへ接続
var db *sql.DB

func init() {
	// .env ファイルから環境変数を読み込む (ローカル開発時のみ)
	if os.Getenv("GOOGLE_CLOUD_PROJECT") == "" { // Cloud Run環境でない場合
		if err := godotenv.Load(); err != nil {
			log.Println("Warning: .env file not found, attempting to use system environment variables for local development")
		}
	}

	mysqlUser := os.Getenv("MYSQL_USER")
	mysqlUserPwd := os.Getenv("MYSQL_PASSWORD")
	mysqlDatabase := os.Getenv("MYSQL_DATABASE")

	var dsn string
	// Cloud Run上では環境変数 GOOGLE_CLOUD_PROJECT が設定されることを利用する
	// または、独自の環境変数 (例: RUN_ENV=production) などで制御する
	if os.Getenv("GOOGLE_CLOUD_PROJECT") != "" { // Cloud Run環境を想定 (INSTANCE_CONNECTION_NAME を使用)
		instanceConnectionName := "term7-459800:us-central1:uttc" //os.Getenv("INSTANCE_CONNECTION_NAME") // 例: my-project:us-central1:my-instance
		if instanceConnectionName == "" {
			log.Fatal("INSTANCE_CONNECTION_NAME environment variable not set for Cloud Run")
		}
		// /cloudsql/ は一般的なディレクトリだが、環境変数 DB_SOCKET_DIR で変更可能にする
		socketDir := os.Getenv("DB_SOCKET_DIR")
		if socketDir == "" {
			socketDir = "/cloudsql"
		}
		dsn = fmt.Sprintf("%s:%s@unix(%s/%s)/%s?parseTime=true",
			mysqlUser,
			mysqlUserPwd,
			socketDir,
			instanceConnectionName,
			mysqlDatabase)
		log.Println("✅ DB接続にUnixソケットを使用します")
	} else { // ローカル開発環境またはその他の環境 (TCP接続)
		mysqlHost := os.Getenv("MYSQL_HOST")
		mysqlPort := os.Getenv("MYSQL_PORT")
		if mysqlPort == "" {
			mysqlPort = "3306" // デフォルトポート
		}
		if mysqlHost == "" {
			log.Fatal("MYSQL_HOST environment variable not set for local development")
		}
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
			mysqlUser,
			mysqlUserPwd,
			mysqlHost,
			mysqlPort,
			mysqlDatabase)
		log.Println("✅ DB接続にTCPを使用します")
	}

	_db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("fail: sql.Open, dsn: %s, error: %v\n", dsn, err)
	}

	// 接続プール設定 (任意だが推奨)
	_db.SetMaxOpenConns(25)
	_db.SetMaxIdleConns(25)
	_db.SetConnMaxLifetime(5 * time.Minute)

	if err := _db.Ping(); err != nil {
		log.Fatalf("fail: _db.Ping, dsn: %s, error: %v\n", dsn, err)
	}
	db = _db
	log.Println("✅ DB接続に成功しました")
}

func userHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		name := r.URL.Query().Get("name")
		if name == "" {
			log.Println("fail: name query parameter is empty")
			http.Error(w, `{"error": "name query parameter is required"}`, http.StatusBadRequest)
			return
		}

		rows, err := db.Query("SELECT id, name, age FROM user WHERE name = ?", name)
		if err != nil {
			log.Printf("fail: db.Query for name %s, %v\n", name, err)
			http.Error(w, `{"error": "Internal server error"}`, http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		users := make([]User, 0)
		for rows.Next() {
			var u User
			if err := rows.Scan(&u.Id, &u.Name, &u.Age); err != nil {
				log.Printf("fail: rows.Scan, %v\n", err)
				http.Error(w, `{"error": "Internal server error during scan"}`, http.StatusInternalServerError)
				return
			}
			users = append(users, u)
		}
		if err := rows.Err(); err != nil {
			log.Printf("fail: rows.Err, %v\n", err)
			http.Error(w, `{"error": "Internal server error with rows"}`, http.StatusInternalServerError)
			return
		}

		if len(users) == 0 {
			// http.NotFound(w, r) でも良いが、JSONレスポンスで統一するなら以下
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message": "No users found"}`))
			return
		}

		bytes, err := json.Marshal(users)
		if err != nil {
			log.Printf("fail: json.Marshal users, %v\n", err)
			http.Error(w, `{"error": "Internal server error during marshal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(bytes)

	case http.MethodPost:
		var reqUser struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqUser); err != nil {
			log.Printf("fail: json decoding request body, %v\n", err)
			http.Error(w, `{"error": "Invalid request body"}`, http.StatusBadRequest)
			return
		}

		// バリデーション
		if reqUser.Name == "" {
			log.Println("fail: POST request with empty name")
			http.Error(w, `{"error": "Name is required"}`, http.StatusBadRequest)
			return
		}
		if len(reqUser.Name) > 50 { // 例: 名前の長さに制限を設ける
			log.Println("fail: POST request with name too long")
			http.Error(w, `{"error": "Name must be 50 characters or less"}`, http.StatusBadRequest)
			return
		}
		if reqUser.Age < 20 || reqUser.Age > 80 {
			log.Printf("fail: POST request with invalid age: %d\n", reqUser.Age)
			http.Error(w, `{"error": "Age must be between 20 and 80"}`, http.StatusBadRequest)
			return
		}

		// ULIDの生成
		t := time.Now().UTC() // UTCを推奨
		entropy := ulid.Monotonic(rand.New(rand.NewSource(t.UnixNano())), 0)
		newID := ulid.MustNew(ulid.Timestamp(t), entropy).String()

		tx, err := db.Begin()
		if err != nil {
			log.Printf("fail: db.Begin, %v\n", err)
			http.Error(w, `{"error": "Internal server error"}`, http.StatusInternalServerError)
			return
		}
		// defer tx.Rollback() はエラー時にのみ呼び出されるようにする
		// 成功時は tx.Commit() が呼ばれる

		_, err = tx.Exec("INSERT INTO user (id, name, age) VALUES (?, ?, ?)", newID, reqUser.Name, reqUser.Age)
		if err != nil {
			tx.Rollback() // INSERT失敗時はロールバック
			log.Printf("fail: tx.Exec INSERT user, %v\n", err)
			http.Error(w, `{"error": "Failed to create user"}`, http.StatusInternalServerError)
			return
		}

		if err := tx.Commit(); err != nil {
			// Commitが失敗した場合、既にExecが成功していてもロールバックを試みるのは難しい
			// (DBによっては自動ロールバックされるが、基本はExecとCommitは一体)
			log.Printf("fail: tx.Commit, %v\n", err)
			http.Error(w, `{"error": "Failed to commit transaction"}`, http.StatusInternalServerError)
			return
		}

		res := struct {
			Id string `json:"id"`
		}{Id: newID}
		resBytes, err := json.Marshal(res)
		if err != nil {
			log.Printf("fail: json.Marshal created user ID, %v\n", err)
			// この時点でDBへの登録は成功しているので、クライアントには成功を伝えるがログは残す
			http.Error(w, `{"error": "Internal server error generating response"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated) // 201 Created がより適切
		w.Write(resBytes)

	default:
		log.Printf("fail: HTTP Method %s not allowed for /user\n", r.Method)
		http.Error(w, `{"error": "Method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// allUsersHandler は /users (複数形) で全ユーザー情報を返すエンドポイントの例
func allUsersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		log.Printf("fail: HTTP Method %s not allowed for /users\n", r.Method)
		http.Error(w, `{"error": "Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	rows, err := db.Query("SELECT id, name, age FROM user ORDER BY name") // 例: 名前順で取得
	if err != nil {
		log.Printf("fail: db.Query for all users, %v\n", err)
		http.Error(w, `{"error": "Internal server error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Id, &u.Name, &u.Age); err != nil {
			log.Printf("fail: rows.Scan for all users, %v\n", err)
			http.Error(w, `{"error": "Internal server error during scan"}`, http.StatusInternalServerError)
			return
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		log.Printf("fail: rows.Err for all users, %v\n", err)
		http.Error(w, `{"error": "Internal server error with rows"}`, http.StatusInternalServerError)
		return
	}

	bytes, err := json.Marshal(users)
	if err != nil {
		log.Printf("fail: json.Marshal all users, %v\n", err)
		http.Error(w, `{"error": "Internal server error during marshal"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(bytes)
}


func main() {
	http.HandleFunc("/user", userHandler) // 単数形エンドポイント (特定のユーザー操作)
	http.HandleFunc("/users", allUsersHandler) // 複数形エンドポイント (全ユーザー取得など) [cite: 79]

	// Ctrl+CでHTTPサーバー停止時にDBをクローズする
	// この関数はメインゴルーチンをブロックしないように最後に呼び出すか、
	// ListenAndServe のエラーハンドリングと組み合わせる
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Cloud Runのデフォルトポート
	}
	serverAddr := fmt.Sprintf(":%s", port)
	log.Printf("✅ Listening on port %s...\n", port)

	go func() {
		if err := http.ListenAndServe(serverAddr, nil); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Could not listen on %s: %v\n", serverAddr, err)
		}
	}()

	// Graceful shutdown
	s := <-stopChan
	log.Printf("received syscall: %v, initiating graceful shutdown...", s)
	// ここでシャットダウン処理 (例: 実行中のリクエストの完了を待つ) を行う
	// 今回はDBクローズのみ
	if db != nil {
		if err := db.Close(); err != nil {
			log.Printf("Error closing database: %v\n", err)
		} else {
			log.Println("✅ Database connection closed successfully.")
		}
	}
	log.Println("Server shut down gracefully.")
	os.Exit(0) // これがないと `go run` が終了しない場合がある
}

// closeDBWithSysCall は main 関数に統合したため不要