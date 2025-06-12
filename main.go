package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"cloud.google.com/go/storage"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	"github.com/oklog/ulid/v2"
	"github.com/rs/cors"
	"google.golang.org/api/option"
)
import (
	"firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
)
type UserResForHTTPGet struct {
    Id               string  `json:"id"`
    Name             string  `json:"name"`
    Age              *int    `json:"age"`
    FirebaseUID      *string `json:"firebase_uid"`
    Bio              *string `json:"bio"`               // ★ 追加
    ProfileImageURL  *string `json:"profile_image_url"` // ★ 追加
    HeaderImageURL   *string `json:"header_image_url"`  // ★ 追加
}


type Post struct {
	PostID      string `json:"post_id"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	Content     string `json:"content"`
	ImageURL    *string `json:"image_url"`
	CreatedAt   string `json:"created_at"`
	LikeCount   int    `json:"like_count"`
	IsLikedByMe bool   `json:"is_liked_by_me"`
	ReplyCount  int    `json:"reply_count"`
}

var db *sql.DB
var firebaseAuth *auth.Client

type contextKey string
const userIDKey contextKey = "userID"

func init() {
	log.Println("アプリケーション初期化処理を開始します...")
	if os.Getenv("GOOGLE_CLOUD_PROJECT") == "" {
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
	mysqlUser := os.Getenv("MYSQL_USER")
	mysqlUserPwd := os.Getenv("MYSQL_PASSWORD")
	mysqlDatabase := os.Getenv("MYSQL_DATABASE")
	mysqlHost := os.Getenv("MYSQL_HOST")
	mysqlPort := os.Getenv("MYSQL_PORT")
	log.Printf("読み込まれた環境変数:\n  MYSQL_USER: %s\n  MYSQL_PASSWORD: [設定済みか確認してください]\n  MYSQL_DATABASE: %s\n  MYSQL_HOST: %s\n  MYSQL_PORT: %s\n",
		mysqlUser, mysqlDatabase, mysqlHost, mysqlPort)
	if mysqlUser == "" || mysqlDatabase == "" || mysqlHost == "" {
		log.Panicln("エラー: データベース接続に必要な環境変数 (MYSQL_USER, MYSQL_DATABASE, MYSQL_HOST) が設定されていません。")
	}
	if os.Getenv("GOOGLE_CLOUD_PROJECT") != "" && mysqlUserPwd == "" {
		log.Panicln("エラー: Cloud Run環境でMYSQL_PASSWORDが設定されていません。")
	}
	var dsn string
	if strings.HasPrefix(mysqlHost, "/cloudsql/") {
		dsn = fmt.Sprintf("%s:%s@unix(%s)/%s?parseTime=true", mysqlUser, mysqlUserPwd, mysqlHost, mysqlDatabase)
		log.Printf("DSN (Unixソケット): %s\n", fmt.Sprintf("%s:[秘匿]@unix(%s)/%s?parseTime=true", mysqlUser, mysqlHost, mysqlDatabase))
	} else {
		if mysqlPort == "" {
			mysqlPort = "3308"
			log.Printf("MYSQL_PORTが未設定のため、デフォルトの %s を使用します。\n", mysqlPort)
		}
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", mysqlUser, mysqlUserPwd, mysqlHost, mysqlPort, mysqlDatabase)
		log.Printf("DSN (TCP/IP): %s\n", fmt.Sprintf("%s:[秘匿]@tcp(%s:%s)/%s?parseTime=true", mysqlUser, mysqlHost, mysqlPort, mysqlDatabase))
	}
	log.Println("データベース接続を試みます (sql.Open)...")
	_db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Panicf("致命的エラー: sql.Open に失敗しました。: %v\n", err)
	}
	if err := _db.Ping(); err != nil {
		maskedDsn := strings.Replace(dsn, mysqlUserPwd, "[PASSWORD_MASKED]", 1)
		log.Panicf("致命的エラー: _db.Ping に失敗しました。\n  DSN(マスク済): %s\n  エラー: %v\n", maskedDsn, err)
	}
	db = _db
	log.Println("✅ DB接続に成功しました。")

	// Firebase Admin SDKの初期化
	serviceAccountKey := os.Getenv("FIREBASE_SERVICE_ACCOUNT_KEY_PATH")
	if serviceAccountKey == "" {
		log.Println("環境変数 FIREBASE_SERVICE_ACCOUNT_KEY_PATH が空のため、フォールバックのファイル名を使用します。")
		serviceAccountKey = "term7-459800-firebase-adminsdk-fbsvc-869b36b213.json"
	}

	// ★★★ このログを追加 ★★★
	// 実際にどのパスでファイルを読み込もうとしているかを確認するためのログ
	log.Printf("Firebase資格情報ファイルの読み込みを試みます: path=%s\n", serviceAccountKey)

	opt := option.WithCredentialsFile(serviceAccountKey)
	
	var app *firebase.App
	var initErr error // エラー変数を別名で宣言
	app, initErr = firebase.NewApp(context.Background(), nil, opt)
	if initErr != nil {
		log.Fatalf("Firebase Admin SDKの初期化エラー: %v\n", initErr)
	}

	client, authErr := app.Auth(context.Background()) // エラー変数を別名で宣言
	if authErr != nil {
		log.Fatalf("Firebase Authクライアントの取得エラー: %v\n", authErr)
	}
	firebaseAuth = client
	log.Println("✅ Firebase Admin SDKの初期化に成功しました。")
}

func handler(w http.ResponseWriter, r *http.Request) {
	log.Printf("受信リクエスト: Method=%s, URL=%s\n", r.Method, r.URL.String())

	switch r.Method {
	case http.MethodGet:
		name := r.URL.Query().Get("name")
		if name != "" {
			log.Printf("特定ユーザー検索を開始します: name=%s\n", name)
			rows, err := db.Query("SELECT id, name, age, firebase_uid FROM user WHERE name = ?", name)
			if err != nil {
				log.Printf("エラー: db.Query (name=%s) に失敗しました。エラー: %v\n", name, err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			defer rows.Close()

			users := make([]UserResForHTTPGet, 0)
			for rows.Next() {
				var u UserResForHTTPGet
				if err := rows.Scan(&u.Id, &u.Name, &u.Age, &u.FirebaseUID); err != nil {
					log.Printf("エラー: rows.Scan (特定ユーザー検索) に失敗しました。エラー: %v\n", err)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					return
				}
				users = append(users, u)
			}
			if err := rows.Err(); err != nil {
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

		log.Println("全ユーザー検索を開始します...")
		rows, err := db.Query("SELECT id, name, age, firebase_uid FROM user")
		if err != nil {
			log.Printf("エラー: db.Query (all users) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		users := make([]UserResForHTTPGet, 0)
		for rows.Next() {
			var u UserResForHTTPGet
			if err := rows.Scan(&u.Id, &u.Name, &u.Age, &u.FirebaseUID); err != nil {
				log.Printf("エラー: rows.Scan (all users) に失敗しました。エラー: %v\n", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			users = append(users, u)
		}
		if err := rows.Err(); err != nil {
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
		if newUser.Age != nil && *newUser.Age < 0 {
			log.Printf("バリデーションエラー: Age が負の値です。入力値: %d\n", *newUser.Age)
			http.Error(w, "Age must be a non-negative value", http.StatusBadRequest)
			return
		}

		newId := ulid.Make().String()
		log.Printf("新規ユーザーID (ULID) を生成しました: %s\n", newId)

		tx, err := db.Begin()
		if err != nil {
			log.Printf("エラー: db.Begin (トランザクション開始) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		_, err = tx.Exec("INSERT INTO user (id, name, age) VALUES (?, ?, ?)", newId, newUser.Name, newUser.Age)
		if err != nil {
			tx.Rollback()
			log.Printf("エラー: tx.Exec (INSERT) に失敗しました。トランザクションをロールバックします。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if err := tx.Commit(); err != nil {
			log.Printf("エラー: tx.Commit (トランザクションコミット) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		log.Printf("ユーザー作成成功: ID=%s, Name=%s\n", newId, newUser.Name)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": newId})

	case http.MethodDelete:
		log.Println("ユーザー削除処理を開始します...")

		userId := r.URL.Query().Get("id")
		if userId == "" {
			log.Println("エラー: ユーザーIDがクエリパラメータに指定されていません。")
			http.Error(w, "User ID is required as query parameter (e.g., /user?id={id})", http.StatusBadRequest)
			return
		}

		log.Printf("ユーザー削除リクエスト: ID=%s\n", userId)

		tx, err := db.Begin()
		if err != nil {
			log.Printf("エラー: db.Begin (トランザクション開始) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		result, err := tx.Exec("DELETE FROM user WHERE id = ?", userId)
		if err != nil {
			tx.Rollback()
			log.Printf("エラー: tx.Exec (DELETE) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			tx.Rollback()
			log.Printf("エラー: RowsAffected() に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if rowsAffected == 0 {
			tx.Rollback()
			log.Printf("エラー: ユーザーID=%s が見つかりませんでした。\n", userId)
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}

		if err := tx.Commit(); err != nil {
			log.Printf("エラー: tx.Commit (トランザクションコミット) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		log.Printf("ユーザーID=%s を正常に削除しました。\n", userId)
		w.WriteHeader(http.StatusNoContent)
	default:
		log.Printf("メソッド不允许: HTTPメソッド %s は許可されていません。\n", r.Method)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

// loginHandlerはFirebaseでのログイン情報を元に、DBのユーザー情報を同期（作成または更新）します。
func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "無効なリクエストボディです", http.StatusBadRequest)
		return
	}

	token, err := firebaseAuth.VerifyIDToken(context.Background(), req.Token)
	if err != nil {
		log.Printf("IDトークンの検証エラー: %v", err)
		http.Error(w, "無効なトークンです", http.StatusUnauthorized)
		return
	}

	firebaseUID := token.UID
	userName := token.Claims["name"].(string)
	profileImageURL := token.Claims["picture"].(string)

	tx, err := db.Begin()
	if err != nil {
		log.Printf("トランザクション開始エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var dbUserID string
	err = tx.QueryRow("SELECT id FROM user WHERE firebase_uid = ?", firebaseUID).Scan(&dbUserID)

	if err == sql.ErrNoRows {
		log.Printf("新規ユーザーを作成します: firebase_uid=%s", firebaseUID)
		newULID := ulid.Make().String()
		_, err = tx.Exec(
			"INSERT INTO user (id, firebase_uid, name, profile_image_url) VALUES (?, ?, ?, ?)",
			newULID, firebaseUID, userName, profileImageURL,
		)
		if err != nil {
			log.Printf("ユーザーの新規作成エラー: %v", err)
			http.Error(w, "ユーザー作成に失敗しました", http.StatusInternalServerError)
			return
		}
	} else if err != nil {
		log.Printf("ユーザー検索時のDBエラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	} else {
		log.Printf("既存ユーザー情報を更新します: firebase_uid=%s", firebaseUID)
		_, err = tx.Exec(
			"UPDATE user SET name = ?, profile_image_url = ? WHERE firebase_uid = ?",
			userName, profileImageURL, firebaseUID,
		)
		if err != nil {
			log.Printf("ユーザー情報更新エラー: %v", err)
			http.Error(w, "ユーザー情報の更新に失敗しました", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("トランザクションのコミットエラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "ログイン成功"})
}


func postsGetHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
        return
    }

    currentUserID := ""
	if userID, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = userID
	}

    query := `
        SELECT
            p.post_id, p.user_id, p.user_name, p.content, p.image_url, p.created_at,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count
        FROM
            posts p
        WHERE
            p.parent_post_id IS NULL
        ORDER BY
            p.created_at DESC
    `

    rows, err := db.Query(query, currentUserID)
    if err != nil {
        log.Printf("エラー: db.Query (all top-level posts) に失敗しました: %v", err)
        http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    posts := make([]Post, 0)
    for rows.Next() {
        var p Post
        if err := rows.Scan(&p.PostID, &p.UserID, &p.UserName, &p.Content, &p.ImageURL, &p.CreatedAt, &p.LikeCount, &p.IsLikedByMe, &p.ReplyCount); err != nil {
            log.Printf("エラー: rows.Scan (all top-level posts) に失敗しました: %v", err)
            http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
            return
        }
        posts = append(posts, p)
    }

    bytes, err := json.Marshal(posts)
    if err != nil {
        log.Printf("エラー: json.Marshal (all top-level posts) に失敗しました: %v", err)
        http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    w.Write(bytes)
    log.Println("トップレベルの投稿（いいね・リプライ数付き）の取得リクエスト成功")
}

// postCreateHandlerは新しい投稿を作成します。
func postCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// ★ 修正点1: 認証ミドルウェアからログインユーザーのFirebase UIDを安全に取得します。
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	var requestBody struct {
		Content  string `json:"content"`
		// UserIDは不要になったため削除
		UserName string `json:"user_name"`
		ImageURL string `json:"image_url,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		log.Printf("エラー: リクエストボディのデコードに失敗しました: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if requestBody.Content == "" && requestBody.ImageURL == "" {
		log.Println("バリデーションエラー: 投稿内容が空です。")
		http.Error(w, "投稿内容が空です", http.StatusBadRequest)
		return
	}

	// ★ 修正点2: ログイン時にユーザー情報は同期されるため、ここでのユーザー作成処理は不要になりました。
	// これによりコードがシンプルで安全になります。

	userName := requestBody.UserName
	if userName == "" {
		// ユーザー名が空の場合、Firebase Authの情報から取得します。
		userRecord, err := firebaseAuth.GetUser(context.Background(), userID)
		if err != nil {
			log.Printf("Firebaseからユーザー情報の取得に失敗: %v", err)
			userName = "名無しさん" // フォールバック
		} else {
			userName = userRecord.DisplayName
		}
	}

	var imageUrlToSave sql.NullString
	if requestBody.ImageURL != "" {
		imageUrlToSave.String = requestBody.ImageURL
		imageUrlToSave.Valid = true
	}

	postID := ulid.Make().String()

	// ★ 修正点3: 投稿を保存する際、認証情報から取得した安全なuserIDを保存します。
	_, err := db.Exec("INSERT INTO posts (post_id, user_id, user_name, content, image_url) VALUES (?, ?, ?, ?, ?)",
		postID,
		userID, // ここを認証済みのIDに変更
		userName,
		requestBody.Content,
		imageUrlToSave,
	)

	if err != nil {
		log.Printf("エラー: postsテーブルへのINSERTに失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	log.Printf("投稿作成成功: post_id=%s\n", postID)
	json.NewEncoder(w).Encode(map[string]string{"post_id": postID})
}


func postDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETEメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 5 || pathSegments[4] == "" {
		http.Error(w, "投稿IDが指定されていません", http.StatusBadRequest)
		return
	}
	postID := pathSegments[4]

	log.Printf("投稿削除リクエストを受信: post_id=%s", postID)

	tx, err := db.Begin()
	if err != nil {
		log.Printf("エラー: db.Begin (トランザクション開始) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	result, err := tx.Exec("DELETE FROM posts WHERE post_id = ?", postID)
	if err != nil {
		tx.Rollback()
		log.Printf("エラー: tx.Exec (delete post) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		tx.Rollback()
		log.Printf("エラー: result.RowsAffected に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if rowsAffected == 0 {
		tx.Rollback()
		log.Printf("削除対象の投稿が見つかりませんでした: post_id=%s", postID)
		http.Error(w, "投稿が見つかりません", http.StatusNotFound)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("エラー: tx.Commit に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	log.Printf("投稿削除成功: post_id=%s", postID)
	w.WriteHeader(http.StatusNoContent)
}

func likeHandler(w http.ResponseWriter, r *http.Request) {
	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 5 {
		http.Error(w, "投稿IDが指定されていません", http.StatusBadRequest)
		return
	}
	postID := pathSegments[4]

	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		log.Println("エラー: likeHandlerでコンテキストからユーザーIDを取得できませんでした。")
		http.Error(w, "Could not retrieve user from context", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodPost:
		likeID := ulid.Make().String()
		_, err := db.Exec("INSERT INTO likes (like_id, user_id, post_id) VALUES (?, ?, ?)", likeID, userID, postID)
		if err != nil {
			log.Printf("エラー: db.Exec (insert like) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		log.Printf("いいね成功: user_id=%s, post_id=%s", userID, postID)

	case http.MethodDelete:
		_, err := db.Exec("DELETE FROM likes WHERE user_id = ? AND post_id = ?", userID, postID)
		if err != nil {
			log.Printf("エラー: db.Exec (delete like) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		log.Printf("いいね取り消し成功: user_id=%s, post_id=%s", userID, postID)

	default:
		http.Error(w, "許可されていないメソッドです", http.StatusMethodNotAllowed)
	}
}

func replyCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		log.Printf("メソッド不允许: /api/posts/reply/ に %s メソッドでアクセスがありました\n", r.Method)
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 5 {
		log.Println("エラー: リプライ先の投稿IDがパスに含まれていません。")
		http.Error(w, "親となる投稿IDがパスに含まれていません", http.StatusBadRequest)
		return
	}
	parentPostID := pathSegments[4]

	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		log.Println("エラー: コンテキストからユーザーIDを取得できませんでした。authMiddlewareが正しく適用されていない可能性があります。")
		http.Error(w, "ユーザーIDが取得できませんでした", http.StatusInternalServerError)
		return
	}
	
	var requestBody struct {
		Content  string `json:"content"`
		UserName string `json:"user_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		log.Printf("エラー: リクエストボディのデコードに失敗しました: %v\n", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	if strings.TrimSpace(requestBody.Content) == "" {
		log.Println("エラー: リプライ内容が空です。")
		http.Error(w, "リプライ内容が空です", http.StatusBadRequest)
		return
	}
	
	userName := requestBody.UserName
	if userName == "" {
		err := db.QueryRow("SELECT name FROM user WHERE firebase_uid = ?", userID).Scan(&userName)
		if err != nil {
			log.Printf("警告: DBからユーザー名の取得に失敗しました (firebase_uid: %s): %v。'名無しさん'を代用します。\n", userID, err)
			userName = "名無しさん"
		}
	}
	
	replyID := ulid.Make().String()

	_, err := db.Exec(
		"INSERT INTO posts (post_id, user_id, user_name, content, parent_post_id) VALUES (?, ?, ?, ?, ?)",
		replyID,
		userID,
		userName,
		requestBody.Content,
		parentPostID,
	)
	if err != nil {
		log.Printf("エラー: db.Exec (insert reply) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"reply_id": replyID})
	log.Printf("リプライ作成成功: reply_id=%s, parent_id=%s\n", replyID, parentPostID)
}

func repliesGetHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
        return
    }

    pathSegments := strings.Split(r.URL.Path, "/")
    if len(pathSegments) < 5 {
        http.Error(w, "親となる投稿IDがパスに含まれていません", http.StatusBadRequest)
        return
    }
    parentPostID := pathSegments[4]
    currentUserID := ""
	if userID, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = userID
	}

    query := `
        SELECT
            p.post_id, p.user_id, p.user_name, p.content, p.image_url, p.created_at,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count
        FROM posts p
        WHERE p.parent_post_id = ?
        ORDER BY p.created_at ASC
    `

    rows, err := db.Query(query, currentUserID, parentPostID)
    if err != nil {
        log.Printf("エラー: db.Query (replies) に失敗しました: %v", err)
        http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    replies := make([]Post, 0)
    for rows.Next() {
        var p Post
        if err := rows.Scan(&p.PostID, &p.UserID, &p.UserName, &p.Content, &p.ImageURL, &p.CreatedAt, &p.LikeCount, &p.IsLikedByMe, &p.ReplyCount); err != nil {
            log.Printf("エラー: rows.Scan (replies) に失敗しました: %v", err)
            http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
            return
        }
        replies = append(replies, p)
    }

    bytes, err := json.Marshal(replies)
    if err != nil {
        log.Printf("エラー: json.Marshal (replies) に失敗しました: %v", err)
        http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    w.Write(bytes)
    log.Printf("リプライ一覧の取得リクエスト成功: parent_id=%s", parentPostID)
}

func geminiSuggestReplyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	var requestBody struct {
		OriginalPostContent string `json:"original_post_content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if requestBody.OriginalPostContent == "" {
		http.Error(w, "元の投稿内容が空です", http.StatusBadRequest)
		return
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Println("エラー: GEMINI_API_KEY が設定されていません")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Printf("エラー: Geminiクライアントの作成に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer client.Close()

	model := client.GenerativeModel("gemini-1.5-flash")
	prompt := fmt.Sprintf("以下のSNS投稿に対して、ポジティブで、少し気の利いた短い返信を1つだけ生成してください。絵文字を少しだけ使って、フレンドリーな雰囲気でお願いします。返信は日本語で、返信文だけを出力してください。\n\n投稿:「%s」", requestBody.OriginalPostContent)

	log.Printf("Geminiに送信するプロンプト: %s\n", prompt)

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		log.Printf("エラー: Gemini APIからのコンテンツ生成に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		suggestion := resp.Candidates[0].Content.Parts[0]
		log.Printf("Geminiからの返信提案: %s\n", suggestion)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"suggestion": suggestion})
	} else {
		log.Println("エラー: Geminiから返信候補が生成されませんでした。")
		http.Error(w, "返信を生成できませんでした", http.StatusInternalServerError)
	}
}

func userPostsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}
	pathWithoutSuffix := strings.TrimSuffix(r.URL.Path, "/posts")
	pathSegments := strings.Split(pathWithoutSuffix, "/")
	if len(pathSegments) < 4 {
		http.Error(w, "ユーザーIDが指定されていません", http.StatusBadRequest)
		return
	}
	userID := pathSegments[3]

	log.Printf("特定ユーザーの投稿検索を開始: user_id=%s\n", userID)

	currentUserID := ""
	if id, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = id
	}

	query := `
        SELECT
            p.post_id, p.user_id, p.user_name, p.content, p.image_url, p.created_at,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count
        FROM posts p
        WHERE p.user_id = ? AND p.parent_post_id IS NULL
        ORDER BY p.created_at DESC
    `
	rows, err := db.Query(query, currentUserID, userID)
	if err != nil {
		log.Printf("エラー: db.Query (user posts) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.PostID, &p.UserID, &p.UserName, &p.Content, &p.ImageURL, &p.CreatedAt, &p.LikeCount, &p.IsLikedByMe, &p.ReplyCount); err != nil {
			log.Printf("エラー: rows.Scan (user posts) に失敗: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		posts = append(posts, p)
	}

	bytes, err := json.Marshal(posts)
	if err != nil {
		log.Printf("エラー: json.Marshal (user posts) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(bytes)
	log.Printf("特定ユーザーの投稿取得リクエスト成功: user_id=%s\n", userID)
}


func imageUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		log.Printf("画像の取得に失敗: %v", err)
		http.Error(w, "画像の取得に失敗しました", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ctx := context.Background()
	bucketName := os.Getenv("GCS_BUCKET_NAME")
	if bucketName == "" {
		log.Println("環境変数 GCS_BUCKET_NAME が設定されていません")
		http.Error(w, "サーバー設定エラー", http.StatusInternalServerError)
		return
	}

	client, err := storage.NewClient(ctx)
	if err != nil {
		log.Printf("GCSクライアントの作成に失敗: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}
	defer client.Close()

	objectName := ulid.Make().String() + ".png"
	
	writer := client.Bucket(bucketName).Object(objectName).NewWriter(ctx)

	if _, err := io.Copy(writer, file); err != nil {
		log.Printf("GCSへのファイルコピーに失敗: %v", err)
		http.Error(w, "アップロードに失敗しました", http.StatusInternalServerError)
		return
	}
	if err := writer.Close(); err != nil {
		log.Printf("GCS writerのクローズに失敗: %v", err)
		http.Error(w, "アップロード後の処理に失敗しました", http.StatusInternalServerError)
		return
	}

	publicURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucketName, objectName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"imageUrl": publicURL})
	log.Printf("画像アップロード成功: %s", publicURL)
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header must be provided", http.StatusUnauthorized)
			return
		}
		idToken := strings.TrimPrefix(authHeader, "Bearer ")
		if idToken == authHeader {
			http.Error(w, "Authorization header must be Bearer token", http.StatusUnauthorized)
			return
		}
		token, err := firebaseAuth.VerifyIDToken(context.Background(), idToken)
		if err != nil {
			log.Printf("error verifying ID token: %v\n", err)
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, token.UID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func authOptionalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		idToken := strings.TrimPrefix(authHeader, "Bearer ")
		if idToken != authHeader {
			token, err := firebaseAuth.VerifyIDToken(context.Background(), idToken)
			if err == nil {
				ctx := context.WithValue(r.Context(), userIDKey, token.UID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// getUserProfileHandlerは特定のユーザーの完全なプロフィール情報を取得します。
func getUserProfileHandler(w http.ResponseWriter, r *http.Request) {
	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 4 {
		http.Error(w, "ユーザーIDが指定されていません", http.StatusBadRequest)
		return
	}
	firebaseUID := pathSegments[3]

	var u UserResForHTTPGet
	var age sql.NullInt64
	var firebaseUIDFromDB, bio, profileImageURL, headerImageURL sql.NullString

	err := db.QueryRow("SELECT id, name, age, firebase_uid, bio, profile_image_url, header_image_url FROM user WHERE firebase_uid = ?", firebaseUID).Scan(
		&u.Id,
		&u.Name,
		&age,
		&firebaseUIDFromDB,
		&bio,
		&profileImageURL,
		&headerImageURL,
	)

	if err == sql.ErrNoRows {
		http.Error(w, "ユーザーが見つかりません", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("ユーザープロフィールの取得エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	if age.Valid {
		ageInt := int(age.Int64)
		u.Age = &ageInt
	}
	if firebaseUIDFromDB.Valid {
		u.FirebaseUID = &firebaseUIDFromDB.String
	}
	if bio.Valid {
		u.Bio = &bio.String
	}
	if profileImageURL.Valid {
		u.ProfileImageURL = &profileImageURL.String
	}
	if headerImageURL.Valid {
		u.HeaderImageURL = &headerImageURL.String
	}

	// ★★★ ここからが新しいデバッグログ ★★★
	// フロントエンドに送信する直前のデータ内容をログに出力します。
	log.Printf("--- Backend Response Debug ---")
	log.Printf("Sending User Profile Data: %+v", u)
	if u.FirebaseUID != nil {
		log.Printf("FirebaseUID to be sent: %s", *u.FirebaseUID)
	} else {
		log.Printf("FirebaseUID to be sent is nil.")
	}
	// ★★★ ここまで ★★★

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(u)
}



// updateUserProfileHandlerはユーザーのプロフィール情報（名前、bio、画像URL）を更新します。
func updateUserProfileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "PUTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	var req struct {
		Name            string `json:"name"`
		Bio             string `json:"bio"`
		ProfileImageURL string `json:"profile_image_url"`
		HeaderImageURL  string `json:"header_image_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "無効なリクエストボディです", http.StatusBadRequest)
		return
	}
	_, err := db.Exec("UPDATE user SET name = ?, bio = ?, profile_image_url = ?, header_image_url = ? WHERE firebase_uid = ?",
		req.Name, req.Bio, req.ProfileImageURL, req.HeaderImageURL, userID)
	if err != nil {
		log.Printf("プロフィール更新エラー: %v", err)
		http.Error(w, "プロフィールの更新に失敗しました", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "プロフィールを更新しました。"})
}

// userRouterHandlerは /api/users/ へのリクエストをさらに振り分けます。
func userRouterHandler(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/posts") {
		userPostsHandler(w, r)
		return
	}
	getUserProfileHandler(w, r)
}


// main関数のmux設定部分をこの内容に置き換えてください
func main() {
    log.Println("main 関数を開始します...")
    mux := http.NewServeMux()

	// --- 認証がオプショナルなエンドポイント ---
	mux.Handle("/posts", authOptionalMiddleware(http.HandlerFunc(postsGetHandler)))
	mux.Handle("/api/posts/replies/", authOptionalMiddleware(http.HandlerFunc(repliesGetHandler)))
	mux.Handle("/api/users/", authOptionalMiddleware(http.HandlerFunc(userRouterHandler))) // ★ ユーザー関連はここで一括処理

	// --- 認証が必須なエンドポイント ---
	mux.Handle("/post", authMiddleware(http.HandlerFunc(postCreateHandler)))
	mux.Handle("/api/post/image", authMiddleware(http.HandlerFunc(imageUploadHandler)))
	mux.Handle("/api/posts/like/", authMiddleware(http.HandlerFunc(likeHandler)))
	mux.Handle("/api/posts/reply/", authMiddleware(http.HandlerFunc(replyCreateHandler)))
	mux.Handle("/api/posts/delete/", authMiddleware(http.HandlerFunc(postDeleteHandler)))
	mux.Handle("/api/posts/suggest-reply", authMiddleware(http.HandlerFunc(geminiSuggestReplyHandler)))
	mux.Handle("/api/profile", authMiddleware(http.HandlerFunc(updateUserProfileHandler))) // ★ プロフィール更新用

	// --- 新しいログイン同期エンドポイント ---
	mux.Handle("/api/login", http.HandlerFunc(loginHandler))

	// --- 古い/userエンドポイント（互換性のために残す） ---
	mux.HandleFunc("/user", handler)

    c := cors.New(cors.Options{
        AllowedOrigins: []string{
            "http://localhost:3000",
            "http://localhost:5173",
            "https://hackathon-frontend-ver.vercel.app",
            "https://hackathon-frontend-ver-git-main-komeiall14s-projects.vercel.app",
            "https://hackathon-frontend-a0lipvgmk-komeiall14s-projects.vercel.app",
            "https://hackathon-frontend-1pis2eltq-komeiall14s-projects.vercel.app",
        },
        AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowedHeaders:   []string{"Content-Type", "Authorization"},
        AllowCredentials: true,
    })
    handlerWithCORS := c.Handler(mux)

    closeDBWithSysCall()

    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }
    log.Printf("HTTPサーバーをポート %s で起動します...\n", port)
    if err := http.ListenAndServe(":"+port, handlerWithCORS); err != nil {
        log.Fatalf("致命的エラー: ListenAndServe に失敗しました: %v", err)
    }
}
func closeDBWithSysCall() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sig
		log.Printf("システムコールを受信しました: %v。シャットダウン処理を開始します。\n", s)

		if db != nil { 
			log.Println("データベース接続をクローズします...")
			if err := db.Close(); err != nil {
				log.Printf("致命的エラー: db.Close に失敗しました。エラー: %v\n", err)
			}
			log.Println("✅ データベース接続を正常にクローズしました。")
		} else {
			log.Println("データベース接続(db)がnilのため、クローズ処理はスキップされました。")
		}
		log.Println("アプリケーションを終了します。")
		os.Exit(0) 
	}()
}
