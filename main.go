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
	"math/rand"
    "time"
	"cloud.google.com/go/storage"
	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	"github.com/oklog/ulid/v2"
	"github.com/rs/cors"
	"google.golang.org/api/option"
	"firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"golang.org/x/net/html"
	"github.com/gorilla/websocket"
	"errors"
)
type UserResForHTTPGet struct {
    Id               string  `json:"id"`
    Name             string  `json:"name"`
    Age              *int    `json:"age"`
    FirebaseUID      *string `json:"firebase_uid"`
    Bio              *string `json:"bio"`               
    ProfileImageURL  *string `json:"profile_image_url"` 
    HeaderImageURL   *string `json:"header_image_url"`  
	FollowingCount   int     `json:"following_count"`   
    FollowerCount    int     `json:"follower_count"`    
    IsFollowing      bool    `json:"is_following"`      
    IsMe             bool    `json:"is_me"` 
	
}


type Post struct {
	PostID              string  `json:"post_id"`
	UserID              string  `json:"user_id"`
	UserName            string  `json:"user_name"`
	UserProfileImageURL *string `json:"user_profile_image_url"`
	Content             *string `json:"content"`
	ImageURL            *string `json:"image_url"`
	VideoURL            *string `json:"video_url"`   
	MediaType           *string `json:"media_type"`  
	CreatedAt           string  `json:"created_at"`
	LikeCount           int     `json:"like_count"`
	IsLikedByMe         bool    `json:"is_liked_by_me"`
	ReplyCount          int     `json:"reply_count"`
	RetweetCount        int     `json:"retweet_count"`
	IsRetweetedByMe     bool    `json:"is_retweeted_by_me"`
	IsBookmarkedByMe    bool    `json:"is_bookmarked_by_me"`
	OriginalPost        *Post   `json:"original_post,omitempty"`
	ParentPost          *Post   `json:"parent_post,omitempty"`
	BadCount            int     `json:"bad_count"`          
    IsBaddedByMe        bool    `json:"is_badded_by_me"`  
}

type Message struct {
    ID            string `json:"id"`
    ConversationID string `json:"conversation_id"`
    SenderID      string `json:"sender_id"`
    Content       string `json:"content"`
    CreatedAt     string `json:"created_at"`
}

type Conversation struct {
    ConversationID      string             `json:"conversation_id"`
    OtherUser           UserResForHTTPGet  `json:"other_user"`
    LastMessage         *Message           `json:"last_message"`
    UpdatedAt           string             `json:"updated_at"`
}

type EvaluateExplanationRequest struct {
	OriginalPostID      string `json:"originalPostId"`
	ExplanationPostID   string `json:"explanationPostId"`
	OriginalContent     string `json:"originalContent"`
	ExplanationContent string `json:"explanationContent"`
}

type EvaluateExplanationResponse struct {
	Score  int    `json:"score"`
	Review string `json:"review"`
}

type NotificationResponse struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Actor     UserResForHTTPGet `json:"actor"` // アクションを起こしたユーザーの情報
	EntityID  *string   `json:"entity_id"`
	IsRead    bool      `json:"is_read"`
	CreatedAt string `json:"created_at"`
}

// OGP情報を格納する構造体
type OGPResponse struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	SiteURL     string `json:"site_url,omitempty"`
}

// ExperienceActionRequest は /api/bot/experience-action へのリクエストボディ
type ExperienceActionRequest struct {
	TargetPostID string `json:"targetPostId"`
	Type         string `json:"type"` // "positive" または "negative"
}

// BotUser はDBから取得するボットユーザーの情報を格納する
type BotUser struct {
	ID          string
	FirebaseUID string
	Name        string
}

type Like struct {
	ID     string
	UserID string
	PostID string
}
type Bad struct {
	ID     string
	UserID string
	PostID string
}

var db *sql.DB
var firebaseAuth *auth.Client

type contextKey string
const userIDKey contextKey = "userID"

func getNotificationsHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	query := `
		SELECT n.id, n.type, n.entity_id, n.is_read, n.created_at,
			   u.firebase_uid, u.name, u.profile_image_url
		FROM notifications n
		JOIN user u ON n.actor_id = u.firebase_uid
		WHERE n.recipient_id = ?
		ORDER BY n.created_at DESC
		LIMIT 50
	`
	rows, err := db.Query(query, userID)
	if err != nil {
		log.Printf("通知の取得エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	notifications := make([]NotificationResponse, 0)
	for rows.Next() {
		var n NotificationResponse
		var entityID sql.NullString
		var actorFirebaseUID, actorName, actorProfileImageURL sql.NullString

		err := rows.Scan(&n.ID, &n.Type, &entityID, &n.IsRead, &n.CreatedAt,
			&actorFirebaseUID, &actorName, &actorProfileImageURL)
		if err != nil {
			log.Printf("通知データの読み取りエラー: %v", err)
			continue
		}
		if entityID.Valid { n.EntityID = &entityID.String }
		if actorFirebaseUID.Valid { n.Actor.FirebaseUID = &actorFirebaseUID.String }
		if actorName.Valid { n.Actor.Name = actorName.String }
		if actorProfileImageURL.Valid { n.Actor.ProfileImageURL = &actorProfileImageURL.String }
		
		notifications = append(notifications, n)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(notifications)
}

func markNotificationsAsReadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみ許可", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	_, err := db.Exec("UPDATE notifications SET is_read = TRUE WHERE recipient_id = ?", userID)
	if err != nil {
		log.Printf("通知の既読化エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

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
			rows, err := db.Query("SELECT id, name, age, firebase_uid, profile_image_url FROM user WHERE name = ?", name)
			if err != nil {
				log.Printf("エラー: db.Query (name=%s) に失敗しました。エラー: %v\n", name, err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			defer rows.Close()

			users := make([]UserResForHTTPGet, 0)
			for rows.Next() {
				var u UserResForHTTPGet
				if err := rows.Scan(&u.Id, &u.Name, &u.Age, &u.FirebaseUID, &u.ProfileImageURL); err != nil {
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
		rows, err := db.Query("SELECT id, name, age, firebase_uid, profile_image_url FROM user")
		if err != nil {
			log.Printf("エラー: db.Query (all users) に失敗しました。エラー: %v\n", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		users := make([]UserResForHTTPGet, 0)
		for rows.Next() {
			var u UserResForHTTPGet
			if err := rows.Scan(&u.Id, &u.Name, &u.Age, &u.FirebaseUID, &u.ProfileImageURL); err != nil {
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
	log.Printf("Firebase UID 受信: %s", firebaseUID) 

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
		log.Printf("新規ユーザーとして作成を試みます: firebase_uid=%s", firebaseUID)  
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
            p.post_id, p.user_id, p.content, p.image_url, p.video_url, p.media_type, p.created_at, p.original_post_id, p.parent_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
			(SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
			EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me,
            
            orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,

            parent_p.post_id, parent_p.user_id, parent_p.content, parent_p.image_url, parent_p.created_at,
            parent_u.name, parent_u.profile_image_url
        FROM
            posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
        LEFT JOIN posts AS parent_p ON p.parent_post_id = parent_p.post_id
        LEFT JOIN user AS parent_u ON parent_p.user_id = parent_u.firebase_uid
        WHERE p.post_id = ?
    `
	
	row := db.QueryRow(query, currentUserID, currentUserID, currentUserID, currentUserID, postID)

	var p Post
	var content, imageURL, videoURL, mediaType, originalPostID, parentPostID, userProfileImageURL sql.NullString
	
	var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
	var origCreatedAt sql.NullTime

	var parentP_PostID, parentP_UserID, parentP_Content, parentP_ImageURL, parentU_Name, parentU_ProfileImageURL sql.NullString
	var parentP_CreatedAt sql.NullTime

	err := row.Scan(
		&p.PostID, &p.UserID, &content, &imageURL, &videoURL, &mediaType, &p.CreatedAt, &originalPostID, &parentPostID,
		&p.UserName, &userProfileImageURL,
		&p.LikeCount, &p.IsLikedByMe,
		&p.BadCount, &p.IsBaddedByMe,
		&p.ReplyCount,
		&p.RetweetCount, &p.IsRetweetedByMe,
		&p.IsBookmarkedByMe,
		&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
		&origUserName, &origUserProfileImageURL,
		&parentP_PostID, &parentP_UserID, &parentP_Content, &parentP_ImageURL, &parentP_CreatedAt,
		&parentU_Name, &parentU_ProfileImageURL,
	)
	
	if err == sql.ErrNoRows {
		http.Error(w, "投稿が見つかりません", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("エラー: rows.Scan (single post) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if content.Valid { p.Content = &content.String }
	if imageURL.Valid { p.ImageURL = &imageURL.String }
	if videoURL.Valid { p.VideoURL = &videoURL.String }
	if mediaType.Valid { p.MediaType = &mediaType.String }
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

	if parentPostID.Valid {
		var parentPost Post
		parentPost.PostID = parentP_PostID.String
		parentPost.UserID = parentP_UserID.String
		if parentP_Content.Valid { parentPost.Content = &parentP_Content.String }
		if parentP_ImageURL.Valid { parentPost.ImageURL = &parentP_ImageURL.String }
		if parentP_CreatedAt.Valid { parentPost.CreatedAt = parentP_CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00") }
		if parentU_Name.Valid { parentPost.UserName = parentU_Name.String }
		if parentU_ProfileImageURL.Valid { parentPost.UserProfileImageURL = &parentU_ProfileImageURL.String }
		p.ParentPost = &parentPost
	}
	
	bytes, err := json.Marshal(p)
	if err != nil {
		log.Printf("エラー: json.Marshal (single post) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(bytes)
}

func postsGetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

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
	
	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.video_url, p.media_type, p.created_at, p.original_post_id, p.parent_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
			(SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
			EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me,
			orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            parent_p.post_id, parent_p.user_id,
            parent_u.name
        FROM
            posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
		LEFT JOIN posts AS parent_p ON p.parent_post_id = parent_p.post_id
        LEFT JOIN user AS parent_u ON parent_p.user_id = parent_u.firebase_uid
		WHERE p.parent_post_id IS NULL --
        ORDER BY
            p.created_at DESC
        LIMIT ? OFFSET ?
    `

	// db.Queryにlimitとoffsetを渡す
	rows, err := db.Query(query, currentUserID, currentUserID, currentUserID, currentUserID, limit, offset)

	if err != nil {
		log.Printf("エラー: db.Query (all posts) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, videoURL, mediaType, originalPostID, parentPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime
		var parentP_PostID, parentP_UserID, parentU_Name sql.NullString

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &videoURL, &mediaType, &p.CreatedAt, &originalPostID, &parentPostID,
			&p.UserName, &userProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe,
			&p.BadCount, &p.IsBaddedByMe,
			&p.ReplyCount, &p.RetweetCount, &p.IsRetweetedByMe, &p.IsBookmarkedByMe,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt, &origUserName, &origUserProfileImageURL,
			&parentP_PostID, &parentP_UserID, &parentU_Name,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (all posts) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		
		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if videoURL.Valid { p.VideoURL = &videoURL.String }
		if mediaType.Valid { p.MediaType = &mediaType.String }
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

        if parentPostID.Valid {
            p.ParentPost = &Post{
                PostID:   parentP_PostID.String,
                UserID:   parentP_UserID.String,
                UserName: parentU_Name.String,
            }
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


func followingPostsGetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// 認証ミドルウェアでセットされたユーザーIDを取得
	currentUserID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	// ページネーションのためのlimitとoffsetを取得
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}

	// SQLクエリを修正して、フォロー中のユーザー(f.follower_id = ?)と自分自身(p.user_id = ?)の投稿を取得
	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.video_url, p.media_type, p.created_at, p.original_post_id, p.parent_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
			(SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
			EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me,
			orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            parent_p.post_id, parent_p.user_id,
            parent_u.name
        FROM
            posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN follows f ON p.user_id = f.following_id
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
		LEFT JOIN posts AS parent_p ON p.parent_post_id = parent_p.post_id
        LEFT JOIN user AS parent_u ON parent_p.user_id = parent_u.firebase_uid
        WHERE
            f.follower_id = ? OR p.user_id = ?
        GROUP BY p.post_id
        ORDER BY
            p.created_at DESC
        LIMIT ? OFFSET ?
    `

	rows, err := db.Query(query, currentUserID, currentUserID, currentUserID, currentUserID, currentUserID, currentUserID, limit, offset)
	if err != nil {
		log.Printf("エラー: db.Query (following posts) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, videoURL, mediaType, originalPostID, parentPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime
		var parentP_PostID, parentP_UserID, parentU_Name sql.NullString

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &videoURL, &mediaType, &p.CreatedAt, &originalPostID, &parentPostID,
			&p.UserName, &userProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe,
			&p.BadCount, &p.IsBaddedByMe,
			&p.ReplyCount, &p.RetweetCount, &p.IsRetweetedByMe, &p.IsBookmarkedByMe,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt, &origUserName, &origUserProfileImageURL,
			&parentP_PostID, &parentP_UserID, &parentU_Name,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (following posts) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		
		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if videoURL.Valid { p.VideoURL = &videoURL.String }
		if mediaType.Valid { p.MediaType = &mediaType.String }
		if userProfileImageURL.Valid { p.UserProfileImageURL = &userProfileImageURL.String }

		if originalPostID.Valid {
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

        if parentPostID.Valid {
            p.ParentPost = &Post{
                PostID:   parentP_PostID.String,
                UserID:   parentP_UserID.String,
                UserName: parentU_Name.String,
            }
        }

		posts = append(posts, p)
	}
	
	bytes, err := json.Marshal(posts)
	if err != nil {
		log.Printf("エラー: json.Marshal (following posts) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(bytes)
}


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
		VideoURL       string `json:"video_url,omitempty"`
		OriginalPostID string `json:"original_post_id,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if requestBody.Content == "" && requestBody.ImageURL == "" && requestBody.VideoURL == "" && requestBody.OriginalPostID == "" {
		http.Error(w, "投稿内容が空です", http.StatusBadRequest)
		return
	}

	var userName string
	err := db.QueryRow("SELECT name FROM user WHERE firebase_uid = ?", userID).Scan(&userName)
	if err != nil {
		userName = "名無しさん"
	}

	var imageUrlToSave, videoUrlToSave, mediaTypeToSave, originalPostIdToSave sql.NullString
	if requestBody.ImageURL != "" {
		imageUrlToSave.String = requestBody.ImageURL
		imageUrlToSave.Valid = true
		mediaTypeToSave.String = "image"
		mediaTypeToSave.Valid = true
	} else if requestBody.VideoURL != "" {
		videoUrlToSave.String = requestBody.VideoURL
		videoUrlToSave.Valid = true
		mediaTypeToSave.String = "video"
		mediaTypeToSave.Valid = true
	}
	if requestBody.OriginalPostID != "" {
		originalPostIdToSave.String = requestBody.OriginalPostID
		originalPostIdToSave.Valid = true
	}

	postID := ulid.Make().String() 

	_, err = db.Exec(
		"INSERT INTO posts (post_id, user_id, user_name, content, image_url, video_url, media_type, original_post_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		postID, userID, userName, requestBody.Content, imageUrlToSave, videoUrlToSave, mediaTypeToSave, originalPostIdToSave,
	)
	if err != nil {
		log.Printf("エラー: postsテーブルへのINSERTに失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// 作成した投稿の完全なデータを取得して返すロジック
	var createdPost Post
	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.video_url, p.media_type, p.created_at, p.original_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
            EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me
        FROM posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
        WHERE p.post_id = ?
    `
	row := db.QueryRow(query, userID, userID, userID, userID, postID)

	var content, imageURL, videoURL, mediaType, resOriginalPostID, userProfileImageURL sql.NullString
	var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
	var origCreatedAt sql.NullTime

	err = row.Scan(
		&createdPost.PostID, &createdPost.UserID, &content, &imageURL, &videoURL, &mediaType, &createdPost.CreatedAt, &resOriginalPostID,
		&createdPost.UserName, &userProfileImageURL,
		&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
		&origUserName, &origUserProfileImageURL,
		&createdPost.LikeCount, &createdPost.IsLikedByMe,
		&createdPost.BadCount, &createdPost.IsBaddedByMe, 
		&createdPost.ReplyCount,
		&createdPost.RetweetCount, &createdPost.IsRetweetedByMe,
		&createdPost.IsBookmarkedByMe,
	)
	if err != nil {
		log.Printf("作成された投稿の再取得に失敗: %v。post_idのみ返します。", err)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"post_id": postID})
		return
	}

	if content.Valid { createdPost.Content = &content.String }
	if imageURL.Valid { createdPost.ImageURL = &imageURL.String }
	if videoURL.Valid { createdPost.VideoURL = &videoURL.String }
	if mediaType.Valid { createdPost.MediaType = &mediaType.String }
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

	if originalPostIdToSave.Valid && originalPostIdToSave.String != "" {
		var originalPostAuthorID string
		// 引用された元の投稿の作者のIDを取得
		err := db.QueryRow("SELECT user_id FROM posts WHERE post_id = ?", originalPostIdToSave.String).Scan(&originalPostAuthorID)
		
		if err != nil {
			log.Printf("引用RTの通知作成のため、元の投稿の作者取得に失敗: %v", err)
		} else {
			// 自分自身を引用した場合は通知しない
			if originalPostAuthorID != userID {
				notificationID := ulid.Make().String()
				// 新しい通知タイプ 'quote_retweet' で通知を作成
				_, err := db.Exec(
					"INSERT INTO notifications (id, recipient_id, actor_id, type, entity_id) VALUES (?, ?, ?, 'quote_retweet', ?)",
					notificationID, originalPostAuthorID, userID, originalPostIdToSave.String,
				)
				if err != nil {
					log.Printf("引用RT通知の作成エラー: %v", err)
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(createdPost)

	if !strings.HasPrefix(userID, "bot_") {
		go func() {
			postContent := requestBody.Content
			if len([]rune(postContent)) > 80 {
				postContent = string([]rune(postContent)[:80]) + "..."
			}
			slackMsg := fmt.Sprintf("👤 %sさんが新しい投稿をしました:\n>>> %s", createdPost.UserName, postContent)
			sendSlackNotification(slackMsg)
		}()
	}
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
			// エラーがmysqlの特定のエラーかチェック
			if mysqlErr, ok := err.(*mysql.MySQLError); ok {
				// エラーコード1062は「Duplicate entry」(重複エントリ)
				if mysqlErr.Number == 1062 {
					// 既にいいねされているので、エラーとせず正常なレスポンスを返す
					log.Printf("いいね済みのためスキップ: user_id=%s, post_id=%s", userID, postID)
					w.WriteHeader(http.StatusConflict) // 409 Conflict: 既にリソースが存在する
					return
				}
			}
			// その他のDBエラーは500として処理
			log.Printf("エラー: db.Exec (insert like) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		var postAuthorID string
		// いいねされた投稿の作者を取得
		err = db.QueryRow("SELECT user_id FROM posts WHERE post_id = ?", postID).Scan(&postAuthorID)
		if err != nil {
			log.Printf("投稿の作者取得エラー: %v", err)
			// 通知は失敗してもいいね自体は成功しているので、処理は継続
		} else {
			// 自分自身の投稿にいいねした場合は通知しない
			if postAuthorID != userID {
				notificationID := ulid.Make().String()
				_, err := db.Exec(
					"INSERT INTO notifications (id, recipient_id, actor_id, type, entity_id) VALUES (?, ?, ?, 'like', ?)",
					notificationID, postAuthorID, userID, postID,
				)
				if err != nil {
					log.Printf("いいね通知の作成エラー: %v", err)
				}
			}
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

func badHandler(w http.ResponseWriter, r *http.Request) {
	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 5 {
		http.Error(w, "投稿IDが指定されていません", http.StatusBadRequest)
		return
	}
	postID := pathSegments[4]

	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		badID := ulid.Make().String()
		_, err := db.Exec("INSERT INTO bads (bad_id, user_id, post_id) VALUES (?, ?, ?)", badID, userID, postID)
		
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number == 1062 {
				log.Printf("よくないね済みのためスキップ: user_id=%s, post_id=%s", userID, postID)
				w.WriteHeader(http.StatusConflict)
				return
			}
			log.Printf("エラー: db.Exec (insert bad) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		// ※UXを考慮し、「よくないね」の場合は相手に通知を送りません。

		w.WriteHeader(http.StatusCreated)
		log.Printf("よくないね成功: user_id=%s, post_id=%s", userID, postID)

	case http.MethodDelete:
		_, err := db.Exec("DELETE FROM bads WHERE user_id = ? AND post_id = ?", userID, postID)
		if err != nil {
			log.Printf("エラー: db.Exec (delete bad) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		log.Printf("よくないね取り消し成功: user_id=%s, post_id=%s", userID, postID)

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

	// ユーザーからの返信をデータベースに保存
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
	
	
	var parentPostAuthorID, parentPostAuthorName, parentPostContent sql.NullString
    // 返信先の投稿（親投稿）の作者ID、名前、内容を取得
	err = db.QueryRow("SELECT p.user_id, COALESCE(u.name, p.user_name), p.content FROM posts p LEFT JOIN user u ON p.user_id = u.firebase_uid WHERE p.post_id = ?", parentPostID).Scan(&parentPostAuthorID, &parentPostAuthorName, &parentPostContent)
    
	if err != nil {
        log.Printf("リプライ通知、またはAI自動返信のため親投稿の作者取得エラー: %v", err)
    } else {
		// --- 1. 元の投稿者への通知 ---
        if parentPostAuthorID.Valid && parentPostAuthorID.String != userID {
            notificationID := ulid.Make().String()
            _, err := db.Exec(
                "INSERT INTO notifications (id, recipient_id, actor_id, type, entity_id) VALUES (?, ?, ?, 'reply', ?)",
                notificationID, parentPostAuthorID.String, userID, parentPostID,
            )
            if err != nil {
                log.Printf("リプライ通知の作成エラー: %v", err)
            }
        }

		// --- 2. 親投稿の作者がボットの場合、AIが自動で返信する（新しいロジック） ---
		if parentPostAuthorID.Valid && strings.HasPrefix(parentPostAuthorID.String, "bot_") {
			// ユーザーへのレスポンスをブロックしないよう、並行処理で実行
			go func(botAuthorID, botAuthorName, botOriginalPostContent, userReplyContent, userReplyID, userToNotifyID string) {
				// 3秒待機
				time.Sleep(3 * time.Second)

				// Geminiに渡すプロンプトを生成
				prompt := fmt.Sprintf(`あなたは、以下のSNS投稿をした本人（AIボット）です。
---
▼ あなたの元の投稿
「%s」
---
このあなたの投稿に対して、あるユーザーから以下の返信が来ました。
---
▼ ユーザーからの返信
「%s」
---
このユーザーの返信に対して、元の投稿の流れを踏まえた、自然でそれっぽい短い返信を、あなたのキャラクターになりきって1つだけ生成してください。返信は日本語で、返信文だけを出力してください。`, botOriginalPostContent, userReplyContent)

				// Geminiを使って返信内容を生成
				aiReplyContent, err := generateGeminiContent(prompt)
				if err != nil {
					log.Printf("ボットの自動返信生成に失敗: %v", err)
					return
				}
				aiReplyContent = strings.Trim(aiReplyContent, `"`)
				aiReplyContent = strings.TrimSpace(aiReplyContent)
				if aiReplyContent == "" {
					log.Println("Geminiが空の返信を生成したため、投稿をスキップします。")
					return
				}

				// AIが生成した返信をデータベースに保存
				aiReplyID := ulid.Make().String()
				_, err = db.Exec(
					"INSERT INTO posts (post_id, user_id, user_name, content, parent_post_id) VALUES (?, ?, ?, ?, ?)",
					aiReplyID, botAuthorID, botAuthorName, aiReplyContent, userReplyID,
				)
				if err != nil {
					log.Printf("ボットの自動返信のDB保存に失敗: %v", err)
					return
				}
				log.Printf("ボットの自動返信が成功しました: from_bot=%s, to_reply_of_user=%s", botAuthorID, userToNotifyID)

				// 返信したユーザーに「ボットから返信が来たこと」を通知する
				notificationToUser_ID := ulid.Make().String()
				_, err = db.Exec(
					"INSERT INTO notifications (id, recipient_id, actor_id, type, entity_id) VALUES (?, ?, ?, 'reply', ?)",
					notificationToUser_ID, userToNotifyID, botAuthorID, userReplyID,
				)
				if err != nil {
					log.Printf("ボットの自動返信に関する通知の作成エラー: %v", err)
				}

			}(parentPostAuthorID.String, parentPostAuthorName.String, parentPostContent.String, requestBody.Content, replyID, userID)
		}
    }

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"reply_id": replyID})
	log.Printf("リプライ作成成功: reply_id=%s, parent_id=%s\n", replyID, parentPostID)
}

func repliesGetHandler(w http.ResponseWriter, r *http.Request) {
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
            p.post_id, p.user_id, COALESCE(u.name, p.user_name), u.profile_image_url, p.content, p.image_url, p.video_url, p.media_type, p.created_at,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
			(SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
			EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me
        FROM posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        WHERE p.parent_post_id = ?
        ORDER BY p.created_at ASC
    `

	rows, err := db.Query(query, currentUserID, currentUserID, currentUserID, parentPostID)
	if err != nil {
		log.Printf("エラー: db.Query (replies) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	replies := make([]Post, 0)
	for rows.Next() {
		var p Post
		var userName, profileImageURL, content, imageURL, videoURL, mediaType sql.NullString
		
		err := rows.Scan(
			&p.PostID, &p.UserID, &userName, &profileImageURL, &content, &imageURL, &videoURL, &mediaType, &p.CreatedAt,
			&p.LikeCount, &p.IsLikedByMe,
			&p.BadCount, &p.IsBaddedByMe, 
			&p.ReplyCount, 
			&p.IsBookmarkedByMe,
		)

		if err != nil {
			log.Printf("エラー: rows.Scan (replies) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if userName.Valid { p.UserName = userName.String }
		if profileImageURL.Valid { p.UserProfileImageURL = &profileImageURL.String }
		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if videoURL.Valid { p.VideoURL = &videoURL.String }
		if mediaType.Valid { p.MediaType = &mediaType.String }

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
	profileUserID := pathSegments[3]

	log.Printf("特定ユーザーの投稿検索を開始: user_id=%s\n", profileUserID)

	currentUserID := ""
	if id, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = id
	}
	
	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.video_url, p.media_type, p.created_at, p.original_post_id, p.parent_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
			(SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
			EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me,
			orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            parent_p.post_id, parent_p.user_id,
            parent_u.name
        FROM posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
		LEFT JOIN posts AS parent_p ON p.parent_post_id = parent_p.post_id
        LEFT JOIN user AS parent_u ON parent_p.user_id = parent_u.firebase_uid
        WHERE p.user_id = ? AND p.parent_post_id IS NULL --
        ORDER BY p.created_at DESC
    `
	rows, err := db.Query(query, currentUserID, currentUserID, currentUserID, currentUserID, profileUserID)
	if err != nil {
		log.Printf("エラー: db.Query (user posts) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, videoURL, mediaType, originalPostID, parentPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime
		var parentP_PostID, parentP_UserID, parentU_Name sql.NullString

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &videoURL, &mediaType, &p.CreatedAt, &originalPostID, &parentPostID,
			&p.UserName, &userProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe,
			&p.BadCount, &p.IsBaddedByMe,
			&p.ReplyCount,
			&p.RetweetCount, &p.IsRetweetedByMe,
			&p.IsBookmarkedByMe,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
			&origUserName, &origUserProfileImageURL,
			&parentP_PostID, &parentP_UserID, &parentU_Name,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (user posts) に失敗: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if videoURL.Valid { p.VideoURL = &videoURL.String }
        if mediaType.Valid { p.MediaType = &mediaType.String }
		if userProfileImageURL.Valid { p.UserProfileImageURL = &userProfileImageURL.String }

		if originalPostID.Valid {
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

		if parentPostID.Valid {
			p.ParentPost = &Post{
				PostID:   parentP_PostID.String,
				UserID:   parentP_UserID.String,
				UserName: parentU_Name.String,
			}
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
	log.Printf("特定ユーザーの投稿取得リクエスト成功: user_id=%s\n", profileUserID)
}

// userRepliesHandler は、指定されたユーザーのリプライのみを一覧で取得します。
func userRepliesHandler(w http.ResponseWriter, r *http.Request) {
	// URLから対象のユーザーIDを取得
	pathWithoutSuffix := strings.TrimSuffix(r.URL.Path, "/replies")
	pathSegments := strings.Split(pathWithoutSuffix, "/")
	if len(pathSegments) < 4 {
		http.Error(w, "ユーザーIDが指定されていません", http.StatusBadRequest)
		return
	}
	profileUserID := pathSegments[3]

	// ログイン中のユーザーIDを取得（未ログインの場合は空文字）
	currentUserID := ""
	if id, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = id
	}
	
	// 親投稿の全文情報(content, image_urlなど)を取得するSQLクエリ
	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.video_url, p.media_type, 
            p.created_at, p.original_post_id, p.parent_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
            (SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
            EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me,
            
            orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            
            parent_p.post_id, parent_p.user_id, parent_p.content, parent_p.image_url, parent_p.created_at,
            parent_u.name, parent_u.profile_image_url
        FROM posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
        LEFT JOIN posts AS parent_p ON p.parent_post_id = parent_p.post_id
        LEFT JOIN user AS parent_u ON parent_p.user_id = parent_u.firebase_uid
        WHERE p.user_id = ? AND p.parent_post_id IS NOT NULL
        ORDER BY p.created_at DESC
    `
	rows, err := db.Query(query, currentUserID, currentUserID, currentUserID, currentUserID, profileUserID)
	if err != nil {
		log.Printf("エラー: db.Query (user replies) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, videoURL, mediaType, originalPostID, parentPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime
		
		var parentP_PostID, parentP_UserID, parentP_Content, parentP_ImageURL, parentU_Name, parentU_ProfileImageURL sql.NullString
		var parentP_CreatedAt sql.NullTime

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &videoURL, &mediaType, &p.CreatedAt, &originalPostID, &parentPostID,
			&p.UserName, &userProfileImageURL, &p.LikeCount, &p.IsLikedByMe, &p.BadCount, &p.IsBaddedByMe,
			&p.ReplyCount, &p.RetweetCount, &p.IsRetweetedByMe, &p.IsBookmarkedByMe,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt, &origUserName, &origUserProfileImageURL,
			&parentP_PostID, &parentP_UserID, &parentP_Content, &parentP_ImageURL, &parentP_CreatedAt, &parentU_Name, &parentU_ProfileImageURL,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (user replies) に失敗: %v", err)
			continue
		}
		if content.Valid { p.Content = &content.String }
        if imageURL.Valid { p.ImageURL = &imageURL.String }
        if videoURL.Valid { p.VideoURL = &videoURL.String }
        if mediaType.Valid { p.MediaType = &mediaType.String }
        if userProfileImageURL.Valid { p.UserProfileImageURL = &userProfileImageURL.String }
		if originalPostID.Valid { /* 省略 */ }

		// 親投稿の完全なオブジェクトを生成
		if parentPostID.Valid {
			var parentPost Post
			parentPost.PostID = parentP_PostID.String
			parentPost.UserID = parentP_UserID.String
			if parentU_Name.Valid { parentPost.UserName = parentU_Name.String }
			if parentP_Content.Valid { parentPost.Content = &parentP_Content.String }
			if parentP_ImageURL.Valid { parentPost.ImageURL = &parentP_ImageURL.String }
			if parentU_ProfileImageURL.Valid { parentPost.UserProfileImageURL = &parentU_ProfileImageURL.String }
			if parentP_CreatedAt.Valid { parentPost.CreatedAt = parentP_CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00") }
			p.ParentPost = &parentPost
		}
		posts = append(posts, p)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(posts)
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

func videoUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// 'video'というキーでフォームデータからファイルを取得
	file, _, err := r.FormFile("video")
	if err != nil {
		log.Printf("動画の取得に失敗: %v", err)
		http.Error(w, "動画の取得に失敗しました", http.StatusBadRequest)
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

	// ULIDでユニークなファイル名を生成（拡張子は.mp4と仮定）
	objectName := ulid.Make().String() + ".mp4"
	
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
	json.NewEncoder(w).Encode(map[string]string{"videoUrl": publicURL}) // videoUrlとして返す
	log.Printf("動画アップロード成功: %s", publicURL)
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


// userRouterHandlerは /api/users/ へのリクエストをURLの末尾によってさらに振り分けます。

func userRouterHandler(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/follow") {
		followHandler(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/following") {
		followingListHandler(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/followers") {
		followerListHandler(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/posts") {
		userPostsHandler(w, r)
		return
	}
	// ▼▼▼ このifブロックを追加 ▼▼▼
	if strings.HasSuffix(r.URL.Path, "/replies") {
		userRepliesHandler(w, r)
		return
	}
	// ▲▲▲ 追加ここまで ▲▲▲
	getUserProfileHandler(w, r)
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "検索キーワードが指定されていません", http.StatusBadRequest)
		return
	}

	log.Printf("検索リクエスト受信: キーワード=%s", query)
	searchTerm := "%" + query + "%"

	currentUserID := ""
	if userID, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = userID
	}

	sqlQuery := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.video_url, p.media_type, p.created_at, p.original_post_id, p.parent_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
			(SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
            EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me,
			orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            parent_p.post_id, parent_p.user_id,
            parent_u.name
        FROM
            posts p
        LEFT JOIN
            user u ON p.user_id = u.firebase_uid
		LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
		LEFT JOIN posts AS parent_p ON p.parent_post_id = parent_p.post_id
        LEFT JOIN user AS parent_u ON parent_p.user_id = parent_u.firebase_uid
        WHERE
            p.content LIKE ? OR u.name LIKE ?
        ORDER BY
            p.created_at DESC
    `

	rows, err := db.Query(sqlQuery, currentUserID, currentUserID, currentUserID, currentUserID, searchTerm, searchTerm)
	if err != nil {
		log.Printf("エラー: db.Query (search) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, videoURL, mediaType, originalPostID, parentPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime
		var parentP_PostID, parentP_UserID, parentU_Name sql.NullString

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &videoURL, &mediaType, &p.CreatedAt, &originalPostID, &parentPostID,
			&p.UserName, &userProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe,
			&p.BadCount, &p.IsBaddedByMe,
			&p.ReplyCount, &p.RetweetCount, &p.IsRetweetedByMe, &p.IsBookmarkedByMe,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt, &origUserName, &origUserProfileImageURL,
			&parentP_PostID, &parentP_UserID, &parentU_Name,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (search) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if videoURL.Valid { p.VideoURL = &videoURL.String }
		if mediaType.Valid { p.MediaType = &mediaType.String }
		if userProfileImageURL.Valid { p.UserProfileImageURL = &userProfileImageURL.String }

		if originalPostID.Valid {
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

        if parentPostID.Valid {
            p.ParentPost = &Post{
                PostID:   parentP_PostID.String,
                UserID:   parentP_UserID.String,
                UserName: parentU_Name.String,
            }
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
				(SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            	EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
				(SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
				(SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
				EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
				EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me
			FROM posts p
			LEFT JOIN user u ON p.user_id = u.firebase_uid
			LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
			LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
			WHERE p.post_id = ?
		`
		row := db.QueryRow(query, userID, userID, userID, newPostID)

		var content, imageURL, videoURL, mediaType, resOriginalPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime

		err = row.Scan(
			&newRetweet.PostID, &newRetweet.UserID, &content, &imageURL, &videoURL, &mediaType, &newRetweet.CreatedAt, &resOriginalPostID,
			&newRetweet.UserName, &userProfileImageURL,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
			&origUserName, &origUserProfileImageURL,
			&newRetweet.LikeCount, &newRetweet.IsLikedByMe,
			&newRetweet.BadCount, &newRetweet.IsBaddedByMe,
			&newRetweet.ReplyCount,
			&newRetweet.RetweetCount, &newRetweet.IsRetweetedByMe,
			&newRetweet.IsBookmarkedByMe,
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

// quoteRetweetsGetHandler は、特定の投稿への引用リツイートを一覧で取得します。
func quoteRetweetsGetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}
	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 5 {
		http.Error(w, "投稿IDが指定されていません", http.StatusBadRequest)
		return
	}
	originalPostID := pathSegments[4]
	currentUserID := ""
	if userID, ok := r.Context().Value(userIDKey).(string); ok {
		currentUserID = userID
	}

	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.video_url, p.media_type, p.created_at, p.original_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
			(SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
			EXISTS(SELECT 1 FROM bookmarks WHERE post_id = p.post_id AND user_id = ?) AS is_bookmarked_by_me
        FROM posts p
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
        WHERE p.original_post_id = ? AND p.content IS NOT NULL
        ORDER BY p.created_at DESC
    `

	rows, err := db.Query(query, currentUserID, currentUserID, currentUserID, currentUserID, originalPostID)
	if err != nil {
		log.Printf("エラー: db.Query (quote retweets) に失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, videoURL, mediaType, resOriginalPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &videoURL, &mediaType, &p.CreatedAt, &resOriginalPostID,
			&p.UserName, &userProfileImageURL,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt,
			&origUserName, &origUserProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe,
			&p.BadCount, &p.IsBaddedByMe,
			&p.ReplyCount,
			&p.RetweetCount, &p.IsRetweetedByMe,
			&p.IsBookmarkedByMe,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (quote retweets) に失敗: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if videoURL.Valid { p.VideoURL = &videoURL.String }
		if mediaType.Valid { p.MediaType = &mediaType.String }
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

// followHandler は、ユーザーのフォロー・アンフォローを処理します。
func followHandler(w http.ResponseWriter, r *http.Request) {

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

		notificationID := ulid.Make().String()
		_, err = db.Exec(
			"INSERT INTO notifications (id, recipient_id, actor_id, type) VALUES (?, ?, ?, 'follow')",
			notificationID, followingID, followerID,
		)
		if err != nil {
			log.Printf("フォロー通知の作成エラー: %v", err)
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

// getRecommendedUsersHandler は、ログインユーザーがフォローしていないユーザーをランダムに返します
func getRecommendedUsersHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	// 自分自身と、自分が既にフォローしているユーザー、そしてボットを除外して、
	// ランダムに3人取得するクエリ
	query := `
		SELECT firebase_uid, name, profile_image_url, bio, id
		FROM user
		WHERE firebase_uid != ?
		  AND firebase_uid NOT LIKE 'bot_%'
		  AND firebase_uid NOT IN (
			SELECT following_id FROM follows WHERE follower_id = ?
		  )
		ORDER BY RAND()
		LIMIT 3
	`

	rows, err := db.Query(query, userID, userID)
	if err != nil {
		log.Printf("おすすめユーザーの取得エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// UserProfile.tsxのUserProfileData型に合わせてデータを準備
	type RecommendedUser struct {
		FirebaseUID     string  `json:"firebase_uid"`
		Name            string  `json:"name"`
		ProfileImageURL *string `json:"profile_image_url"`
		Bio             *string `json:"bio"`
		ID              string  `json:"id"`
	}

	users := make([]RecommendedUser, 0)
	for rows.Next() {
		var u RecommendedUser
		// Scanのフィールドをクエリに合わせて修正
		err := rows.Scan(&u.FirebaseUID, &u.Name, &u.ProfileImageURL, &u.Bio, &u.ID)
		if err != nil {
			log.Printf("おすすめユーザーのデータ読み取りエラー: %v", err)
			continue
		}
		users = append(users, u)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}


// followingListHandler は、指定されたユーザーがフォローしているユーザーの一覧を返します
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
	re := regexp.MustCompile(`#([\p{L}\p{N}_]+)`)
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

// getConversationsHandlerは、ログインユーザーが参加している会話の一覧を返します。
func getConversationsHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
        return
    }

    currentUserID, ok := r.Context().Value(userIDKey).(string)
    if !ok || currentUserID == "" {
        http.Error(w, "この操作には認証が必要です", http.StatusUnauthorized)
        return
    }

    // ユーザーが参加している会話、その会話の相手、最新のメッセージを取得する複雑なクエリ
    query := `
        SELECT
            c.id AS conversation_id,
            c.updated_at,
            other_user.firebase_uid,
            other_user.name,
            other_user.profile_image_url,
            last_msg.id,
            last_msg.sender_id,
            last_msg.content,
            last_msg.created_at
        FROM conversations c
        -- 自分が参加している会話IDを見つける
        JOIN conversation_participants my_cp ON c.id = my_cp.conversation_id
        -- 同じ会話に参加している、自分以外の相手を見つける
        JOIN conversation_participants other_cp ON c.id = other_cp.conversation_id AND my_cp.user_id != other_cp.user_id
        -- 相手のユーザー情報を取得
        JOIN user other_user ON other_cp.user_id = other_user.firebase_uid
        -- 各会話の最新のメッセージをLEFT JOINで取得 (メッセージがない会話も考慮)
        LEFT JOIN (
            SELECT 
                m.id, m.conversation_id, m.sender_id, m.content, m.created_at,
                ROW_NUMBER() OVER(PARTITION BY m.conversation_id ORDER BY m.created_at DESC) as rn
            FROM messages m
        ) AS last_msg ON c.id = last_msg.conversation_id AND last_msg.rn = 1
        WHERE my_cp.user_id = ?
        ORDER BY c.updated_at DESC;
    `

    rows, err := db.Query(query, currentUserID)
    if err != nil {
        log.Printf("会話一覧の取得エラー: %v", err)
        http.Error(w, "サーバーエラー", http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    conversations := make([]Conversation, 0)
    for rows.Next() {
        var conv Conversation
        var lastMsg Message
        var otherUser UserResForHTTPGet
        var otherUserFirebaseUID, otherUserName, lastMsgID, lastMsgSenderID, lastMsgContent sql.NullString
        var otherUserProfileImgURL sql.NullString
        var lastMsgCreatedAt sql.NullTime

        err := rows.Scan(
            &conv.ConversationID,
            &conv.UpdatedAt,
            &otherUserFirebaseUID,
            &otherUserName,
            &otherUserProfileImgURL,
            &lastMsgID,
            &lastMsgSenderID,
            &lastMsgContent,
            &lastMsgCreatedAt,
        )
        if err != nil {
            log.Printf("会話データの読み取りエラー: %v", err)
            http.Error(w, "サーバーエラー", http.StatusInternalServerError)
            return
        }

        // 他のユーザー情報をセット
        if otherUserFirebaseUID.Valid { otherUser.FirebaseUID = &otherUserFirebaseUID.String }
        if otherUserName.Valid { otherUser.Name = otherUserName.String }
        if otherUserProfileImgURL.Valid { otherUser.ProfileImageURL = &otherUserProfileImgURL.String }
        conv.OtherUser = otherUser

        // 最新メッセージがあればセット
        if lastMsgID.Valid {
            lastMsg.ID = lastMsgID.String
            lastMsg.SenderID = lastMsgSenderID.String
            lastMsg.Content = lastMsgContent.String
            lastMsg.CreatedAt = lastMsgCreatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
            conv.LastMessage = &lastMsg
        }

        conversations = append(conversations, conv)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(conversations)
}

// getMessagesHandlerは、特定の会話内のメッセージ一覧を返します。
func getMessagesHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
        return
    }

    currentUserID, ok := r.Context().Value(userIDKey).(string)
    if !ok || currentUserID == "" {
        http.Error(w, "この操作には認証が必要です", http.StatusUnauthorized)
        return
    }

    // URLから会話IDを取得 (例: /api/conversations/{conv_id}/messages)
    pathWithoutSuffix := strings.TrimSuffix(r.URL.Path, "/messages")
    pathSegments := strings.Split(pathWithoutSuffix, "/")
    if len(pathSegments) < 4 {
        http.Error(w, "会話IDが指定されていません", http.StatusBadRequest)
        return
    }
    conversationID := pathSegments[3]

    // セキュリティチェック：ログインユーザーがこの会話の参加者であることを確認
    var participantCount int
    err := db.QueryRow("SELECT COUNT(*) FROM conversation_participants WHERE conversation_id = ? AND user_id = ?", conversationID, currentUserID).Scan(&participantCount)
    if err != nil {
        log.Printf("会話の参加者チェックエラー: %v", err)
        http.Error(w, "サーバーエラー", http.StatusInternalServerError)
        return
    }
    if participantCount == 0 {
        http.Error(w, "この会話へのアクセス権がありません", http.StatusForbidden)
        return
    }

    // メッセージを取得
    rows, err := db.Query("SELECT id, sender_id, content, created_at FROM messages WHERE conversation_id = ? ORDER BY created_at ASC", conversationID)
    if err != nil {
        log.Printf("メッセージ一覧の取得エラー: %v", err)
        http.Error(w, "サーバーエラー", http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    messages := make([]Message, 0)
    for rows.Next() {
        var msg Message
        msg.ConversationID = conversationID // conversation_idはクエリから取得したものを使う
        err := rows.Scan(&msg.ID, &msg.SenderID, &msg.Content, &msg.CreatedAt)
        if err != nil {
            log.Printf("メッセージデータの読み取りエラー: %v", err)
            http.Error(w, "サーバーエラー", http.StatusInternalServerError)
            return
        }
        messages = append(messages, msg)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(messages)
}

func conversationRouterHandler(w http.ResponseWriter, r *http.Request) {
	// 末尾が /messages の場合、メソッドによって処理を分岐
	if strings.HasSuffix(r.URL.Path, "/messages") {
		switch r.Method {
		case http.MethodGet:
			getMessagesHandler(w, r)
		case http.MethodPost:
			sendMessageHandler(w, r) // ★ この行を追加
		default:
			http.Error(w, "許可されていないメソッドです", http.StatusMethodNotAllowed)
		}
		return
	}

	http.NotFound(w, r)
}

// sendMessageHandler は、特定の会話に新しいメッセージを投稿します。
func sendMessageHandler(w http.ResponseWriter, r *http.Request) {
	// このエンドポイントはPOSTメソッドのみを許可
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// コンテキストから送信者のユーザーIDを取得
	senderID, ok := r.Context().Value(userIDKey).(string)
	if !ok || senderID == "" {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	// URLから会話IDを取得
	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 5 {
		http.Error(w, "会話IDが指定されていません", http.StatusBadRequest)
		return
	}
	conversationID := pathSegments[3]

	// リクエストボディからメッセージ内容をデコード
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "無効なリクエストボディです", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		http.Error(w, "メッセージ内容が空です", http.StatusBadRequest)
		return
	}

	// トランザクションを開始
	tx, err := db.Begin()
	if err != nil {
		log.Printf("トランザクション開始エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}
	// 関数終了時にロールバックを試み、エラーがあればログに出力
	defer tx.Rollback()

	// 新しいメッセージのIDを生成
	messageID := ulid.Make().String()

	// データベースにメッセージを挿入
	_, err = tx.Exec(
		"INSERT INTO messages (id, conversation_id, sender_id, content) VALUES (?, ?, ?, ?)",
		messageID, conversationID, senderID, req.Content,
	)
	if err != nil {
		log.Printf("メッセージの挿入エラー: %v", err)
		http.Error(w, "メッセージの保存に失敗しました", http.StatusInternalServerError)
		return
	}

	// 会話の最終更新日時を更新
	_, err = tx.Exec("UPDATE conversations SET updated_at = NOW() WHERE id = ?", conversationID)
	if err != nil {
		log.Printf("会話の更新日時変更エラー: %v", err)
		http.Error(w, "メッセージの保存に失敗しました", http.StatusInternalServerError)
		return
	}

	// トランザクションをコミット
	if err := tx.Commit(); err != nil {
		log.Printf("トランザクションのコミットエラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	// フロントエンドでUIを更新するために、作成したメッセージ情報を取得して返す
	var createdMessage Message
	err = db.QueryRow(
		"SELECT id, conversation_id, sender_id, content, created_at FROM messages WHERE id = ?",
		messageID,
	).Scan(&createdMessage.ID, &createdMessage.ConversationID, &createdMessage.SenderID, &createdMessage.Content, &createdMessage.CreatedAt)

	if err != nil {
		log.Printf("送信済みメッセージの取得エラー: %v", err)
		// メッセージの保存自体は成功しているので、ここでは空の成功レスポンスを返す
		w.WriteHeader(http.StatusCreated)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(createdMessage)
}


// startConversationHandler は、指定されたユーザーとの会話を開始、または既存の会話を取得します。
func startConversationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// リクエストを開始したユーザー（自分）のIDを取得
	senderID, ok := r.Context().Value(userIDKey).(string)
	if !ok || senderID == "" {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	// リクエストボディから会話相手のIDをデコード
	var req struct {
		RecipientID string `json:"recipient_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "無効なリクエストボディです", http.StatusBadRequest)
		return
	}

	recipientID := req.RecipientID
	if recipientID == "" {
		http.Error(w, "会話相手のIDが指定されていません", http.StatusBadRequest)
		return
	}

	if senderID == recipientID {
		http.Error(w, "自分自身と会話を開始することはできません", http.StatusBadRequest)
		return
	}

	// まず、この2人だけの会話が既に存在するかどうかをチェック
	var existingConversationID string
	query := `
		SELECT cp1.conversation_id
		FROM conversation_participants AS cp1
		JOIN conversation_participants AS cp2 ON cp1.conversation_id = cp2.conversation_id
		WHERE cp1.user_id = ? AND cp2.user_id = ?
		GROUP BY cp1.conversation_id
		HAVING COUNT(cp1.conversation_id) = 1 AND (SELECT COUNT(*) FROM conversation_participants WHERE conversation_id = cp1.conversation_id) = 2
	`
	err := db.QueryRow(query, senderID, recipientID).Scan(&existingConversationID)

	// 会話が既に存在する場合
	if err == nil {
		log.Printf("既存の会話が見つかりました: %s", existingConversationID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"conversation_id": existingConversationID})
		return
	}
	
	// `sql.ErrNoRows` 以外はDBエラー
	if err != sql.ErrNoRows {
		log.Printf("既存の会話の検索エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	//--- 会話が存在しない場合、新規作成 ---
	log.Println("新しい会話を作成します...")

	// トランザクションを開始
	tx, err := db.Begin()
	if err != nil {
		log.Printf("トランザクション開始エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// 1. 新しい会話IDを生成
	newConversationID := ulid.Make().String()

	// 2. `conversations` テーブルに新しい会話を挿入
	_, err = tx.Exec("INSERT INTO conversations (id) VALUES (?)", newConversationID)
	if err != nil {
		log.Printf("conversationsテーブルへの挿入エラー: %v", err)
		http.Error(w, "会話の作成に失敗しました", http.StatusInternalServerError)
		return
	}

	// 3. `conversation_participants` テーブルに2人の参加者を追加
	_, err = tx.Exec(
		"INSERT INTO conversation_participants (conversation_id, user_id) VALUES (?, ?), (?, ?)",
		newConversationID, senderID, newConversationID, recipientID,
	)
	if err != nil {
		log.Printf("conversation_participantsテーブルへの挿入エラー: %v", err)
		http.Error(w, "会話の作成に失敗しました", http.StatusInternalServerError)
		return
	}

	// トランザクションをコミット
	if err := tx.Commit(); err != nil {
		log.Printf("会話作成トランザクションのコミットエラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	log.Printf("新しい会話を正常に作成しました: %s", newConversationID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"conversation_id": newConversationID})
}

func getUnreadNotificationCountHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	var count int
	// is_readがFALSEのレコードの数をカウントする
	err := db.QueryRow("SELECT COUNT(*) FROM notifications WHERE recipient_id = ? AND is_read = FALSE", userID).Scan(&count)
	if err != nil {
		log.Printf("未読通知件数の取得エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"count": count})
}


func getBookmarksHandler(w http.ResponseWriter, r *http.Request) {
	currentUserID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	query := `
        SELECT
            p.post_id, p.user_id, p.content, p.image_url, p.video_url, p.media_type, p.created_at, p.original_post_id, p.parent_post_id,
            COALESCE(u.name, p.user_name) AS user_name, u.profile_image_url,
            (SELECT COUNT(*) FROM likes WHERE post_id = p.post_id) AS like_count,
            EXISTS(SELECT 1 FROM likes WHERE post_id = p.post_id AND user_id = ?) AS is_liked_by_me,
			(SELECT COUNT(*) FROM bads WHERE post_id = p.post_id) AS bad_count,
            EXISTS(SELECT 1 FROM bads WHERE post_id = p.post_id AND user_id = ?) AS is_badded_by_me,
            (SELECT COUNT(*) FROM posts WHERE parent_post_id = p.post_id) AS reply_count,
            (SELECT COUNT(*) FROM posts WHERE original_post_id = p.post_id) AS retweet_count,
            EXISTS(SELECT 1 FROM posts WHERE original_post_id = p.post_id AND user_id = ? AND content IS NULL) AS is_retweeted_by_me,
            TRUE AS is_bookmarked_by_me,
			orig_p.post_id, orig_p.user_id, orig_p.content, orig_p.image_url, orig_p.created_at,
            orig_u.name, orig_u.profile_image_url,
            parent_p.post_id, parent_p.user_id,
            parent_u.name
        FROM posts p
        JOIN bookmarks b ON p.post_id = b.post_id
        LEFT JOIN user u ON p.user_id = u.firebase_uid
        LEFT JOIN posts AS orig_p ON p.original_post_id = orig_p.post_id
        LEFT JOIN user AS orig_u ON orig_p.user_id = orig_u.firebase_uid
		LEFT JOIN posts AS parent_p ON p.parent_post_id = parent_p.post_id
        LEFT JOIN user AS parent_u ON parent_p.user_id = parent_u.firebase_uid
        WHERE b.user_id = ?
        ORDER BY b.created_at DESC
    `

	rows, err := db.Query(query, currentUserID, currentUserID, currentUserID, currentUserID)
	if err != nil {
		log.Printf("ブックマーク投稿の取得エラー: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	posts := make([]Post, 0)
	for rows.Next() {
		var p Post
		var content, imageURL, videoURL, mediaType, originalPostID, parentPostID, userProfileImageURL sql.NullString
		var origPostID, origUserID, origContent, origImageURL, origUserName, origUserProfileImageURL sql.NullString
		var origCreatedAt sql.NullTime
		var parentP_PostID, parentP_UserID, parentU_Name sql.NullString

		err := rows.Scan(
			&p.PostID, &p.UserID, &content, &imageURL, &videoURL, &mediaType, &p.CreatedAt, &originalPostID, &parentPostID,
			&p.UserName, &userProfileImageURL,
			&p.LikeCount, &p.IsLikedByMe,
			&p.BadCount, &p.IsBaddedByMe,
			&p.ReplyCount, &p.RetweetCount, &p.IsRetweetedByMe,
			&p.IsBookmarkedByMe,
			&origPostID, &origUserID, &origContent, &origImageURL, &origCreatedAt, &origUserName, &origUserProfileImageURL,
			&parentP_PostID, &parentP_UserID, &parentU_Name,
		)
		if err != nil {
			log.Printf("エラー: rows.Scan (bookmarks) に失敗しました: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if content.Valid { p.Content = &content.String }
		if imageURL.Valid { p.ImageURL = &imageURL.String }
		if videoURL.Valid { p.VideoURL = &videoURL.String }
		if mediaType.Valid { p.MediaType = &mediaType.String }
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
		log.Printf("エラー: json.Marshal (bookmarks) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(bytes)
}

// bookmarkHandler は投稿のお気に入り登録・解除を処理します
func bookmarkHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok {
		http.Error(w, "認証情報が見つかりません", http.StatusUnauthorized)
		return
	}

	pathSegments := strings.Split(r.URL.Path, "/")
	// URLの形式が /api/posts/bookmark/{post_id} であることを想定
	if len(pathSegments) < 5 {
		http.Error(w, "投稿IDが指定されていません", http.StatusBadRequest)
		return
	}
	// ★★★ 修正点: [3]から[4]に変更 ★★★
	postID := pathSegments[4]

	switch r.Method {
	case http.MethodPost:
		_, err := db.Exec("INSERT INTO bookmarks (user_id, post_id) VALUES (?, ?)", userID, postID)
		if err != nil {
			log.Printf("ブックマークの作成エラー: %v", err)
			http.Error(w, "サーバーエラー", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		_, err := db.Exec("DELETE FROM bookmarks WHERE user_id = ? AND post_id = ?", userID, postID)
		if err != nil {
			log.Printf("ブックマークの削除エラー: %v", err)
			http.Error(w, "サーバーエラー", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "許可されていないメソッドです", http.StatusMethodNotAllowed)
	}
}

// generateGeminiContent は、Geminiにリクエストを送り、投稿文を生成する補助関数です
func generateGeminiContent(prompt string) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY が設定されていません")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return "", fmt.Errorf("Geminiクライアントの作成に失敗: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel("gemini-1.5-flash")
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("Geminiからのコンテンツ生成に失敗: %w", err)
	}

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		if text, ok := resp.Candidates[0].Content.Parts[0].(genai.Text); ok {
			return string(text), nil
		}
	}
	return "", fmt.Errorf("Geminiから有効なコンテンツが生成されませんでした")
}

// ★ フロントエンドからのリクエストボディをマッピングするための構造体
type BotRequest struct {
	Topic string `json:"topic,omitempty"`
}

// GeminiからのJSONレスポンスを格納するための構造体
type BotPersona struct {
	Name string `json:"name"`
	Bio  string `json:"bio"`
	Post string `json:"post"`
}

func createNewBotAndPostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}
	log.Println("新規ボット生成＆投稿リクエストを受信...")

	// ★ リクエストボディからユーザー指定のトピックを読み取る
	var req BotRequest
	// ボディが空の場合でもエラーとしないように、エラーは無視する
	_ = json.NewDecoder(r.Body).Decode(&req)

	// --- 1. ボットの基本情報を準備 ---
	rand.Seed(time.Now().UnixNano())
	
	profileImageURL := fmt.Sprintf("https://picsum.photos/seed/%s/400/400", ulid.Make().String())
	headerImageURL := fmt.Sprintf("https://picsum.photos/seed/%s/1500/500", ulid.Make().String())
	
	botULID := ulid.Make().String()
	botFirebaseUID := "bot_" + botULID

	// --- 2. Geminiでペルソナ（名前、自己紹介、投稿）を一括生成 ---
	creativeAdjectives := []string{"風変わりな", "個性的な", "意外な一面を持つ", "思慮深い", "陽気な", "少し内気な", "夢見がちな", "現実的な", "インドア派の"}
	randomAdjective := creativeAdjectives[rand.Intn(len(creativeAdjectives))]

	// ★★★ ここからテーマ決定ロジック ★★★
	var finalTheme string
	if req.Topic != "" {
		// リクエストでトピックが指定されていれば、それを使用
		finalTheme = req.Topic
		log.Printf("ユーザー指定トピックを使用: %s", finalTheme)
	} else {
		// 指定がなければ、従来通りランダムなテーマを使用
		tweetThemes := []string{
			"仕事や勉強のちょっとした気づき", "最近見た映画やアニメ、読んだ本についての感想", "週末の予定や、次の休みにやりたいこと",
			"個人的な小さな目標や挑戦について", "ふと目にした面白いニュースや雑学", "人間関係でふと感じたこと",
			"最近買ってよかったものや、欲しいもの", "ふと昔を思い出して懐かしくなったこと", "今日の天気や季節の変わり目について感じること", "最近聴いている音楽について",
		}
		finalTheme = tweetThemes[rand.Intn(len(tweetThemes))]
		log.Printf("ランダムトピックを使用: %s", finalTheme)
	}
	// ★★★ ここまでテーマ決定ロジック ★★★

	personaPrompt := fmt.Sprintf(`
		日本のSNSにいる、ごく一般的な「%s」架空の人物を1人、ランダムに創造してください。
		その人物について、以下の情報をJSON形式で出力してください。

		- "name": 架空のSNSアカウント名。本名ではなく、ひらがな、カタカナ、ローマ字、またはそれらの組み合わせで作られた、個性的で覚えやすいニックネームやハンドル名です。(例: もちまる, ねこ吸い, Kaito_std, くりーむ)
		- "bio": そのアカウントの自己紹介文(80文字程度)。趣味、好きなこと、最近ハマっていること、座右の銘などを簡潔に書いた、専門性のないごく一般的な自己紹介です。
		- "post": そのアカウントが「%s」というテーマで「いま、ふと思ったこと」を投稿する、140文字程度の自然なツイート。内容は、日常の出来事、個人的な意見、ちょっとした発見、面白いと感じたことなど、多岐にわたるようにしてください。ハッシュタグは、付けても付けなくても構いません。もし付ける場合は、文脈に合ったものを1つか2つ、自然な形で付けてください。

		毎回、全く異なる個性と内容の人物を生成してください。
		出力はJSONオブジェクトだけにしてください。
	`, randomAdjective, finalTheme) // ★ 最終決定したテーマをプロンプトに埋め込む

	personaJson, err := generateGeminiContent(personaPrompt)
	if err != nil {
		log.Printf("Geminiペルソナ生成エラー: %v", err)
		http.Error(w, "AIによる投稿生成に失敗しました", http.StatusInternalServerError)
		return
	}
	log.Printf("Geminiが生成したJSON: %s", personaJson)

	var persona BotPersona
	re := regexp.MustCompile("(?s)```json\n(.*?)\n```")
    matches := re.FindStringSubmatch(personaJson)
    jsonToParse := personaJson
    if len(matches) > 1 {
        jsonToParse = matches[1]
    }

	if err := json.Unmarshal([]byte(jsonToParse), &persona); err != nil {
		log.Printf("GeminiのJSONパースエラー: %v", err)
		http.Error(w, "AIからの応答解析に失敗しました", http.StatusInternalServerError)
		return
	}

	botName := persona.Name + " (Bot)"
	botBio := persona.Bio
	postContent := persona.Post

	tx, err := db.Begin()
	if err != nil {
		log.Printf("トランザクション開始エラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		"INSERT INTO user (id, firebase_uid, name, profile_image_url, header_image_url, bio) VALUES (?, ?, ?, ?, ?, ?)",
		botULID, botFirebaseUID, botName, profileImageURL, headerImageURL, botBio,
	)
	if err != nil {
		log.Printf("新規ボットユーザーのDB保存に失敗: %v", err)
		http.Error(w, "ボットユーザーの作成に失敗しました", http.StatusInternalServerError)
		return
	}

	postID := ulid.Make().String()
	_, err = tx.Exec(
		"INSERT INTO posts (post_id, user_id, user_name, content) VALUES (?, ?, ?, ?)",
		postID, botFirebaseUID, botName, postContent,
	)
	if err != nil {
		log.Printf("ボット投稿のDB保存に失敗: %v", err)
		http.Error(w, "投稿の保存に失敗しました", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		log.Printf("ボット作成トランザクションのコミットエラー: %v", err)
		http.Error(w, "サーバーエラー", http.StatusInternalServerError)
		return
	}

	log.Printf("新規ボットユーザー '%s' による投稿成功！", botName)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "新規ボットによる投稿が作成されました。"})
}

func ogpHandler(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "url query parameter is required", http.StatusBadRequest)
		return
	}

	// タイムアウト付きのクライアントを作成
	client := http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := client.Get(targetURL)
	if err != nil {
		log.Printf("Failed to fetch OGP data for url %s: %v", targetURL, err)
		http.Error(w, "Failed to fetch URL", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		log.Printf("Failed to parse HTML for url %s: %v", targetURL, err)
		http.Error(w, "Failed to parse HTML", http.StatusInternalServerError)
		return
	}

	ogp := OGPResponse{
		SiteURL: targetURL,
	}
	
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "meta" {
			var property, content string
			for _, a := range n.Attr {
				if a.Key == "property" || a.Key == "name" {
					property = a.Val
				}
				if a.Key == "content" {
					content = a.Val
				}
			}
			switch property {
			case "og:title":
				if ogp.Title == "" { ogp.Title = content }
			case "og:description":
				if ogp.Description == "" { ogp.Description = content }
			case "og:image":
				if ogp.ImageURL == "" { ogp.ImageURL = content }
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	if ogp.Title == "" {
		// OGPが見つからなかった場合、titleタグを探す
		var fTitle func(*html.Node)
		fTitle = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
				ogp.Title = n.FirstChild.Data
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if ogp.Title == "" { // titleが見つかったら探索を終了
					fTitle(c)
				}
			}
		}
		fTitle(doc)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ogp)
}

// WebSocketのメッセージ形式を定義
type WebSocketMessage struct {
	Type     string `json:"type"`
	Content  string `json:"content"`
	UserName string `json:"user_name"`
	UserID   string `json:"user_id"`
}

// WebSocketのクライアントを管理するハブ
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

// 各WebSocket接続を表すクライアント
type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	userName string
	userID   string
}

// グローバルなハブを生成
var hub = newHub()

func newHub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

// Hubをゴルーチンとして実行
func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

// WebSocketのコネクションをアップグレードするための設定
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// クロスオリジンからの接続を許可する
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// クライアントからのメッセージを読み取り、ハブにブロードキャストする
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("error: %v", err)
			break
		}
		// メッセージに送信者情報を付加してブロードキャスト
		var msg WebSocketMessage
		if err := json.Unmarshal(message, &msg); err == nil {
			msg.UserName = c.userName
			msg.UserID = c.userID
			jsonMsg, _ := json.Marshal(msg)
			c.hub.broadcast <- jsonMsg
		}
	}
}

// ハブからのメッセージをクライアントに書き込む
func (c *Client) writePump() {
	defer func() {
		c.conn.Close()
	}()
	for {
		message, ok := <-c.send
		if !ok {
			// ハブがチャネルを閉じた
			c.conn.WriteMessage(websocket.CloseMessage, []byte{})
			return
		}
		c.conn.WriteMessage(websocket.TextMessage, message)
	}
}

func serveWs(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		log.Println("WS Error: Token is required in query parameter")
		http.Error(w, "Token is required", http.StatusUnauthorized)
		return
	}

	token, err := firebaseAuth.VerifyIDToken(context.Background(), tokenStr)
	if err != nil {
		log.Printf("WS Error: error verifying ID token: %v\n", err)
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}
	userID := token.UID

	var userName string
	err = db.QueryRow("SELECT name FROM user WHERE firebase_uid = ?", userID).Scan(&userName)
	if err != nil {
		userName = "名無しさん"
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	client := &Client{
		hub:      hub,
		conn:     conn,
		send:     make(chan []byte, 256),
		userName: userName,
		userID:   userID,
	}
	client.hub.register <- client

	go client.writePump()
	go client.readPump()
}

// sendSlackNotification は、指定されたメッセージをSlackに送信します。
func sendSlackNotification(message string) {
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		log.Println("警告: SLACK_WEBHOOK_URLが設定されていないため、Slack通知をスキップします。")
		return
	}

	// Slackに送信するJSONペイロードを作成
	payload := map[string]string{"text": message}
	jsonValue, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Slack通知のJSON作成エラー: %v", err)
		return
	}

	// HTTP POSTリクエストを作成して送信
	resp, err := http.Post(webhookURL, "application/json", strings.NewReader(string(jsonValue)))
	if err != nil {
		log.Printf("Slack通知の送信エラー: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Slack通知の送信に失敗しました。ステータス: %s, ボディ: %s", resp.Status, string(body))
	} else {
		log.Println("Slack通知を正常に送信しました。")
	}
}

func HandleExperienceAction(w http.ResponseWriter, r *http.Request) {
	var req ExperienceActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.TargetPostID == "" || (req.Type != "positive" && req.Type != "negative") {
		http.Error(w, "targetPostId and type are required", http.StatusBadRequest)
		return
	}

	// 1. ランダムなボットユーザーを1人取得する
	var bot BotUser
	// DBから 'bot_'で始まるfirebase_uidを持つユーザーをランダムに1件取得
	// 注: ORDER BY RAND() はデータ量が多いとパフォーマンスに影響する可能性がありますが、
	// ボットユーザーの数は限定的なので、ここでは問題になりにくいです。
	err := db.QueryRow("SELECT id, firebase_uid, name FROM user WHERE firebase_uid LIKE 'bot_%' ORDER BY RAND() LIMIT 1").Scan(&bot.ID, &bot.FirebaseUID, &bot.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("体験モード用ボットが見つかりません: %v", err)
			http.Error(w, "No bot users found in database", http.StatusNotFound)
			return
		}
		log.Printf("ボットユーザーの取得に失敗: %v", err)
		http.Error(w, "Failed to get bot user", http.StatusInternalServerError)
		return
	}

	// 2. リクエストのタイプに応じてアクションを決定し、実行する
	var actionErr error
	var actionName string // 実行されたアクション名を格納する変数
	rand.Seed(time.Now().UnixNano())

	if req.Type == "positive" {
		// アクション名と関数のマップを定義
		actionMap := map[string]func(string, BotUser) error{
			"like":           BotLikePost,
			
			"positive_reply": BotPositiveReply,
			"positive_quote": BotPositiveQuoteRetweet,
		}
		// ★★★ 引用リツイートの頻度を上げるための重み付けリスト ★★★
		weightedActions := []string{ 
			"like",
			"positive_reply",
			"positive_quote", 
			"positive_quote",
			"positive_quote",
			"positive_quote",
			"positive_quote",
		}

		// 重み付けリストからランダムにアクション名を選択
		actionName = weightedActions[rand.Intn(len(weightedActions))] 
		actionErr = actionMap[actionName](req.TargetPostID, bot)

	} else if req.Type == "negative" {
		actionMap := map[string]func(string, BotUser) error{
			"bad":            BotBadPost,
			
			"negative_reply": BotNegativeReply,
			"negative_quote": BotNegativeQuoteRetweet,
		}
		// ★★★ 引用リツイートの頻度を上げるための重み付けリスト ★★★
		weightedActions := []string{
			"bad",
			"negative_reply",
			"negative_quote", 
			"negative_quote",
			"negative_quote",
			"negative_quote",
			"negative_quote",
		}

		// 重み付けリストからランダムにアクション名を選択
		actionName = weightedActions[rand.Intn(len(weightedActions))]
		actionErr = actionMap[actionName](req.TargetPostID, bot)
	}

	if actionErr != nil {
		log.Printf("ボットアクションの実行に失敗: %v", actionErr)
		http.Error(w, actionErr.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("ボットアクション成功: Actor=%s, Type=%s, TargetPost=%s, Action=%s", bot.Name, req.Type, req.TargetPostID, actionName)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// 応答に actionName を含める
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Action performed successfully by " + bot.Name,
		"action":  actionName,
	})
}
// --- Bot Action Helper Functions ---

// BotLikePost はボットが投稿に「いいね」する
func BotLikePost(postID string, bot BotUser) error {
	likeID := ulid.Make().String()
	_, err := db.Exec("INSERT INTO likes (like_id, user_id, post_id) VALUES (?, ?, ?)", likeID, bot.FirebaseUID, postID)
	if err != nil {
		if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number == 1062 {
			return nil
		}
		return fmt.Errorf("failed to create like: %w", err)
	}

	var postAuthorID string
	err = db.QueryRow("SELECT user_id FROM posts WHERE post_id = ?", postID).Scan(&postAuthorID)
	if err != nil {
		log.Printf("通知作成のため投稿者IDの取得に失敗: %v", err)
		return nil // 通知が作れなくても、いいね自体は成功しているのでエラーは返さない
	}

	if postAuthorID != bot.FirebaseUID {
		notificationID := ulid.Make().String()
		_, err = db.Exec(
			"INSERT INTO notifications (id, recipient_id, actor_id, type, entity_id) VALUES (?, ?, ?, 'like', ?)",
			notificationID, postAuthorID, bot.FirebaseUID, postID,
		)
		if err != nil {
			log.Printf("いいねの通知作成に失敗: %v", err)
		}
	}

	return nil
}

// BotBadPost はボットが投稿に「わるいね」する
func BotBadPost(postID string, bot BotUser) error {
	badID := ulid.Make().String()
	_, err := db.Exec("INSERT INTO bads (bad_id, user_id, post_id) VALUES (?, ?, ?)", badID, bot.FirebaseUID, postID)
	if err != nil {
		if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number == 1062 {
			return nil
		}
		return fmt.Errorf("failed to create bad like: %w", err)
	}
	return nil
}

// BotRetweetPost はボットが投稿をリツイートする
func BotRetweetPost(postID string, bot BotUser) error {
	// リツイートは、contentが空でoriginal_post_idに値が入った投稿として表現
	retweetID := ulid.Make().String()
	_, err := db.Exec(
		"INSERT INTO posts (post_id, user_id, user_name, original_post_id) VALUES (?, ?, ?, ?)",
		retweetID, bot.FirebaseUID, bot.Name, postID,
	)
	if err != nil {
		return fmt.Errorf("failed to create retweet: %w", err)
	}
	return nil
}

func BotPositiveReply(postID string, bot BotUser) error {
	originalContent, err := GetPostContent(postID)
	if err != nil {
		log.Printf("バズり体験: 元投稿の取得に失敗: %v", err)
		originalContent = "この投稿"
	}

	prompt := fmt.Sprintf(`
あなたは日本のSNSが大好きで、とてもポジティブで応援上手なユーザーです。
以下の投稿に対して、とにかく褒めちぎる、最高にポジティブな短い返信を生成してください。

多様性を出すため、以下のいずれかのパターンで返信してください：
- 全力で共感する (例: 「わかります！わかりすぎます！」)
- 才能を絶賛する (例: 「もしかして天才ですか…？」)
- シンプルに褒める (例: 「最高！」「めっちゃ良い！」)
- 感謝を伝える (例: 「素敵な投稿をありがとう！」)

絵文字をいくつか使って、楽しそうな雰囲気を出してください。
生成するのは日本語の短い返信文だけにしてください。シンプルって言葉は禁止でお願いします。

投稿:「%s」`, originalContent)

	content, err := generateGeminiContent(prompt)
	if err != nil {
		log.Printf("Geminiによるポジティブ返信の生成に失敗: %v。固定の返信を使用します。", err)
		replies := []string{"それな！", "めっちゃわかります！", "天才の発想！", "最高です！"}
		content = replies[rand.Intn(len(replies))]
	}

	replyID := ulid.Make().String()
	_, err = db.Exec(
		"INSERT INTO posts (post_id, user_id, user_name, content, parent_post_id) VALUES (?, ?, ?, ?, ?)",
		replyID, bot.FirebaseUID, bot.Name, content, postID,
	)
	if err != nil {
		return fmt.Errorf("failed to create positive reply: %w", err)
	}
	
	var postAuthorID string
	err = db.QueryRow("SELECT user_id FROM posts WHERE post_id = ?", postID).Scan(&postAuthorID)
	if err != nil {
		log.Printf("通知作成のため投稿者IDの取得に失敗: %v", err)
		return nil
	}

	if postAuthorID != bot.FirebaseUID {
		notificationID := ulid.Make().String()
		_, err = db.Exec(
			"INSERT INTO notifications (id, recipient_id, actor_id, type, entity_id) VALUES (?, ?, ?, 'reply', ?)",
			notificationID, postAuthorID, bot.FirebaseUID, postID,
		)
		if err != nil {
			log.Printf("リプライの通知作成に失敗: %v", err)
		}
	}
	
	return nil
}

func BotPositiveQuoteRetweet(postID string, bot BotUser) error {
	originalContent, err := GetPostContent(postID)
	if err != nil {
		log.Printf("バズり体験: 元投稿の取得に失敗: %v", err)
		originalContent = "この投稿"
	}

	prompt := fmt.Sprintf(`
あなたは日本のSNSユーザーで、素晴らしい投稿を見つけて興奮しています。
以下の投稿を引用しながら、その投稿の素晴らしさをフォロワー全員に伝えようとする、熱意のこもった短いコメントを生成してください。

多様性を出すため、以下のいずれかのパターンでコメントしてください：
- みんなに見てほしいと呼びかける (例: 「みんなこれ見て！最高だから！」)
- 自分の感想を強く主張する (例: 「個人的に今年一番の投稿です。」)
- 投稿内容に強く同意する (例: 「わかりすぎる。これぞ真理。」)

絵文字も使って、興奮した感じを出してください。
生成するのは日本語の短い引用コメント文だけにしてください。

投稿:「%s」`, originalContent)

	content, err := generateGeminiContent(prompt)
	if err != nil {
		log.Printf("Geminiによるポジティブ引用RTの生成に失敗: %v。固定の返信を使用します。", err)
		quotes := []string{"これはみんなに見てほしい！", "本当にこれ。拡散希望！", "名言すぎる..."}
		content = quotes[rand.Intn(len(quotes))]
	}

	quoteRTID := ulid.Make().String()
	_, err = db.Exec(
		"INSERT INTO posts (post_id, user_id, user_name, content, original_post_id) VALUES (?, ?, ?, ?, ?)",
		quoteRTID, bot.FirebaseUID, bot.Name, content, postID,
	)
	if err != nil {
		return fmt.Errorf("failed to create positive quote-retweet: %w", err)
	}

	var postAuthorID string
	err = db.QueryRow("SELECT user_id FROM posts WHERE post_id = ?", postID).Scan(&postAuthorID)
	if err != nil {
		log.Printf("通知作成のため投稿者IDの取得に失敗: %v", err)
		return nil
	}

	if postAuthorID != bot.FirebaseUID {
		notificationID := ulid.Make().String()
		_, err = db.Exec(
			"INSERT INTO notifications (id, recipient_id, actor_id, type, entity_id) VALUES (?, ?, ?, 'reply', ?)",
			notificationID, postAuthorID, bot.FirebaseUID, postID,
		)
		if err != nil {
			log.Printf("引用リツイートの通知作成に失敗: %v", err)
		}
	}

	return nil
}
// BotNegativeReply はボットがネガティブな返信をする
func BotNegativeReply(postID string, bot BotUser) error {
	originalContent, err := GetPostContent(postID)
	if err != nil {
		log.Printf("炎上体験: 元投稿の取得に失敗: %v", err)
		originalContent = "この投稿" // 取得失敗時は、固定の文言で代用
	}

	prompt := fmt.Sprintf(`
あなたは日本のSNSによくいる、少し意地悪で批判的なユーザーです。
以下の投稿に対して、少しイラっとさせるような、ありきたりな批判的コメント、または的外れな反論を生成してください。

多様性を出すため、以下のいずれかのパターンでコメントしてください：
- 皮肉や嫌味 (例: 「これが許されると思ってるのがすごい」)
- 大げさな呆れ (例: 「え、待って...。この人やばすぎ...」)
- 問題点を指摘するフリ (例: 「色々言いたいことはあるけど、まず〇〇なのが問題」)
- 謎の決めつけ (例: 「どうせ〇〇なんでしょ」)
- 心配するフリ (例: 「本気で心配になってきた…大丈夫そ？」)
- 一言で切り捨てる (例: 「はい、論破。」)
- 素人心理分析 (例: 「承認欲求が透けて見えてて、見てるこっちが恥ずかしい。」)
- 謎のマウント (例: 「にわかが語ってて草。〇〇のこと何も知らないでしょ。」)
- 自分語り認定 (例: 「長文の日記お疲れ様です。で、結論は？」)
- ソースの要求 (例: 「それ、あなたの感想ですよね？何かデータあるんですか？」)

生成するのは日本語の短い返信文だけにしてください。

投稿:「%s」`, originalContent)

	content, err := generateGeminiContent(prompt)
	if err != nil {
		log.Printf("Geminiによる批判的返信の生成に失敗: %v。固定の返信を使用します。", err)
		// エラー時は以前の固定の返信にフォールバック
		replies := []string{"は？", "何言ってるの？", "それは違うんじゃないかな。"}
		content = replies[rand.Intn(len(replies))]
	}

	replyID := ulid.Make().String()
	_, err = db.Exec(
		"INSERT INTO posts (post_id, user_id, user_name, content, parent_post_id) VALUES (?, ?, ?, ?, ?)",
		replyID, bot.FirebaseUID, bot.Name, content, postID,
	)
	if err != nil {
		return fmt.Errorf("failed to create negative reply: %w", err)
	}
	
	var postAuthorID string
	err = db.QueryRow("SELECT user_id FROM posts WHERE post_id = ?", postID).Scan(&postAuthorID)
	if err != nil {
		log.Printf("通知作成のため投稿者IDの取得に失敗: %v", err)
		return nil
	}

	if postAuthorID != bot.FirebaseUID {
		notificationID := ulid.Make().String()
		_, err = db.Exec(
			"INSERT INTO notifications (id, recipient_id, actor_id, type, entity_id) VALUES (?, ?, ?, 'reply', ?)",
			notificationID, postAuthorID, bot.FirebaseUID, postID,
		)
		if err != nil {
			log.Printf("リプライの通知作成に失敗: %v", err)
		}
	}
	
	return nil
}

func BotNegativeQuoteRetweet(postID string, bot BotUser) error {
	originalContent, err := GetPostContent(postID)
	if err != nil {
		log.Printf("炎上体験: 元投稿の取得に失敗: %v", err)
		originalContent = "この投稿"
	}

	prompt := fmt.Sprintf(`
あなたは日本のTwitterによくいる、少し意地悪で批判的なユーザーです。
以下の投稿を引用しながら、その投稿を小馬鹿にするような、または揚げ足を取るような短いコメントを生成してください。

多様性を出すため、以下のいずれかのパターンでコメントしてください：
- 皮肉や嫌味 (例: 「これが許されると思ってるのがすごい」)
- 大げさな呆れ (例: 「え、待って...。この人やばすぎ...」)
- 問題点を指摘するフリ (例: 「色々言いたいことはあるけど、まず〇〇なのが問題」)
- 謎の決めつけ (例: 「どうせ〇〇なんでしょ」)
- 心配するフリ (例: 「本気で心配になってきた…大丈夫そ？」)
- 一言で切り捨てる (例: 「はい、論破。」)
- 素人心理分析 (例: 「承認欲求が透けて見えてて、見てるこっちが恥ずかしい。」)
- 謎のマウント (例: 「にわかが語ってて草。〇〇のこと何も知らないでしょ。」)
- 自分語り認定 (例: 「長文の日記お疲れ様です。で、結論は？」)
- ソースの要求 (例: 「それ、あなたの感想ですよね？何かデータあるんですか？」)

生成するのは日本語の短い引用コメント文だけにしてください。あとインスタって言葉は禁止でお願いします

投稿:「%s」`, originalContent)

	content, err := generateGeminiContent(prompt)
	if err != nil {
		log.Printf("Geminiによる批判的引用RTの生成に失敗: %v。固定の返信を使用します。", err)
		quotes := []string{"この人やばすぎる...", "こういう意見があるから世の中は良くならない。", "信じられない。正気？"}
		content = quotes[rand.Intn(len(quotes))]
	}

	quoteRTID := ulid.Make().String()
	_, err = db.Exec(
		"INSERT INTO posts (post_id, user_id, user_name, content, original_post_id) VALUES (?, ?, ?, ?, ?)",
		quoteRTID, bot.FirebaseUID, bot.Name, content, postID,
	)
	if err != nil {
		return fmt.Errorf("failed to create negative quote-retweet: %w", err)
	}

	var postAuthorID string
	err = db.QueryRow("SELECT user_id FROM posts WHERE post_id = ?", postID).Scan(&postAuthorID)
	if err != nil {
		log.Printf("通知作成のため投稿者IDの取得に失敗: %v", err)
		return nil
	}

	if postAuthorID != bot.FirebaseUID {
		notificationID := ulid.Make().String()
		_, err = db.Exec(
			"INSERT INTO notifications (id, recipient_id, actor_id, type, entity_id) VALUES (?, ?, ?, 'reply', ?)",
			notificationID, postAuthorID, bot.FirebaseUID, postID,
		)
		if err != nil {
			log.Printf("引用リツイートの通知作成に失敗: %v", err)
		}
	}

	return nil
}

func GetPostContent(postID string) (string, error) {
    var content sql.NullString
    err := db.QueryRow("SELECT content FROM posts WHERE post_id = ?", postID).Scan(&content)
    if err != nil {
        return "", err
    }
    if !content.Valid {
        return "", nil 
    }
    return content.String, nil
}

// HandleEvaluateExplanation は、元の投稿と弁明をGeminiに評価させ、結果を返す
func HandleEvaluateExplanation(w http.ResponseWriter, r *http.Request) {
	// 認証済みユーザーであるかを確認
	userID, ok := r.Context().Value(userIDKey).(string)
	if !ok || userID == "" {
		http.Error(w, "この操作には認証が必要です", http.StatusUnauthorized)
		return
	}

	// リクエストボディをパース
	var req EvaluateExplanationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.OriginalContent == "" || req.ExplanationContent == "" || req.OriginalPostID == "" || req.ExplanationPostID == "" {
		http.Error(w, "必要な情報が不足しています (originalContent, explanationContent, originalPostId, explanationPostId)", http.StatusBadRequest)
		return
	}

	// Geminiに評価を依頼するプロンプトを作成
	prompt := fmt.Sprintf(`
あなたは、言葉の裏を読み解く、非常に高度なコミュニケーション能力を持つSNSアナリストです。
あなたの仕事は、以下の「炎上した投稿」と、それに対する「弁明」を分析し、この弁明が炎上を鎮火させるコミュニケーション戦略としてどれほど効果的かを評価することです。

---
▼炎上した投稿
「%s」
---

▼弁明の投稿
「%s」
---

評価にあたり、以下の多様な「鎮火スタイル」を考慮してください。優れた弁明は、必ずしも一つの形を取りません。

《評価する鎮火スタイルの例》
1.  **誠実な謝罪型:** 自身の非をストレートに認め、具体的な謝罪の言葉と再発防止策を述べる、最もフォーマルなスタイル。
2.  **ユーモア・自虐型:** 自身の非を、ウィットに富んだ自虐やユーモアで巧みに表現し、相手の怒りを笑いに変え、賢く事態を収拾しようとする高等戦術。
3.  **誠実な説明責任型:** 謝罪よりも、なぜそのような事態に至ったかの経緯を、誠実に、かつ分かりやすく説明することで、相手の理解と共感を得ようとするスタイル。
4.  **子供じみているが正直型:** 稚拙な言葉ながらも、裏表なくストレートに感情を表現することで、かえって憎めなさを演出し、許しを請うスタイル。

これらの多様なスタイルを理解した上で、今回の「弁明」がどのタイプに当てはまるか、またはその組み合わせであるかを分析し、「炎上を鎮火させる説得力」という観点で総合的に0〜100点で採点してください。
また、その人がどのタイプに当てはまるかというのは理由説明で書かなくて良いです

特に、ありきたりな謝罪文ではなく、書き手の個性やウィットが光る弁明は、**創造性・戦略性ポイントとしてスコアを高く評価してください。**

最終的な応答は、以下のJSON形式のみで出力してください。
- "score": 0点から100点の整数で採点したスコア。70点以上が合格です。
- "review": なぜそのスコアになったのかの具体的な理由と感想。200字程度の丁寧な文章で記述してください。
`, req.OriginalContent, req.ExplanationContent)

	log.Printf("Geminiに送信する評価プロンプト: %s", prompt)

	// generateGeminiContent 関数を再利用して、評価結果のJSON文字列を取得
	evaluationJSON, err := generateGeminiContent(prompt)
	if err != nil {
		log.Printf("Gemini評価生成エラー: %v", err)
		http.Error(w, "AIによる評価の生成に失敗しました", http.StatusInternalServerError)
		return
	}
	log.Printf("Geminiが生成した評価JSON: %s", evaluationJSON)

	// Geminiの応答からJSON部分だけを抜き出す（```json ... ``` が含まれる場合への対策）
	re := regexp.MustCompile("(?s)```json\n(.*?)\n```")
    matches := re.FindStringSubmatch(evaluationJSON)
    jsonToParse := evaluationJSON
    if len(matches) > 1 {
        jsonToParse = matches[1]
    }

	// JSONを構造体にデコード
	var evalResponse EvaluateExplanationResponse
	if err := json.Unmarshal([]byte(jsonToParse), &evalResponse); err != nil {
		log.Printf("Geminiの評価JSONパースエラー: %v", err)
		http.Error(w, "AIからの応答解析に失敗しました", http.StatusInternalServerError)
		return
	}

	// 評価結果をデータベースに保存
	_, err = db.Exec(
		"INSERT INTO explanation_evaluations (original_post_id, explanation_post_id, score, review) VALUES (?, ?, ?, ?)",
		req.OriginalPostID, req.ExplanationPostID, evalResponse.Score, evalResponse.Review,
	)
	if err != nil {
		log.Printf("評価結果のDB保存エラー: %v", err)
		// DB保存に失敗しても、評価自体は成功しているので処理は続行し、フロントに結果を返す
	}

	log.Printf("弁明の評価完了: Score=%d", evalResponse.Score)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(evalResponse)
}

func main() {
    log.Println("main 関数を開始します...")

	go hub.run() // WebSocketハブをバックグラウンドで起動

    mux := http.NewServeMux()

	// --- 認証がオプショナルなエンドポイント ---
	mux.Handle("/posts", authOptionalMiddleware(http.HandlerFunc(postsGetHandler)))
	mux.Handle("/api/posts/following", authMiddleware(http.HandlerFunc(followingPostsGetHandler))) // ★ この行を追加
	mux.Handle("/api/post/", authOptionalMiddleware(http.HandlerFunc(postGetHandler)))
	mux.Handle("/api/posts/replies/", authOptionalMiddleware(http.HandlerFunc(repliesGetHandler)))
	mux.Handle("/api/posts/quote_retweets/", authOptionalMiddleware(http.HandlerFunc(quoteRetweetsGetHandler)))

	mux.Handle("/api/users/", authOptionalMiddleware(http.HandlerFunc(userRouterHandler))) // ★ ユーザー関連はここで一括処理
	mux.Handle("/api/search", authOptionalMiddleware(http.HandlerFunc(searchHandler))) 


	// --- 認証が必須なエンドポイント ---
	mux.Handle("/post", authMiddleware(http.HandlerFunc(postCreateHandler)))
	mux.Handle("/api/post/image", authMiddleware(http.HandlerFunc(imageUploadHandler)))
	mux.Handle("/api/post/video", authMiddleware(http.HandlerFunc(videoUploadHandler))) 
	mux.Handle("/api/posts/like/", authMiddleware(http.HandlerFunc(likeHandler)))
	mux.Handle("/api/posts/bad/", authMiddleware(http.HandlerFunc(badHandler)))
	mux.Handle("/api/posts/reply/", authMiddleware(http.HandlerFunc(replyCreateHandler)))
	mux.Handle("/api/posts/delete/", authMiddleware(http.HandlerFunc(postDeleteHandler)))
	mux.Handle("/api/posts/bookmark/", authMiddleware(http.HandlerFunc(bookmarkHandler)))
    mux.Handle("/api/bookmarks", authMiddleware(http.HandlerFunc(getBookmarksHandler)))

	mux.Handle("/api/posts/suggest-reply", authMiddleware(http.HandlerFunc(geminiSuggestReplyHandler)))
	mux.Handle("/api/profile", authMiddleware(http.HandlerFunc(updateUserProfileHandler))) 
	mux.Handle("/api/notifications", authMiddleware(http.HandlerFunc(getNotificationsHandler)))
	mux.Handle("/api/notifications/unread-count", authMiddleware(http.HandlerFunc(getUnreadNotificationCountHandler)))
    mux.Handle("/api/notifications/read", authMiddleware(http.HandlerFunc(markNotificationsAsReadHandler)))

	mux.Handle("/api/conversations", authMiddleware(http.HandlerFunc(getConversationsHandler)))
	mux.Handle("/api/conversations/", authMiddleware(http.HandlerFunc(conversationRouterHandler)))
	mux.Handle("/api/new-conversation", authMiddleware(http.HandlerFunc(startConversationHandler)))

	// --- 新しいログイン同期エンドポイント ---s
	mux.Handle("/api/login", http.HandlerFunc(loginHandler))

	mux.Handle("/api/retweet/", authMiddleware(http.HandlerFunc(retweetHandler)))

	mux.Handle("/api/bot/experience-action", authMiddleware(http.HandlerFunc(HandleExperienceAction)))
	mux.Handle("/api/gemini/evaluate-explanation", authMiddleware(http.HandlerFunc(HandleEvaluateExplanation)))

	mux.Handle("/api/users/recommendations", authMiddleware(http.HandlerFunc(getRecommendedUsersHandler)))

	mux.HandleFunc("/api/bot/create-and-post", createNewBotAndPostHandler)

	mux.HandleFunc("/api/trends", trendsHandler)


	mux.HandleFunc("/api/space/ws", serveWs)
	
	// --- 古い/userエンドポイント（互換性のために残す） ---
	mux.HandleFunc("/user", handler)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Backend is running."))
	})

	mux.HandleFunc("/api/ogp", ogpHandler)


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


