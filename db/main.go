package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	// "time" // ulid.Make()が引数を取らない場合、直接は不要になる可能性がありますが、他の用途でtimeを使っている場合は残します。

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"github.com/oklog/ulid/v2" // ULIDライブラリをインポート
)

type UserResForHTTPGet struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}

var db *sql.DB

func init() {
	if os.Getenv("GOOGLE_CLOUD_PROJECT") == "" {
		err := godotenv.Load()
		if err != nil {
			log.Println("Warning: .env file not found or error loading, using system environment variables or defaults")
		}
	}

	mysqlUser := os.Getenv("MYSQL_USER")
	mysqlUserPwd := os.Getenv("MYSQL_PASSWORD")
	mysqlDatabase := os.Getenv("MYSQL_DATABASE")
	mysqlHost := os.Getenv("MYSQL_HOST")
	mysqlPort := os.Getenv("MYSQL_PORT")

	var dsn string
	if strings.HasPrefix(mysqlHost, "/") { // Unixソケットパス
		dsn = fmt.Sprintf("%s:%s@unix(%s)/%s?parseTime=true", mysqlUser, mysqlUserPwd, mysqlHost, mysqlDatabase)
	} else { // TCP/IP接続
		if mysqlPort == "" {
			mysqlPort = "3308"
		}
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", mysqlUser, mysqlUserPwd, mysqlHost, mysqlPort, mysqlDatabase)
	}

	_db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("fail: sql.Open, %v\n", err)
	}

	if err := _db.Ping(); err != nil {
		log.Fatalf("fail: _db.Ping, %v\n", err)
	}
	db = _db
	log.Println("✅ DB接続に成功しました")
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch r.Method {
	case http.MethodGet:
		name := r.URL.Query().Get("name")
		if name != "" {
			rows, err := db.Query("SELECT id, name, age FROM user WHERE name = ?", name)
			if err != nil {
				log.Printf("fail: db.Query (name=%s), %v\n", name, err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			defer rows.Close()

			users := make([]UserResForHTTPGet, 0)
			for rows.Next() {
				var u UserResForHTTPGet
				if err := rows.Scan(&u.Id, &u.Name, &u.Age); err != nil {
					log.Printf("fail: rows.Scan, %v\n", err)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					return
				}
				users = append(users, u)
			}
			if err := rows.Err(); err != nil {
				log.Printf("fail: rows.Err, %v\n", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}

			bytes, err := json.Marshal(users)
			if err != nil {
				log.Printf("fail: json.Marshal, %v\n", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(bytes)
			return
		}

		rows, err := db.Query("SELECT id, name, age FROM user")
		if err != nil {
			log.Printf("fail: db.Query (all users), %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		users := make([]UserResForHTTPGet, 0)
		for rows.Next() {
			var u UserResForHTTPGet
			if err := rows.Scan(&u.Id, &u.Name, &u.Age); err != nil {
				log.Printf("fail: rows.Scan (all users), %v\n", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			users = append(users, u)
		}
		if err := rows.Err(); err != nil {
			log.Printf("fail: rows.Err (all users), %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		bytes, err := json.Marshal(users)
		if err != nil {
			log.Printf("fail: json.Marshal (all users), %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(bytes)

	case http.MethodPost:
		var newUser UserResForHTTPGet
		if err := json.NewDecoder(r.Body).Decode(&newUser); err != nil {
			log.Printf("fail: json.NewDecoder.Decode, %v\n", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if newUser.Name == "" {
			http.Error(w, "Name is empty", http.StatusBadRequest)
			return
		}
		if len(newUser.Name) > 50 {
			http.Error(w, "Name is too long (max 50 characters)", http.StatusBadRequest)
			return
		}
		if newUser.Age < 0 {
			http.Error(w, "Age must be a positive value", http.StatusBadRequest)
			return
		}

		// ULIDの生成 (修正箇所)
		newId := ulid.Make().String()

		tx, err := db.Begin()
		if err != nil {
			log.Printf("fail: db.Begin, %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		_, err = tx.Exec("INSERT INTO user (id, name, age) VALUES (?, ?, ?)", newId, newUser.Name, newUser.Age)
		if err != nil {
			tx.Rollback()
			log.Printf("fail: tx.Exec, %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if err := tx.Commit(); err != nil {
			log.Printf("fail: tx.Commit, %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": newId})

	default:
		log.Printf("fail: HTTP Method is %s (Not Allowed)\n", r.Method)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func main() {
	http.HandleFunc("/user", handler)
	closeDBWithSysCall()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
		log.Printf("Defaulting to port %s", port)
	}

	log.Printf("Listening on port %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func closeDBWithSysCall() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sig
		log.Printf("received syscall, %v", s)

		if db != nil {
			if err := db.Close(); err != nil {
				log.Fatalf("fail: db.Close, %v\n", err)
			}
			log.Printf("success: db.Close()")
		}
		os.Exit(0)
	}()
}