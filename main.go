package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"regexp"
    "sort"
	"cloud.google.com/go/storage"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	"github.com/oklog/ulid/v2"
	"github.com/rs/cors"
	"google.golang.org/api/option"
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
	FollowingCount   int     `json:"following_count"`   // ▼▼▼ この行を追加
    FollowerCount    int     `json:"follower_count"`    // ▼▼▼ この行を追加
    IsFollowing      bool    `json:"is_following"`      // ▼▼▼ この行を追加
    IsMe             bool    `json:"is_me"` 
	
}


type Post struct {
	PostID              string  `json:"post_id"`
	UserID              string  `json:"user_id"`
	UserName            string  `json:"user_name"`
	UserProfileImageURL *string `json:"user_profile_image_url"` // ポインタ型にする
	Content             *string `json:"content"`               // ★ stringから*stringに変更
	ImageURL            *string `json:"image_url"`
	CreatedAt           string  `json:"created_at"`
	LikeCount           int     `json:"like_count"`
	IsLikedByMe         bool    `json:"is_liked_by_me"`
	ReplyCount          int     `json:"reply_count"`
	RetweetCount        int     `json:"retweet_count"`
	IsRetweetedByMe     bool    `json:"is_retweeted_by_me"`
	OriginalPost        *Post   `json:"original_post,omitempty"`
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
	log.Printf("Firebase UID 受信: %s", firebaseUID)  // ←★ここを追加

	// name/picture を安全に取得
	var userName, profileImageURL string
	if nameClaim, ok := token.Claims["name"].(string); ok {
		userName = nameClaim
	} else {
		log.Println("警告: name クレームがありません。")
		userName = "名無し"
	}
	if pictureClaim, ok := token.Claims["picture"].(string); ok {
		profileImageURL = pictureClaim
	} else {
		log.Println("警告: picture クレームがありません。")
		profileImageURL = ""
	}

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
		log.Printf("新規ユーザーとして作成を試みます: firebase_uid=%s", firebaseUID)  // ←★ここもデバッグに役立ちます
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
// main.go
// postGetHandlerは特定の1件の投稿を取得します。
func postGetHandler(w http.ResponseWriter, r *http.Request) {
	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 4 || pathSegments[3] == "" {
		http.Error(w, "投稿IDが指定されていません", http.StatusBadRequest)
		return
	}
	postID := pathSegments[3]

	currentUserID := ""
	if userID, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = userID
	}

	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.created_at, p.original_post_id,
            COALESCE(u.name, p.user_name) AS user_name,
            u.profile_image_url,
            orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me
        FROM
            posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
        WHERE p.post_id = ?
    `

	// ▼▼▼ 3つの?に、それぞれ対応する変数を渡します ▼▼▼
	rows, err := db.Query(query, currentUserID, currentUserID, postID)
	if err != nil {
		log.Printf("エラー: db.Query (single post) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	if rows.Next() {
		var p Post
		var content, imageURL, originalPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime

		// ▼▼▼ Scanの最後に2つのフィールドを追加します ▼▼▼
		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &p.CreatedAt, &originalPostID,
			&p.UserName, &userProfileImageURL,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
			&origUserName, &origUserProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe, &p.ReplyCount,
			&p.RetweetCount, &p.IsRetweetedByMe,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (single post) に失敗: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if userProfileImageURL.Valid { p.UserProfileImageURL = &userProfileImageURL.String }

		if originalPostID.Valid {
			var originalPost Post
			originalPost.PostID = origPostID.String
			originalPost.UserID = origUserID.String
			if origContent.Valid { originalPost.Content = &origContent.String }
			if origImageURL.Valid { originalPost.ImageURL = &origImageURL.String }
			if origCreatedAt.Valid { 
				originalPost.CreatedAt = origCreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
				// ▼▼▼ このデバッグ用ログを一行追加してください ▼▼▼
				log.Printf("DEBUG: Original Post Date. Raw: %v, Formatted: %s", origCreatedAt.Time, originalPost.CreatedAt)
			}
			if origUserName.Valid { originalPost.UserName = origUserName.String }
			if origUserProfileImageURL.Valid { originalPost.UserProfileImageURL = &origUserProfileImageURL.String }
			p.OriginalPost = &originalPost
		}
		
		bytes, err := json.Marshal(p)
		if err != nil {
			log.Printf("エラー: json.Marshal (single post) に失敗: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(bytes)

	} else {
		http.Error(w, "投稿が見つかりません", http.StatusNotFound)
	}
}

func postsGetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// --- ▼▼▼ ここからが修正・追加箇所 ▼▼▼ ---

	// クエリパラメータからlimitとoffsetを取得
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	// デフォルト値を設定
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20 // デフォルトは20件
	}

	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0 // デフォルトは0
	}

	log.Printf("投稿一覧取得リクエスト受信: limit=%d, offset=%d\n", limit, offset)

	currentUserID := ""
	if userID, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = userID
	}
	
	// SQLクエリに LIMIT と OFFSET を追加
	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.created_at, p.original_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me
        FROM
            posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
        WHERE
            p.parent_post_id IS NULL
        ORDER BY
            p.created_at DESC
        LIMIT ? OFFSET ?  -- この行を追加
    `

	// db.Queryにlimitとoffsetを渡す
	rows, err := db.Query(query, currentUserID, currentUserID, limit, offset)

	// --- ▲▲▲ ここまでが修正・追加箇所 ▲▲▲ ---

	if err != nil {
		log.Printf("エラー: db.Query (all posts) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// ...これ以降のScanロジックは変更なし...
	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, originalPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &p.CreatedAt, &originalPostID,
			&p.UserName, &userProfileImageURL,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
			&origUserName, &origUserProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe, &p.ReplyCount,
			&p.RetweetCount, &p.IsRetweetedByMe,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (all posts) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if userProfileImageURL.Valid { p.UserProfileImageURL = &userProfileImageURL.String }

		if originalPostID.Valid {
			var originalPost Post
			originalPost.PostID = origPostID.String
			originalPost.UserID = origUserID.String
			if origContent.Valid { originalPost.Content = &origContent.String }
			if origImageURL.Valid { originalPost.ImageURL = &origImageURL.String }
			if origCreatedAt.Valid { 
				originalPost.CreatedAt = origCreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
				log.Printf("DEBUG: Original Post Date. Raw: %v, Formatted: %s", origCreatedAt.Time, originalPost.CreatedAt)
			}
			if origUserName.Valid { originalPost.UserName = origUserName.String }
			if origUserProfileImageURL.Valid { originalPost.UserProfileImageURL = &origUserProfileImageURL.String }
			p.OriginalPost = &originalPost
		}

		posts = append(posts, p)
	}
	
	bytes, err := json.Marshal(posts)
	if err != nil {
		log.Printf("エラー: json.Marshal (all posts) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(bytes)
}

// postCreateHandlerは新しい投稿を作成します。
// postCreateHandlerを、この内容に丸ごと置き換えてください

func postCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	var requestBody struct {
		Content        string `json:"content"`
		ImageURL       string `json:"image_url,omitempty"`
		OriginalPostID string `json:"original_post_id,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if requestBody.Content == "" && requestBody.ImageURL == "" && requestBody.OriginalPostID == "" {
		http.Error(w, "投稿内容が空です", http.StatusBadRequest)
		return
	}

	var userName string
	err := db.QueryRow("SELECT name FROM user WHERE firebase_uid = ?", userID).Scan(&userName)
	if err != nil {
		userName = "名無しさん"
	}

	var imageUrlToSave, originalPostIdToSave sql.NullString
	if requestBody.ImageURL != "" {
		imageUrlToSave.String = requestBody.ImageURL
		imageUrlToSave.Valid = true
	}
	if requestBody.OriginalPostID != "" {
		originalPostIdToSave.String = requestBody.OriginalPostID
		originalPostIdToSave.Valid = true
	}

	postID := ulid.Make().String()

	_, err = db.Exec(
		"INSERT INTO posts (post_id, user_id, user_name, content, image_url, original_post_id) VALUES (?, ?, ?, ?, ?, ?)",
		postID, userID, userName, requestBody.Content, imageUrlToSave, originalPostIdToSave,
	)
	if err != nil {
		log.Printf("エラー: postsテーブルへのINSERTに失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// ▼▼▼ 作成した投稿の完全なデータを取得して返すロジック ▼▼▼
	var createdPost Post
	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.created_at, p.original_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
			(SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
			EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me
        FROM posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
        WHERE p.post_id = ?
    `
	row := db.QueryRow(query, userID, userID, postID)

	var content, imageURL, resOriginalPostID, userProfileImageURL sql.NullString
	var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
	var origCreatedAt sql.NullTime

	err = row.Scan(
		&createdPost.PostID, &createdPost.UserID, &content, &imageURL, &createdPost.CreatedAt, &resOriginalPostID,
		&createdPost.UserName, &userProfileImageURL,
		&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
		&origUserName, &origUserProfileImageURL,
		&createdPost.LikeCount, &createdPost.IsLikedByMe, &createdPost.ReplyCount,
		&createdPost.RetweetCount, &createdPost.IsRetweetedByMe,
	)
	if err != nil {
		log.Printf("作成された投稿の再取得に失敗: %v。post_idのみ返します。", err)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"post_id": postID})
		return
	}

	if content.Valid { createdPost.Content = &content.String }
	if imageURL.Valid { createdPost.ImageURL = &imageURL.String }
	if userProfileImageURL.Valid { createdPost.UserProfileImageURL = &userProfileImageURL.String }
	if resOriginalPostID.Valid {
		var originalPostData Post
		originalPostData.PostID = origPostID.String
		originalPostData.UserID = origUserID.String
		if origContent.Valid { originalPostData.Content = &origContent.String }
		if origImageURL.Valid { originalPostData.ImageURL = &origImageURL.String }
		if origCreatedAt.Valid { originalPostData.CreatedAt = origCreatedAt.Time.Format("2006-01-02T15:04:05Z07:00") }
		if origUserName.Valid { originalPostData.UserName = origUserName.String }
		if origUserProfileImageURL.Valid { originalPostData.UserProfileImageURL = &origUserProfileImageURL.String }
		createdPost.OriginalPost = &originalPostData
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(createdPost)
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

// main.go

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

	// ★ 修正点: SQLクエリをLEFT JOINを使ったものに変更
    query := `
        SELECT
            p.post_id, p.user_id, COALESCE(u.name, p.user_name), u.profile_image_url, p.content, p.image_url, p.created_at,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count
        FROM posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
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
		// ★ 修正点: Scanの対象に &p.UserProfileImageURL を追加
        if err := rows.Scan(&p.PostID, &p.UserID, &p.UserName, &p.UserProfileImageURL, &p.Content, &p.ImageURL, &p.CreatedAt, &p.LikeCount, &p.IsLikedByMe, &p.ReplyCount); err != nil {
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

// main.go

// userPostsHandlerを、この内容に丸ごと置き換えてください

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
            p.post_id, p.user_id, p.content, p.image_url, p.created_at, p.original_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me
        FROM posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
        WHERE p.user_id = ? -- ★★★ ここの条件を変更し、リツイートも取得対象に含めます ★★★
        ORDER BY p.created_at DESC
    `
	rows, err := db.Query(query, currentUserID, currentUserID, userID)
	if err != nil {
		log.Printf("エラー: db.Query (user posts) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, originalPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &p.CreatedAt, &originalPostID,
			&p.UserName, &userProfileImageURL,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
			&origUserName, &origUserProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe, &p.ReplyCount,
			&p.RetweetCount, &p.IsRetweetedByMe,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (user posts) に失敗: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if userProfileImageURL.Valid { p.UserProfileImageURL = &userProfileImageURL.String }

		if originalPostID.Valid {
			var originalPost Post
			originalPost.PostID = origPostID.String
			originalPost.UserID = origUserID.String
			if origContent.Valid { originalPost.Content = &origContent.String }
			if origImageURL.Valid { originalPost.ImageURL = &origImageURL.String }
			if origCreatedAt.Valid { 
				originalPost.CreatedAt = origCreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
				// ▼▼▼ このデバッグ用ログを一行追加してください ▼▼▼
				log.Printf("DEBUG: Original Post Date. Raw: %v, Formatted: %s", origCreatedAt.Time, originalPost.CreatedAt)
			}
			if origUserName.Valid { originalPost.UserName = origUserName.String }
			if origUserProfileImageURL.Valid { originalPost.UserProfileImageURL = &origUserProfileImageURL.String }
			p.OriginalPost = &originalPost
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

// main.go の getUserProfileHandler 関数をこれで置き換えてください

// main.go の getUserProfileHandler 関数をこれで置き換えてください
func getUserProfileHandler(w http.ResponseWriter, r *http.Request) {
    pathSegments := strings.Split(r.URL.Path, "/")
    if len(pathSegments) < 4 {
        http.Error(w, "ユーザーIDが指定されていません", http.StatusBadRequest)
        return
    }
    profileUserID := pathSegments[3] // プロフィールページのユーザーID

    // 現在ログインしているユーザーのIDを取得（ログインしていない場合は空文字）
    currentUserID := ""
    if userID, ok := r.Context().Value(userIDKey).(string); ok {
        currentUserID = userID
    }

    var u UserResForHTTPGet
    var age sql.NullInt64
    var firebaseUIDFromDB, bio, profileImageURL, headerImageURL sql.NullString

    // SQLクエリに、フォロー数・フォロワー数・フォロー状態の取得ロジックを追加
    query := `
        SELECT
            id, name, age, firebase_uid, bio, profile_image_url, header_image_url,
            (SELECT COUNT(*) FROM follows WHERE follower_id = user.firebase_uid) AS following_count,
            (SELECT COUNT(*) FROM follows WHERE following_id = user.firebase_uid) AS follower_count,
            EXISTS(SELECT 1 FROM follows WHERE follower_id = ? AND following_id = user.firebase_uid) AS is_following
        FROM user
        WHERE firebase_uid = ?
    `
    // 最初の?にログインユーザーID、2番目の?にプロフィールユーザーIDを渡す
    err := db.QueryRow(query, currentUserID, profileUserID).Scan(
        &u.Id, &u.Name, &age, &firebaseUIDFromDB, &bio, &profileImageURL, &headerImageURL,
        &u.FollowingCount, &u.FollowerCount, &u.IsFollowing,
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

    // 取得した値を構造体にマッピング
    if age.Valid { ageInt := int(age.Int64); u.Age = &ageInt }
    if firebaseUIDFromDB.Valid { u.FirebaseUID = &firebaseUIDFromDB.String }
    if bio.Valid { u.Bio = &bio.String }
    if profileImageURL.Valid { u.ProfileImageURL = &profileImageURL.String }
    if headerImageURL.Valid { u.HeaderImageURL = &headerImageURL.String }
    
    // 自分自身のプロフィールかどうかを判定
    u.IsMe = currentUserID != "" && currentUserID == profileUserID

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

// main.go の userRouterHandler 関数をこれで置き換えてください

// userRouterHandlerは /api/users/ へのリクエストをURLの末尾によってさらに振り分けます。
// main.go の userRouterHandler 関数をこれで置き換えてください

func userRouterHandler(w http.ResponseWriter, r *http.Request) {
	// 末尾が /follow の場合
	if strings.HasSuffix(r.URL.Path, "/follow") {
		followHandler(w, r)
		return
	}
	// 末尾が /following の場合
	if strings.HasSuffix(r.URL.Path, "/following") {
		followingListHandler(w, r)
		return
	}
	// 末尾が /followers の場合
	if strings.HasSuffix(r.URL.Path, "/followers") {
		followerListHandler(w, r)
		return
	}
	// 末尾が /posts の場合
	if strings.HasSuffix(r.URL.Path, "/posts") {
		userPostsHandler(w, r)
		return
	}
	// それ以外の場合は、ユーザープロフィール取得として処理
	getUserProfileHandler(w, r)
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// URLクエリから検索キーワード'q'を取得
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "検索キーワードが指定されていません", http.StatusBadRequest)
		return
	}

	log.Printf("検索リクエスト受信: キーワード=%s", query)
	searchTerm := "%" + query + "%" // LIKE検索用のワイルドカードを追加

	currentUserID := ""
	if userID, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = userID
	}

	// posts.content または user.name がキーワードに一致する投稿を検索するSQL
	sqlQuery := `
        SELECT
            p.post_id, p.user_id, COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url, p.content, p.image_url, p.created_at,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count
        FROM
            posts p
        LEFT JOIN
            user u ON p.user_id = u.firebase_uid
        WHERE
            p.parent_post_id IS NULL AND (p.content LIKE ? OR u.name LIKE ?)
        ORDER BY
            p.created_at DESC
    `

	rows, err := db.Query(sqlQuery, currentUserID, searchTerm, searchTerm)
	if err != nil {
		log.Printf("エラー: db.Query (search) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.PostID, &p.UserID, &p.UserName, &p.UserProfileImageURL, &p.Content, &p.ImageURL, &p.CreatedAt, &p.LikeCount, &p.IsLikedByMe, &p.ReplyCount); err != nil {
			log.Printf("エラー: rows.Scan (search) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		posts = append(posts, p)
	}

	bytes, err := json.Marshal(posts)
	if err != nil {
		log.Printf("エラー: json.Marshal (search) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(bytes)
}

// retweetHandlerはリツイートを作成します。
// retweetHandlerを、この内容に丸ごと置き換えてください

func retweetHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 4 {
		http.Error(w, "リツイート対象の投稿IDが指定されていません", http.StatusBadRequest)
		return
	}
	originalPostID := pathSegments[3]

	switch r.Method {
	case http.MethodPost:
		var userName string
		err := db.QueryRow("SELECT name FROM user WHERE firebase_uid = ?", userID).Scan(&userName)
		if err != nil {
			log.Printf("リツイートユーザーの名前取得に失敗: %v", err)
			userName = "名無しさん"
		}

		newPostID := ulid.Make().String()
		_, err = db.Exec(
			"INSERT INTO posts (post_id, user_id, user_name, original_post_id) VALUES (?, ?, ?, ?)",
			newPostID, userID, userName, originalPostID,
		)
		if err != nil {
			log.Printf("リツイートの作成に失敗: %v", err)
			http.Error(w, "リツイートに失敗しました", http.StatusInternalServerError)
			return
		}

		// ▼▼▼ 作成したリツイート投稿の完全なデータを取得して返すロジック ▼▼▼
		var newRetweet Post
		query := `
			SELECT
				p.post_id, p.user_id, p.content, p.image_url, p.created_at, p.original_post_id,
				COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
				orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
				orig_u.name, orig_u.profile_image_url,
				(SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
				EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
				(SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
				(SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
				EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me
			FROM posts p
			LEFT JOIN user u ON p.user_id = u.firebase_uid
			LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
			LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
			WHERE p.post_id = ?
		`
		row := db.QueryRow(query, userID, userID, newPostID)

		var content, imageURL, resOriginalPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime

		err = row.Scan(
			&newRetweet.PostID, &newRetweet.UserID, &content, &imageURL, &newRetweet.CreatedAt, &resOriginalPostID,
			&newRetweet.UserName, &userProfileImageURL,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
			&origUserName, &origUserProfileImageURL,
			&newRetweet.LikeCount, &newRetweet.IsLikedByMe, &newRetweet.ReplyCount,
			&newRetweet.RetweetCount, &newRetweet.IsRetweetedByMe,
		)
		if err != nil {
			log.Printf("作成されたリツイートの取得に失敗: %v", err)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"message": "リツイートしました"})
			return
		}

		if content.Valid { newRetweet.Content = &content.String }
		if imageURL.Valid { newRetweet.ImageURL = &imageURL.String }
		if userProfileImageURL.Valid { newRetweet.UserProfileImageURL = &userProfileImageURL.String }
		if resOriginalPostID.Valid {
			var originalPostData Post
			originalPostData.PostID = origPostID.String
			originalPostData.UserID = origUserID.String
			if origContent.Valid { originalPostData.Content = &origContent.String }
			if origImageURL.Valid { originalPostData.ImageURL = &origImageURL.String }
			if origCreatedAt.Valid { originalPostData.CreatedAt = origCreatedAt.Time.Format("2006-01-02T15:04:05Z07:00") }
			if origUserName.Valid { originalPostData.UserName = origUserName.String }
			if origUserProfileImageURL.Valid { originalPostData.UserProfileImageURL = &origUserProfileImageURL.String }
			newRetweet.OriginalPost = &originalPostData
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(newRetweet)

	case http.MethodDelete:
		result, err := db.Exec(
			"DELETE FROM posts WHERE original_post_id = ? AND user_id = ? AND content IS NULL",
			originalPostID, userID,
		)
		if err != nil {
			log.Printf("リツイートの取り消しに失敗: %v", err)
			http.Error(w, "リツイートの取り消しに失敗しました", http.StatusInternalServerError)
			return
		}
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			log.Printf("取り消し対象のリツイートが見つかりません: original_post_id=%s, user_id=%s", originalPostID, userID)
		}
		w.WriteHeader(http.StatusNoContent)
		log.Printf("リツイート取り消し成功: user_id=%s, original_post_id=%s", userID, originalPostID)
	default:
		http.Error(w, "許可されていないメソッドです", http.StatusMethodNotAllowed)
	}
}

// main.go にこの関数を追加

// quoteRetweetsGetHandler は、特定の投稿への引用リツイートを一覧で取得します。
func quoteRetweetsGetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	pathSegments := strings.Split(r.URL.Path, "/")
	// /api/posts/{id}/quote_retweets のようなパスを想定
	if len(pathSegments) < 5 {
		http.Error(w, "投稿IDが指定されていません", http.StatusBadRequest)
		return
	}
	originalPostID := pathSegments[4]

	currentUserID := ""
	if userID, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = userID
	}

	// original_post_id を持ち、かつ content が空でない投稿を取得するクエリ
	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.created_at, p.original_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me
        FROM posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
        WHERE p.original_post_id = ? AND p.content IS NOT NULL
        ORDER BY p.created_at DESC
    `

	rows, err := db.Query(query, currentUserID, currentUserID, originalPostID)
	if err != nil {
		log.Printf("エラー: db.Query (quote retweets) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, resOriginalPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &p.CreatedAt, &resOriginalPostID,
			&p.UserName, &userProfileImageURL,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
			&origUserName, &origUserProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe, &p.ReplyCount,
			&p.RetweetCount, &p.IsRetweetedByMe,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (quote retweets) に失敗: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if userProfileImageURL.Valid { p.UserProfileImageURL = &userProfileImageURL.String }

		if resOriginalPostID.Valid {
			var originalPost Post
			originalPost.PostID = origPostID.String
			originalPost.UserID = origUserID.String
			if origContent.Valid { originalPost.Content = &origContent.String }
			if origImageURL.Valid { originalPost.ImageURL = &origImageURL.String }
			if origCreatedAt.Valid { originalPost.CreatedAt = origCreatedAt.Time.Format("2006-01-02T15:04:05Z07:00") }
			if origUserName.Valid { originalPost.UserName = origUserName.String }
			if origUserProfileImageURL.Valid { originalPost.UserProfileImageURL = &origUserProfileImageURL.String }
			p.OriginalPost = &originalPost
		}

		posts = append(posts, p)
	}

	bytes, err := json.Marshal(posts)
	if err != nil {
		log.Printf("エラー: json.Marshal (quote retweets) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(bytes)
}

// main.go にこの関数を追加

// followHandler は、ユーザーのフォロー・アンフォローを処理します。
func followHandler(w http.ResponseWriter, r *http.Request) {

	// ▼▼▼ この認証チェックを関数の冒頭に追加 ▼▼▼
	followerID, ok := r.Context().Value(userIDKey).(string)
	if !ok || followerID == "" {
		http.Error(w, "この操作には認証が必要です", http.StatusUnauthorized)
		return
	}
	// URLからフォロー対象のユーザーIDを取得
	// 例: /api/users/01JXXXX/follow
	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 5 {
		http.Error(w, "対象のユーザーIDが指定されていません", http.StatusBadRequest)
		return
	}
	followingID := pathSegments[3] // フォローされる側のID

	// 自分自身をフォローしようとした場合はエラー
	if followerID == followingID {
		http.Error(w, "自分自身をフォローすることはできません", http.StatusBadRequest)
		return
	}

	// HTTPメソッドによって処理を分岐
	switch r.Method {
	case http.MethodPost: // フォローする
		_, err := db.Exec("INSERT INTO follows (follower_id, following_id) VALUES (?, ?)", followerID, followingID)
		if err != nil {
			// 主キー制約違反（既にフォロー済み）の場合も考えられるが、ここでは汎用的なサーバーエラーとして処理
			log.Printf("エラー: フォロー処理に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated) // 201 Created ステータスを返す
		log.Printf("フォロー成功: follower=%s, following=%s", followerID, followingID)

	case http.MethodDelete: // アンフォローする
		_, err := db.Exec("DELETE FROM follows WHERE follower_id = ? AND following_id = ?", followerID, followingID)
		if err != nil {
			log.Printf("エラー: アンフォロー処理に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent) // 204 No Content ステータスを返す
		log.Printf("アンフォロー成功: follower=%s, following=%s", followerID, followingID)

	default:
		http.Error(w, "許可されていないメソッドです", http.StatusMethodNotAllowed)
	}
}

// main.go にこの2つの関数を追加

// followingListHandler は、指定されたユーザーがフォローしているユーザーの一覧を返します。
func followingListHandler(w http.ResponseWriter, r *http.Request) {
	profileUserID, currentUserID, ok := getUserIDsFromRequest(r)
	if !ok {
		http.Error(w, "ユーザーIDの取得に失敗しました", http.StatusBadRequest)
		return
	}

	query := `
		SELECT u.id, u.name, u.age, u.firebase_uid, u.bio, u.profile_image_url, u.header_image_url,
			(SELECT COUNT(*) FROM follows WHERE follower_id = u.firebase_uid) AS following_count,
			(SELECT COUNT(*) FROM follows WHERE following_id = u.firebase_uid) AS follower_count,
			EXISTS(SELECT 1 FROM follows WHERE follower_id = ? AND following_id = u.firebase_uid) AS is_following
		FROM user u
		INNER JOIN follows f ON u.firebase_uid = f.following_id
		WHERE f.follower_id = ?
	`
	rows, err := db.Query(query, currentUserID, profileUserID)
	if err != nil {
		log.Printf("フォロー中のユーザー一覧取得エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	users, err := scanUsers(rows)
	if err != nil {
		log.Printf("ユーザーデータの読み取りエラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}


// followerListHandler は、指定されたユーザーをフォローしているユーザーの一覧を返します。
func followerListHandler(w http.ResponseWriter, r *http.Request) {
	profileUserID, currentUserID, ok := getUserIDsFromRequest(r)
	if !ok {
		http.Error(w, "ユーザーIDの取得に失敗しました", http.StatusBadRequest)
		return
	}

	query := `
		SELECT u.id, u.name, u.age, u.firebase_uid, u.bio, u.profile_image_url, u.header_image_url,
			(SELECT COUNT(*) FROM follows WHERE follower_id = u.firebase_uid) AS following_count,
			(SELECT COUNT(*) FROM follows WHERE following_id = u.firebase_uid) AS follower_count,
			EXISTS(SELECT 1 FROM follows WHERE follower_id = ? AND following_id = u.firebase_uid) AS is_following
		FROM user u
		INNER JOIN follows f ON u.firebase_uid = f.follower_id
		WHERE f.following_id = ?
	`
	rows, err := db.Query(query, currentUserID, profileUserID)
	if err != nil {
		log.Printf("フォロワー一覧取得エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	users, err := scanUsers(rows)
	if err != nil {
		log.Printf("ユーザーデータの読み取りエラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}


// 共通ロジックをまとめたヘルパー関数（もしこのような関数がなければ、これも追加してください）
func getUserIDsFromRequest(r *http.Request) (profileUserID, currentUserID string, ok bool) {
	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 4 {
		return "", "", false
	}
	profileUserID = pathSegments[3]

	if userID, success := r.Context().Value(userIDKey).(string); success {
		currentUserID = userID
	}
	return profileUserID, currentUserID, true
}

func scanUsers(rows *sql.Rows) ([]UserResForHTTPGet, error) {
	users := make([]UserResForHTTPGet, 0)
	for rows.Next() {
		var u UserResForHTTPGet
		var age sql.NullInt64
		var firebaseUID, bio, profileImageURL, headerImageURL sql.NullString
		
		err := rows.Scan(
			&u.Id, &u.Name, &age, &firebaseUID, &bio, &profileImageURL, &headerImageURL,
			&u.FollowingCount, &u.FollowerCount, &u.IsFollowing,
		)
		if err != nil {
			return nil, err
		}

		if age.Valid { ageInt := int(age.Int64); u.Age = &ageInt }
		if firebaseUID.Valid { u.FirebaseUID = &firebaseUID.String }
		if bio.Valid { u.Bio = &bio.String }
		if profileImageURL.Valid { u.ProfileImageURL = &profileImageURL.String }
		if headerImageURL.Valid { u.HeaderImageURL = &headerImageURL.String }
		users = append(users, u)
	}
	return users, nil
}

// main.go にこの構造体と関数を追加

// Trend は、トレンドのトピックと投稿数を表します。
type Trend struct {
    Topic string `json:"topic"`
    Count int    `json:"count"`
}

// trendsHandler は、直近の投稿からトレンドを抽出して返します。
func trendsHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
        return
    }

    // 直近24時間の投稿を取得（期間は調整可能）
    rows, err := db.Query("SELECT content FROM posts WHERE created_at > NOW() - INTERVAL 24 HOUR AND content IS NOT NULL")
    if err != nil {
        log.Printf("トレンドデータ取得エラー: %v", err)
        http.Error(w, "サーバーエラー", http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    // ハッシュタグを抽出するための正規表現
    re := regexp.MustCompile(`#(\w+)`)
    hashtagCounts := make(map[string]int)

    for rows.Next() {
        var content string
        if err := rows.Scan(&content); err != nil {
            continue // スキャンに失敗した行はスキップ
        }
        
        // 1つの投稿から複数のハッシュタグを抽出
        matches := re.FindAllStringSubmatch(content, -1)
        for _, match := range matches {
            if len(match) > 1 {
                hashtag := strings.ToLower(match[1]) // 大文字小文字を区別しない
                hashtagCounts[hashtag]++
            }
        }
    }

    // 集計結果をスライスに変換
    trends := make([]Trend, 0, len(hashtagCounts))
    for topic, count := range hashtagCounts {
        trends = append(trends, Trend{Topic: topic, Count: count})
    }

    // 投稿数が多い順にソート
    sort.Slice(trends, func(i, j int) bool {
        return trends[i].Count > trends[j].Count
    })

    // 上位10件に制限
    limit := 10
    if len(trends) < limit {
        limit = len(trends)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(trends[:limit])
}

// main関数のmux設定部分をこの内容に置き換えてください
func main() {
    log.Println("main 関数を開始します...")
    mux := http.NewServeMux()

	// --- 認証がオプショナルなエンドポイント ---
	mux.Handle("/posts", authOptionalMiddleware(http.HandlerFunc(postsGetHandler)))
	mux.Handle("/api/post/", authOptionalMiddleware(http.HandlerFunc(postGetHandler)))
	mux.Handle("/api/posts/replies/", authOptionalMiddleware(http.HandlerFunc(repliesGetHandler)))
	mux.Handle("/api/posts/quote_retweets/", authOptionalMiddleware(http.HandlerFunc(quoteRetweetsGetHandler)))

	mux.Handle("/api/users/", authOptionalMiddleware(http.HandlerFunc(userRouterHandler))) // ★ ユーザー関連はここで一括処理
	mux.Handle("/api/search", authOptionalMiddleware(http.HandlerFunc(searchHandler))) // ★ この行を追加


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

	mux.Handle("/api/retweet/", authMiddleware(http.HandlerFunc(retweetHandler)))

	mux.HandleFunc("/api/trends", trendsHandler)
	


	// --- 古い/userエンドポイント（互換性のために残す） ---
	mux.HandleFunc("/user", handler)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Backend is running."))
	})


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


