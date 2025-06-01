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
	log.Println("DEBUG: init() started")

	log.Printf("DEBUG: K_SERVICE environment variable: [%s]\n", os.Getenv("K_SERVICE"))
	log.Printf("DEBUG: GOOGLE_CLOUD_PROJECT environment variable: [%s]\n", os.Getenv("GOOGLE_CLOUD_PROJECT"))

	// Cloud Run環境でない場合（ローカル開発時など）に .env ファイルを読み込む
	if os.Getenv("K_SERVICE") == "" {
		log.Println("DEBUG: Not a Cloud Run environment (K_SERVICE is empty or not set), attempting to load .env file")
		if err := godotenv.Load(); err != nil {
			log.Printf("DEBUG: Warning - .env file not found or error loading: %v. Will use system environment variables.\n", err)
		} else {
			log.Println("DEBUG: Successfully loaded .env file for local development")
		}
	} else {
		log.Println("DEBUG: Cloud Run environment detected (K_SERVICE is set)")
	}

	mysqlUser := os.Getenv("MYSQL_USER")
	log.Printf("DEBUG: MYSQL_USER: [%s]\n", mysqlUser)
	mysqlUserPwd := os.Getenv("MYSQL_PASSWORD")
	// パスワード自体はログに出力しない方が良いが、取得できたかどうかの確認
	if mysqlUserPwd != "" {
		log.Println("DEBUG: MYSQL_PASSWORD is set (not empty)")
	} else {
		log.Println("DEBUG: MYSQL_PASSWORD is NOT set (empty)")
	}
	mysqlDatabase := os.Getenv("MYSQL_DATABASE")
	log.Printf("DEBUG: MYSQL_DATABASE: [%s]\n", mysqlDatabase)

	var dsn string
	if os.Getenv("K_SERVICE") != "" { // Cloud Run環境
		log.Println("DEBUG: Configuring DSN for Cloud Run (Unix socket)")
		instanceConnectionName := os.Getenv("INSTANCE_CONNECTION_NAME")
		log.Printf("DEBUG: INSTANCE_CONNECTION_NAME: [%s]\n", instanceConnectionName)
		if instanceConnectionName == "" {
			log.Fatal("FATAL: INSTANCE_CONNECTION_NAME environment variable not set for Cloud Run")
		}
		socketDir := os.Getenv("DB_SOCKET_DIR")
		log.Printf("DEBUG: DB_SOCKET_DIR: [%s]\n", socketDir)
		if socketDir == "" {
			socketDir = "/cloudsql"
			log.Printf("DEBUG: DB_SOCKET_DIR not set, defaulting to: %s\n", socketDir)
		}
		dsn = fmt.Sprintf("%s:%s@unix(%s/%s)/%s?parseTime=true",
			mysqlUser,
			mysqlUserPwd,
			socketDir,
			instanceConnectionName,
			mysqlDatabase)
		log.Println("DEBUG: ✅ DB接続にUnixソケットを使用します (Cloud Run environment)")
	} else { // ローカル開発環境など
		log.Println("DEBUG: Configuring DSN for local development (TCP)")
		mysqlHost := os.Getenv("MYSQL_HOST")
		log.Printf("DEBUG: MYSQL_HOST (local): [%s]\n", mysqlHost)
		mysqlPort := os.Getenv("MYSQL_PORT")
		log.Printf("DEBUG: MYSQL_PORT (local): [%s]\n", mysqlPort)
		if mysqlPort == "" {
			mysqlPort = "3306"
			log.Printf("DEBUG: MYSQL_PORT not set, defaulting to: %s\n", mysqlPort)
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
		log.Println("DEBUG: ✅ DB接続にTCPを使用します (Local environment)")
	}

	if mysqlUser == "" {
		log.Fatal("FATAL: MYSQL_USER environment variable is not set.")
	}
	if mysqlUserPwd == "" {
		log.Fatal("FATAL: MYSQL_PASSWORD environment variable is not set.")
	}
	if mysqlDatabase == "" {
		log.Fatal("FATAL: MYSQL_DATABASE environment variable is not set.")
	}

	// DSNにパスワードが含まれるため、ログ出力時はマスキングする
	maskedDsn := fmt.Sprintf("%s:******@%s...", mysqlUser, dsn[len(mysqlUser)+1+len(mysqlUserPwd):]) // 簡単なマスキング
	log.Printf("DEBUG: Attempting sql.Open() with DSN (password masked): %s\n", maskedDsn)


	_db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("FATAL: sql.Open failed, DSN (password masked): %s, error: %v\n", maskedDsn, err)
	}
	log.Println("DEBUG: sql.Open() successful")

	_db.SetMaxOpenConns(10)
	_db.SetMaxIdleConns(10)
	_db.SetConnMaxLifetime(5 * time.Minute)
	log.Println("DEBUG: Database connection pool configured")

	log.Println("DEBUG: Attempting _db.Ping()...")
	if err := _db.Ping(); err != nil {
		log.Fatalf("FATAL: _db.Ping failed, DSN (password masked): %s, error: %v\n", maskedDsn, err)
	}
	db = _db
	log.Println("✅ DB接続に成功しました (init() finished)")
}

func userHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("DEBUG: userHandler received request: Method=%s, URL=%s\n", r.Method, r.URL.String())
	switch r.Method {
	case http.MethodGet:
		log.Println("DEBUG: userHandler - GET request processing started")
		name := r.URL.Query().Get("name")
		if name == "" {
			log.Println("DEBUG: userHandler - GET - name query parameter is empty")
			http.Error(w, `{"error": "name query parameter is required"}`, http.StatusBadRequest)
			return
		}
		log.Printf("DEBUG: userHandler - GET - Searching for name: %s\n", name)

		rows, err := db.Query("SELECT id, name, age FROM user WHERE name = ?", name)
		if err != nil {
			log.Printf("ERROR: userHandler - GET - db.Query failed for name %s: %v\n", name, err)
			http.Error(w, `{"error": "Internal server error"}`, http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		log.Println("DEBUG: userHandler - GET - db.Query successful")

		users := make([]User, 0)
		log.Println("DEBUG: userHandler - GET - Starting rows.Next()")
		for rows.Next() {
			var u User
			if err := rows.Scan(&u.Id, &u.Name, &u.Age); err != nil {
				log.Printf("ERROR: userHandler - GET - rows.Scan failed: %v\n", err)
				http.Error(w, `{"error": "Internal server error during scan"}`, http.StatusInternalServerError)
				return
			}
			users = append(users, u)
			log.Printf("DEBUG: userHandler - GET - Scanned user: %+v\n", u)
		}
		if err := rows.Err(); err != nil {
			log.Printf("ERROR: userHandler - GET - rows.Err: %v\n", err)
			http.Error(w, `{"error": "Internal server error with rows"}`, http.StatusInternalServerError)
			return
		}
		log.Printf("DEBUG: userHandler - GET - Found %d users for name: %s\n", len(users), name)

		if len(users) == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message": "No users found"}`))
			return
		}

		bytes, err := json.Marshal(users)
		if err != nil {
			log.Printf("ERROR: userHandler - GET - json.Marshal users failed: %v\n", err)
			http.Error(w, `{"error": "Internal server error during marshal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(bytes)
		log.Println("DEBUG: userHandler - GET - Successfully sent response")

	case http.MethodPost:
		log.Println("DEBUG: userHandler - POST request processing started")
		var reqUser struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqUser); err != nil {
			log.Printf("ERROR: userHandler - POST - json decoding request body failed: %v\n", err)
			http.Error(w, `{"error": "Invalid request body"}`, http.StatusBadRequest)
			return
		}
		log.Printf("DEBUG: userHandler - POST - Decoded request body: Name=%s, Age=%d\n", reqUser.Name, reqUser.Age)

		// バリデーション
		if reqUser.Name == "" {
			log.Println("DEBUG: userHandler - POST - Validation failed: Name is required")
			http.Error(w, `{"error": "Name is required"}`, http.StatusBadRequest)
			return
		}
		if len(reqUser.Name) > 50 {
			log.Println("DEBUG: userHandler - POST - Validation failed: Name must be 50 characters or less")
			http.Error(w, `{"error": "Name must be 50 characters or less"}`, http.StatusBadRequest)
			return
		}
		if reqUser.Age < 20 || reqUser.Age > 80 {
			log.Printf("DEBUG: userHandler - POST - Validation failed: Invalid age: %d\n", reqUser.Age)
			http.Error(w, `{"error": "Age must be between 20 and 80"}`, http.StatusBadRequest)
			return
		}
		log.Println("DEBUG: userHandler - POST - Validation successful")

		t := time.Now().UTC()
		entropy := ulid.Monotonic(rand.New(rand.NewSource(t.UnixNano())), 0)
		newID := ulid.MustNew(ulid.Timestamp(t), entropy).String()
		log.Printf("DEBUG: userHandler - POST - Generated ULID: %s for Name: %s, Age: %d\n", newID, reqUser.Name, reqUser.Age)

		log.Println("DEBUG: userHandler - POST - Beginning transaction...")
		tx, err := db.Begin()
		if err != nil {
			log.Printf("ERROR: userHandler - POST - db.Begin failed: %v\n", err)
			http.Error(w, `{"error": "Internal server error on db.Begin"}`, http.StatusInternalServerError)
			return
		}
		log.Println("DEBUG: userHandler - POST - Transaction begun successfully")

		log.Printf("DEBUG: userHandler - POST - Executing INSERT with ID: %s, Name: %s, Age: %d\n", newID, reqUser.Name, reqUser.Age)
		_, err = tx.Exec("INSERT INTO user (id, name, age) VALUES (?, ?, ?)", newID, reqUser.Name, reqUser.Age)
		if err != nil {
			tx.Rollback()
			log.Printf("ERROR: userHandler - POST - tx.Exec INSERT user failed: %v. Transaction rolled back.\n", err)
			http.Error(w, `{"error": "Failed to create user"}`, http.StatusInternalServerError)
			return
		}
		log.Printf("DEBUG: userHandler - POST - ✅ tx.Exec INSERT user success for ID: %s\n", newID)

		log.Println("DEBUG: userHandler - POST - Committing transaction...")
		if err := tx.Commit(); err != nil {
			log.Printf("ERROR: userHandler - POST - tx.Commit failed: %v\n", err)
			http.Error(w, `{"error": "Failed to commit transaction"}`, http.StatusInternalServerError)
			return
		}
		log.Println("DEBUG: userHandler - POST - ✅ tx.Commit success")

		res := struct {
			Id string `json:"id"`
		}{Id: newID}
		resBytes, err := json.Marshal(res)
		if err != nil {
			log.Printf("ERROR: userHandler - POST - json.Marshal created user ID failed: %v\n", err)
			http.Error(w, `{"error": "Internal server error generating response"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(resBytes)
		log.Println("DEBUG: userHandler - POST - Successfully sent response with new user ID")

	default:
		log.Printf("DEBUG: userHandler - Method %s not allowed for /user\n", r.Method)
		http.Error(w, `{"error": "Method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func allUsersHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("DEBUG: allUsersHandler received request: Method=%s, URL=%s\n", r.Method, r.URL.String())
	if r.Method != http.MethodGet {
		log.Printf("DEBUG: allUsersHandler - Method %s not allowed for /users\n", r.Method)
		http.Error(w, `{"error": "Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	log.Println("DEBUG: allUsersHandler - GET request processing started")

	rows, err := db.Query("SELECT id, name, age FROM user ORDER BY name")
	if err != nil {
		log.Printf("ERROR: allUsersHandler - db.Query for all users failed: %v\n", err)
		http.Error(w, `{"error": "Internal server error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	log.Println("DEBUG: allUsersHandler - db.Query successful")

	users := make([]User, 0)
	log.Println("DEBUG: allUsersHandler - Starting rows.Next()")
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Id, &u.Name, &u.Age); err != nil {
			log.Printf("ERROR: allUsersHandler - rows.Scan failed: %v\n", err)
			http.Error(w, `{"error": "Internal server error during scan"}`, http.StatusInternalServerError)
			return
		}
		users = append(users, u)
		log.Printf("DEBUG: allUsersHandler - Scanned user: %+v\n", u)
	}
	if err := rows.Err(); err != nil {
		log.Printf("ERROR: allUsersHandler - rows.Err: %v\n", err)
		http.Error(w, `{"error": "Internal server error with rows"}`, http.StatusInternalServerError)
		return
	}
	log.Printf("DEBUG: allUsersHandler - Found %d users\n", len(users))

	bytes, err := json.Marshal(users)
	if err != nil {
		log.Printf("ERROR: allUsersHandler - json.Marshal all users failed: %v\n", err)
		http.Error(w, `{"error": "Internal server error during marshal"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(bytes)
	log.Println("DEBUG: allUsersHandler - Successfully sent response")
}

func main() {
	log.Println("🚀 DEBUG: main() - Server starting...")

	http.HandleFunc("/user", userHandler)
	http.HandleFunc("/users", allUsersHandler)

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	port := os.Getenv("PORT")
	log.Printf("DEBUG: main() - PORT environment variable: [%s]\n", port)
	if port == "" {
		port = "8080"
		log.Printf("DEBUG: main() - PORT not set, defaulting to %s\n", port)
	}
	serverAddr := fmt.Sprintf(":%s", port)
	log.Printf("DEBUG: main() - ✅ Listening on port %s...\n", port)

	go func() {
		log.Println("DEBUG: main() - Starting HTTP server...")
		if err := http.ListenAndServe(serverAddr, nil); err != nil && err != http.ErrServerClosed {
			log.Fatalf("FATAL: main() - Could not listen on %s: %v\n", serverAddr, err)
		}
	}()
	log.Println("DEBUG: main() - ✅ HTTP server is serving")

	s := <-stopChan
	log.Printf("ℹ️ DEBUG: main() - Received syscall: %v, initiating graceful shutdown...", s)

	if db != nil {
		log.Println("ℹ️ DEBUG: main() - Closing database connection...")
		if err := db.Close(); err != nil {
			log.Printf("ERROR: main() - Error closing database: %v\n", err)
		} else {
			log.Println("DEBUG: main() - ✅ Database connection closed successfully.")
		}
	}
	log.Println("✅ DEBUG: main() - Server shut down gracefully.")
}