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
	"strings" // stringsパッケージをインポート
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

var db *sql.DB

// Ternary is a helper function for conditional expressions (like a ? b : c)
func Ternary[T any](condition bool, trueVal, falseVal T) T {
	if condition {
		return trueVal
	}
	return falseVal
}

func init() {
	log.Println("-------------------- DEBUG: init() started --------------------")

	isCloudRun := os.Getenv("K_SERVICE") != ""

	if isCloudRun {
		log.Println("DEBUG_INIT: Detected Cloud Run environment (K_SERVICE is set).")
		log.Printf("DEBUG_INIT: K_SERVICE env: [%s]\n", os.Getenv("K_SERVICE"))
		log.Printf("DEBUG_INIT: GOOGLE_CLOUD_PROJECT env: [%s]\n", os.Getenv("GOOGLE_CLOUD_PROJECT"))
	} else {
		log.Println("DEBUG_INIT: Not a Cloud Run environment. Attempting to load .env file.")
		if err := godotenv.Load(); err != nil {
			log.Printf("DEBUG_INIT: Warning - .env file not found or error loading: %v. Will use system environment variables.\n", err)
		} else {
			log.Println("DEBUG_INIT: Successfully loaded .env file for local development.")
		}
	}

	mysqlUser := strings.TrimSpace(os.Getenv("MYSQL_USER"))
	log.Printf("DEBUG_INIT: MYSQL_USER: [%s]\n", mysqlUser)
	mysqlUserPwd := os.Getenv("MYSQL_PASSWORD") // パスワードはログに直接値を出力しない
	if mysqlUserPwd != "" {
		log.Println("DEBUG_INIT: MYSQL_PASSWORD is set (not empty)")
	} else {
		log.Println("DEBUG_INIT: MYSQL_PASSWORD is NOT set (empty)")
	}
	mysqlDatabase := strings.TrimSpace(os.Getenv("MYSQL_DATABASE"))
	log.Printf("DEBUG_INIT: MYSQL_DATABASE: [%s]\n", mysqlDatabase)


	var dsn string
	if isCloudRun {
		log.Println("DEBUG_INIT: Configuring DSN for Cloud Run (Unix socket) WITH HARDCODED INSTANCE_CONNECTION_NAME")
		
		// ▼▼▼ INSTANCE_CONNECTION_NAME を直接書き込む ▼▼▼
		instanceConnectionName := "term7-459800:us-central1:uttc" // あなたの実際のインスタンス接続名に置き換えてください
		log.Printf("DEBUG_INIT: HARDCODED INSTANCE_CONNECTION_NAME: [%s]\n", instanceConnectionName)
		// ▲▲▲ INSTANCE_CONNECTION_NAME を直接書き込む ▲▲▲

		// socketDirはオプションなので、未設定の場合はデフォルト値を使用
		socketDir := strings.TrimSpace(os.Getenv("DB_SOCKET_DIR"))
		log.Printf("DEBUG_INIT: Raw DB_SOCKET_DIR from env: [%s]\n", os.Getenv("DB_SOCKET_DIR"))
		if socketDir == "" {
			socketDir = "/cloudsql"
			log.Printf("DEBUG_INIT: DB_SOCKET_DIR not set or empty, defaulting to: %s\n", socketDir)
		} else {
			log.Printf("DEBUG_INIT: Trimmed DB_SOCKET_DIR: [%s]\n", socketDir)
		}
		
		dsn = fmt.Sprintf("%s:%s@unix(%s/%s)/%s?parseTime=true",
			mysqlUser,
			mysqlUserPwd,
			socketDir,
			instanceConnectionName, // ハードコードされた値が使われる
			mysqlDatabase)
		log.Println("DEBUG_INIT: ✅ DB DSN for Unix socket constructed.")

	} else { // ローカル開発環境など
		log.Println("DEBUG_INIT: Configuring DSN for local development (TCP)")
		mysqlHost := strings.TrimSpace(os.Getenv("MYSQL_HOST"))
		mysqlPort := strings.TrimSpace(os.Getenv("MYSQL_PORT"))
		// ... (ローカル開発用のMYSQL_HOST, MYSQL_PORTのログ出力とデフォルト値設定) ...
		log.Printf("DEBUG_INIT: Trimmed MYSQL_HOST (local): [%s]\n", mysqlHost)
		log.Printf("DEBUG_INIT: Trimmed MYSQL_PORT (local): [%s]\n", mysqlPort)
		if mysqlPort == "" {
			mysqlPort = "3306"
			log.Printf("DEBUG_INIT: MYSQL_PORT (local) was empty, defaulting to: %s\n", mysqlPort)
		}
		if mysqlHost == "" { // ローカルではMYSQL_HOSTは必須
			log.Fatal("FATAL_INIT: MYSQL_HOST environment variable not set for local development.")
		}
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
			mysqlUser,
			mysqlUserPwd,
			mysqlHost,
			mysqlPort,
			mysqlDatabase)
		log.Println("DEBUG_INIT: ✅ DB DSN for TCP constructed.")
	}

	// 必須環境変数の共通チェック
	if mysqlUser == "" {
		log.Fatal("FATAL_INIT: MYSQL_USER environment variable is not set.")
	}
	if mysqlUserPwd == "" {
		log.Fatal("FATAL_INIT: MYSQL_PASSWORD environment variable is not set.")
	}
	if mysqlDatabase == "" {
		log.Fatal("FATAL_INIT: MYSQL_DATABASE environment variable is not set.")
	}


	maskedDsnForLog := fmt.Sprintf("%s:******@%s.../%s (type: %s)", mysqlUser, Ternary(isCloudRun, "unix(", "tcp("), mysqlDatabase, Ternary(isCloudRun, "unix", "tcp"))
	log.Printf("DEBUG_INIT: Attempting sql.Open() with DSN (masked): %s\n", maskedDsnForLog)

	var err error
	_db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("FATAL_INIT: sql.Open failed. DSN (masked): %s. Error: %v\n", maskedDsnForLog, err)
	}
	log.Println("DEBUG_INIT: sql.Open() successful.")

	_db.SetMaxOpenConns(10)
	_db.SetMaxIdleConns(10)
	_db.SetConnMaxLifetime(5 * time.Minute)
	log.Println("DEBUG_INIT: Database connection pool configured.")

	log.Println("DEBUG_INIT: Attempting _db.Ping()...")
	if err := _db.Ping(); err != nil {
		log.Fatalf("FATAL_INIT: _db.Ping failed. DSN (masked): %s. Error: %v\n", maskedDsnForLog, err)
	}
	db = _db
	log.Println("DEBUG_INIT: ✅ DB接続に成功しました.")
	log.Println("-------------------- DEBUG: init() finished --------------------")
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