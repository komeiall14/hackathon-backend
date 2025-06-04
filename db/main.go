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
	// "time" // ulid.Make() が引数を取らないバージョンでは直接は不要

	_ "github.com/go-sql-driver/mysql" // MySQLドライバ
	"github.com/joho/godotenv"         // .envファイル読み込み用
	"github.com/oklog/ulid/v2"         // ULID生成用
	"github.com/rs/cors"  
)

// UserResForHTTPGet はHTTPレスポンス用のユーザー情報の構造体です。
type UserResForHTTPGet struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}

var db *sql.DB // グローバルなデータベース接続プール

// init関数はmain関数より先に実行され、アプリケーションの初期化処理を行います。
func init() {
	log.Println("アプリケーション初期化処理を開始します...")

	// Cloud Run環境でない場合（ローカル開発時など）は .env ファイルから環境変数を読み込む
	if os.Getenv("GOOGLE_CLOUD_PROJECT") == "" { // GOOGLE_CLOUD_PROJECTはCloud Runで通常設定される環境変数
		log.Println(".env ファイルの読み込みを試みます...")
		err := godotenv.Load()
		if err != nil {
			log.Printf("警告: .env ファイルの読み込みに失敗しました。環境変数またはデフォルト値を使用します。エラー: %v\n", err)
		} else {
			log.Println(".env ファイルを正常に読み込みました。")
		}
	} else {
		log.Println("Cloud Run環境を検出しました。.env ファイルは読み込みません。")
	}

	// 環境変数からMySQL接続情報を取得
	mysqlUser := os.Getenv("MYSQL_USER")
	mysqlUserPwd := os.Getenv("MYSQL_PASSWORD") // Cloud RunではMYSQL_PASSWORD, ローカルでは.envで設定
	mysqlDatabase := os.Getenv("MYSQL_DATABASE")
	mysqlHost := os.Getenv("MYSQL_HOST")         // Cloud Runでは /cloudsql/... のソケットパス, ローカルではIPアドレス
	mysqlPort := os.Getenv("MYSQL_PORT")         // ローカル開発時(Cloud SQL Proxy用)に主に設定

	// 取得した環境変数の値をログに出力（パスワードはマスク）
	log.Printf("読み込まれた環境変数:\n  MYSQL_USER: %s\n  MYSQL_PASSWORD: [設定済みか確認してください]\n  MYSQL_DATABASE: %s\n  MYSQL_HOST: %s\n  MYSQL_PORT: %s\n",
		mysqlUser, mysqlDatabase, mysqlHost, mysqlPort)

	// 必須環境変数のチェック
	if mysqlUser == "" || mysqlDatabase == "" || mysqlHost == "" {
		log.Panicln("エラー: データベース接続に必要な環境変数 (MYSQL_USER, MYSQL_DATABASE, MYSQL_HOST) が設定されていません。")
	}
	if os.Getenv("GOOGLE_CLOUD_PROJECT") != "" && mysqlUserPwd == "" { // Cloud Run環境ではパスワードも必須
		log.Panicln("エラー: Cloud Run環境でMYSQL_PASSWORDが設定されていません。")
	}


	var dsn string
	// MYSQL_HOSTが /cloudsql/ で始まるか（Cloud SQL Unixソケットの典型的なパス）どうかでDSNを分岐
	if strings.HasPrefix(mysqlHost, "/cloudsql/") { // Cloud RunなどでのUnixソケット接続
		dsn = fmt.Sprintf("%s:%s@unix(%s)/%s?parseTime=true", mysqlUser, mysqlUserPwd, mysqlHost, mysqlDatabase)
		log.Printf("DSN (Unixソケット): %s\n", fmt.Sprintf("%s:[秘匿]@unix(%s)/%s?parseTime=true", mysqlUser, mysqlHost, mysqlDatabase))
	} else { // TCP/IP接続 (ローカル開発など)
		if mysqlPort == "" {
			mysqlPort = "3308" // ローカルCloud SQL Proxy用デフォルトポート
			log.Printf("MYSQL_PORTが未設定のため、デフォルトの %s を使用します。\n", mysqlPort)
		}
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", mysqlUser, mysqlUserPwd, mysqlHost, mysqlPort, mysqlDatabase)
		log.Printf("DSN (TCP/IP): %s\n", fmt.Sprintf("%s:[秘匿]@tcp(%s:%s)/%s?parseTime=true", mysqlUser, mysqlHost, mysqlPort, mysqlDatabase))
	}

	log.Println("データベース接続を試みます (sql.Open)...")
	_db, err := sql.Open("mysql", dsn)
	if err != nil {
		// sql.Openは実際には接続検証を行わないため、通常ここでのエラーは稀。DSNの形式不正など。
		log.Panicf("致命的エラー: sql.Open に失敗しました。DSNが不正である可能性があります。エラー: %v\n", err)
	}

	log.Println("データベースへのPingを試みます...")
	if err := _db.Ping(); err != nil {
		// Pingで実際の接続を検証
		maskedDsn := dsn // 実際のDSNをそのままログに出すのはセキュリティリスクがあるため、必要に応じてマスク処理
		if mysqlUserPwd != "" {
			maskedDsn = strings.Replace(dsn, mysqlUserPwd, "[PASSWORD_MASKED]", 1)
		}
		log.Panicf("致命的エラー: _db.Ping に失敗しました。データベースへの接続を確認できません。\n  エラー詳細: %v\n  DSN (マスク済): %s\n", err, maskedDsn)
	}
	db = _db // グローバル変数に代入
	log.Println("✅ DB接続に成功しました。初期化処理を完了します。")
}

// handler関数はHTTPリクエストを処理します。
// handler関数はHTTPリクエストを処理します。
func handler(w http.ResponseWriter, r *http.Request) {
    // 古い手動CORS設定とOPTIONSハンドリングは削除します。
    // 代わりにmain関数でcorsミドルウェアが適用されます。

    log.Printf("受信リクエスト: Method=%s, URL=%s\n", r.Method, r.URL.String())

    switch r.Method {
    case http.MethodGet:
    	name := r.URL.Query().Get("name")
		if name != "" { // nameクエリパラメータがある場合は特定ユーザーを検索
			log.Printf("特定ユーザー検索を開始します: name=%s\n", name)
			rows, err := db.Query("SELECT id, name, age FROM user WHERE name = ?", name)
			if err != nil {
				log.Printf("エラー: db.Query (name=%s) に失敗しました。エラー: %v\n", name, err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			defer rows.Close()

			users := make([]UserResForHTTPGet, 0)
			for rows.Next() {
				var u UserResForHTTPGet
				if err := rows.Scan(&u.Id, &u.Name, &u.Age); err != nil {
					log.Printf("エラー: rows.Scan (特定ユーザー検索) に失敗しました。エラー: %v\n", err)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					return
				}
				users = append(users, u)
			}
			if err := rows.Err(); err != nil { // ループ中のエラーを確認
				log.Printf("エラー: rows.Err (特定ユーザー検索) でエラーが発生しました。エラー: %v\n", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}

			log.Printf("特定ユーザー検索成功: %d件見つかりました (name=%s)\n", len(users), name)
			bytes, err := json.Marshal(users)
			if err != nil {
				log.Printf("エラー: json.Marshal (特定ユーザー検索) に失敗しました。エラー: %v\n", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(bytes)
			return
		}

		// nameクエリパラメータがない場合は全ユーザーを取得
		log.Println("全ユーザー検索を開始します...")
		rows, err := db.Query("SELECT id, name, age FROM user")
		if err != nil {
			log.Printf("エラー: db.Query (all users) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		users := make([]UserResForHTTPGet, 0)
		for rows.Next() {
			var u UserResForHTTPGet
			if err := rows.Scan(&u.Id, &u.Name, &u.Age); err != nil {
				log.Printf("エラー: rows.Scan (all users) に失敗しました。エラー: %v\n", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			users = append(users, u)
		}
		if err := rows.Err(); err != nil { // ループ中のエラーを確認
			log.Printf("エラー: rows.Err (all users) でエラーが発生しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		log.Printf("全ユーザー検索成功: %d件見つかりました\n", len(users))
		bytes, err := json.Marshal(users)
		if err != nil {
			log.Printf("エラー: json.Marshal (all users) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(bytes)

	case http.MethodPost:
		log.Println("ユーザー作成処理を開始します...")
		var newUser UserResForHTTPGet
		if err := json.NewDecoder(r.Body).Decode(&newUser); err != nil {
			log.Printf("エラー: リクエストボディのJSONデコードに失敗しました。エラー: %v\n", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// バリデーション
		if newUser.Name == "" {
			log.Println("バリデーションエラー: Name が空です。")
			http.Error(w, "Name is empty", http.StatusBadRequest)
			return
		}
		if len(newUser.Name) > 50 {
			log.Printf("バリデーションエラー: Name が長すぎます (最大50文字)。入力値: %s\n", newUser.Name)
			http.Error(w, "Name is too long (max 50 characters)", http.StatusBadRequest)
			return
		}
		if newUser.Age < 0 { // 年齢は0歳以上とする (要件に応じて変更)
			log.Printf("バリデーションエラー: Age が負の値です。入力値: %d\n", newUser.Age)
			http.Error(w, "Age must be a non-negative value", http.StatusBadRequest)
			return
		}

		// ULIDの生成
		newId := ulid.Make().String()
		log.Printf("新規ユーザーID (ULID) を生成しました: %s\n", newId)

		// トランザクションを開始
		tx, err := db.Begin()
		if err != nil {
			log.Printf("エラー: db.Begin (トランザクション開始) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		// データベースに挿入
		_, err = tx.Exec("INSERT INTO user (id, name, age) VALUES (?, ?, ?)", newId, newUser.Name, newUser.Age)
		if err != nil {
			tx.Rollback() // エラー時はロールバック
			log.Printf("エラー: tx.Exec (INSERT) に失敗しました。トランザクションをロールバックします。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		// トランザクションをコミット
		if err := tx.Commit(); err != nil {
			log.Printf("エラー: tx.Commit (トランザクションコミット) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		log.Printf("ユーザー作成成功: ID=%s, Name=%s, Age=%d\n", newId, newUser.Name, newUser.Age)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated) // 201 Created
		json.NewEncoder(w).Encode(map[string]string{"id": newId})
	case http.MethodOptions:
		// CORSのためのOPTIONSリクエストを処理
		log.Println("CORSのためのOPTIONSリクエストを処理します...")
		w.Header().Set("Access-Control-Allow-Origin", "https://hackathon-frontend-ver.vercel.app") // 必要に応じて特定のオリジンに変更
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.WriteHeader(http.StatusOK) // 200 OK
		log.Println("OPTIONSリクエストに対するCORSヘッダを設定しました。")
		return // OPTIONSリクエストはここで終了
	default:
		log.Printf("メソッド不允许: HTTPメソッド %s は許可されていません。\n", r.Method)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

// main関数はアプリケーションのエントリポイントです。
func main() {
    log.Println("main 関数を開始します...")
    log.Println("DEBUG: main function started successfully. Proceeding to setup router and CORS.") // ★追加するログ

    // カスタムのServeMuxを作成
    // http.DefaultServeMux (nil) の代わりにこれを使用することで、CORSミドルウェアを適用しやすくなります。
    log.Println("DEBUG: Mux router creation point.") // ★追加するログ
    mux := http.NewServeMux()
    mux.HandleFunc("/user", handler) // /user/パスにハンドラを割り当て
    log.Println("/user エンドポイントのハンドラを設定しました。")

    // CORSミドルウェアの設定
    log.Println("DEBUG: CORS middleware configuration point.") // ★追加するログ
    c := cors.New(cors.Options{
        AllowedOrigins: []string{
            "http://localhost:3000",
            "http://localhost:5173",
            "https://hackathon-frontend-ver.vercel.app",
            "https://hackathon-frontend-ver-git-main-komeiall14s-projects.vercel.app",
            "https://hackathon-frontend-a0lipvgmk-komeiall14s-projects.vercel.app",
        },
        AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowedHeaders:   []string{"Content-Type", "Authorization"},
        AllowCredentials: true,
        Debug:            true, // これがtrueであることを確認
    })

    // CORSミドルウェアをHTTPハンドラに適用
    handlerWithCORS := c.Handler(mux)
    log.Println("DEBUG: CORS middleware applied to handler.") // ★追加するログ

    closeDBWithSysCall()
    log.Println("システムコールによるDBクローズ処理を設定しました。")

    // Cloud Runから提供されるPORT環境変数を尊重する
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
        log.Printf("環境変数 PORT が未設定のため、デフォルトの %s を使用します。\n", port)
    }

    log.Printf("HTTPサーバーをポート %s で起動します...\n", port)
    log.Printf("DEBUG: About to call ListenAndServe. Port: %s", port) // ★追加するログ（ポート番号も確認）

    // サーバーを起動し、CORSが適用されたハンドラを渡す
    if err := http.ListenAndServe(":"+port, handlerWithCORS); err != nil {
        log.Fatalf("致命的エラー: ListenAndServe に失敗しました。ポート %s を使用できませんでした: %v", port, err)
    }
}

// closeDBWithSysCall関数はOSのシグナル(SIGTERM, SIGINT)を補足し、DB接続を安全にクローズします。
func closeDBWithSysCall() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sig
		log.Printf("システムコールを受信しました: %v。シャットダウン処理を開始します。\n", s)

		if db != nil { // dbが初期化されていればクローズ処理を行う
			log.Println("データベース接続をクローズします...")
			if err := db.Close(); err != nil {
				log.Printf("致命的エラー: db.Close に失敗しました。エラー: %v\n", err)
				// ここで os.Exit(1) などで終了させることも検討できる
			}
			log.Println("✅ データベース接続を正常にクローズしました。")
		} else {
			log.Println("データベース接続(db)がnilのため、クローズ処理はスキップされました。")
		}
		log.Println("アプリケーションを終了します。")
		os.Exit(0) // プログラムを正常終了させる
	}()
}