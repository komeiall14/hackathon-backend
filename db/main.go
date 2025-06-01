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

var db *sql.DB

func init() {
	// Cloud Run環境でない場合（ローカル開発時など）に .env ファイルを読み込む
	if os.Getenv("K_SERVICE") == "" { // K_SERVICEはCloud Runが自動で設定する環境変数の一つ
		if err := godotenv.Load(); err != nil {
			log.Println("Warning: .env file not found or error loading, attempting to use system environment variables for local development")
		}
	}

	mysqlUser := os.Getenv("MYSQL_USER")
	mysqlUserPwd := os.Getenv("MYSQL_PASSWORD")
	mysqlDatabase := os.Getenv("MYSQL_DATABASE")

	var dsn string
	// Cloud Run環境では環境変数 K_SERVICE が設定されることを利用する
	if os.Getenv("K_SERVICE") != "" { // Cloud Run環境を想定 (INSTANCE_CONNECTION_NAME を使用)
		instanceConnectionName := os.Getenv("INSTANCE_CONNECTION_NAME")
		if instanceConnectionName == "" {
			log.Fatal("FATAL: INSTANCE_CONNECTION_NAME environment variable not set for Cloud Run")
		}
		// /cloudsql/ は一般的なディレクトリだが、環境変数 DB_SOCKET_DIR で変更可能にする
		socketDir := os.Getenv("DB_SOCKET_DIR")
		if socketDir == "" {
			socketDir = "/cloudsql" // Cloud RunのデフォルトのUnixソケットディレクトリ
		}
		dsn = fmt.Sprintf("%s:%s@unix(%s/%s)/%s?parseTime=true",
			mysqlUser,
			mysqlUserPwd,
			socketDir,
			instanceConnectionName,
			mysqlDatabase)
		log.Println("✅ DB接続にUnixソケットを使用します (Cloud Run environment)")
	} else { // ローカル開発環境またはその他の環境 (TCP接続)
		mysqlHost := os.Getenv("MYSQL_HOST")
		mysqlPort := os.Getenv("MYSQL_PORT")
		if mysqlPort == "" {
			mysqlPort = "3306" // デフォルトポート
		}
		if mysqlHost == "" {
			log.Fatal("FATAL: MYSQL_HOST environment variable not set for local development")
		}
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
			mysqlUser,
			mysqlUserPwd,
			mysqlHost,
			mysqlPort,
			mysqlDatabase)
		log.Println("✅ DB接続にTCPを使用します (Local environment)")
	}

	if mysqlUser == "" || mysqlUserPwd == "" || mysqlDatabase == "" {
		log.Fatal("FATAL: MYSQL_USER, MYSQL_PASSWORD, or MYSQL_DATABASE environment variable is not set.")
	}

	log.Printf("Attempting to connect with DSN (password masked): %s:******@.../%s\n", mysqlUser, mysqlDatabase)

	_db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("FATAL: sql.Open failed, dsn: %s, error: %v\n", dsn, err)
	}

	_db.SetMaxOpenConns(10) // 少し控えめに設定
	_db.SetMaxIdleConns(10)
	_db.SetConnMaxLifetime(5 * time.Minute)

	if err := _db.Ping(); err != nil {
		log.Fatalf("FATAL: _db.Ping failed, dsn (password masked): %s:******@.../%s, error: %v\n", mysqlUser, mysqlDatabase, err)
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

		if reqUser.Name == "" {
			log.Println("fail: POST request with empty name")
			http.Error(w, `{"error": "Name is required"}`, http.StatusBadRequest)
			return
		}
		if len(reqUser.Name) > 50 {
			log.Println("fail: POST request with name too long")
			http.Error(w, `{"error": "Name must be 50 characters or less"}`, http.StatusBadRequest)
			return
		}
		if reqUser.Age < 20 || reqUser.Age > 80 {
			log.Printf("fail: POST request with invalid age: %d\n", reqUser.Age)
			http.Error(w, `{"error": "Age must be between 20 and 80"}`, http.StatusBadRequest)
			return
		}

		t := time.Now().UTC()
		entropy := ulid.Monotonic(rand.New(rand.NewSource(t.UnixNano())), 0)
		newID := ulid.MustNew(ulid.Timestamp(t), entropy).String()

		log.Printf("Attempting to INSERT user with ID: %s, Name: %s, Age: %d\n", newID, reqUser.Name, reqUser.Age)

		tx, err := db.Begin()
		if err != nil {
			log.Printf("fail: db.Begin, %v\n", err)
			http.Error(w, `{"error": "Internal server error on db.Begin"}`, http.StatusInternalServerError)
			return
		}

		_, err = tx.Exec("INSERT INTO user (id, name, age) VALUES (?, ?, ?)", newID, reqUser.Name, reqUser.Age)
		if err != nil {
			tx.Rollback()
			log.Printf("fail: tx.Exec INSERT user, %v\n", err)
			http.Error(w, `{"error": "Failed to create user"}`, http.StatusInternalServerError)
			return
		}
		log.Printf("✅ tx.Exec INSERT user success for ID: %s\n", newID)

		if err := tx.Commit(); err != nil {
			log.Printf("fail: tx.Commit, %v\n", err)
			http.Error(w, `{"error": "Failed to commit transaction"}`, http.StatusInternalServerError)
			return
		}
		log.Println("✅ tx.Commit success")

		res := struct {
			Id string `json:"id"`
		}{Id: newID}
		resBytes, err := json.Marshal(res)
		if err != nil {
			log.Printf("fail: json.Marshal created user ID, %v\n", err)
			http.Error(w, `{"error": "Internal server error generating response"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(resBytes)

	default:
		log.Printf("fail: HTTP Method %s not allowed for /user\n", r.Method)
		http.Error(w, `{"error": "Method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func allUsersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		log.Printf("fail: HTTP Method %s not allowed for /users\n", r.Method)
		http.Error(w, `{"error": "Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	rows, err := db.Query("SELECT id, name, age FROM user ORDER BY name")
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
	log.Println("🚀 Server starting...")

	http.HandleFunc("/user", userHandler)
	http.HandleFunc("/users", allUsersHandler)

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Cloud Runのデフォルトポート
	}
	serverAddr := fmt.Sprintf(":%s", port)
	log.Printf("✅ Listening on port %s...\n", port)

	// HTTPサーバーをゴルーチンで起動し、エラーハンドリングを行う
	go func() {
		if err := http.ListenAndServe(serverAddr, nil); err != nil && err != http.ErrServerClosed {
			log.Fatalf("FATAL: Could not listen on %s: %v\n", serverAddr, err)
		}
	}()
	log.Println("✅ HTTP server is serving")


	// Graceful shutdown
	s := <-stopChan // ここでシグナルを待つ
	log.Printf("ℹ️ Received syscall: %v, initiating graceful shutdown...", s)
	
	// ここでシャットダウン前の処理（例: 進行中のリクエストの完了を待つなど）を実装できる
	// 今回はDBクローズのみ
	if db != nil {
		log.Println("ℹ️ Closing database connection...")
		if err := db.Close(); err != nil {
			log.Printf("Error closing database: %v\n", err)
		} else {
			log.Println("✅ Database connection closed successfully.")
		}
	}
	log.Println("✅ Server shut down gracefully.")
	// os.Exit(0) は通常、Graceful Shutdownの最後に実行されるか、
	// シグナルハンドラとは別にメインゴルーチンが終了することで自然に終了するのを待つ
}