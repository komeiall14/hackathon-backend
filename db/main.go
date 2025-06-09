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

// ★★★ 修正箇所1 ★★★
// UserResForHTTPGet はHTTPレスポンス用のユーザー情報の構造体です。
type UserResForHTTPGet struct {
	Id          string  `json:"id"`
	Name        string  `json:"name"`
	Age         *int    `json:"age"`
	FirebaseUID *string `json:"firebase_uid"`
}

// Post は投稿データの構造体です。
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

var db *sql.DB // グローバルなデータベース接続プール

// init関数は変更ありません
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
		log.Panicf("致命的エラー: sql.Open に失敗しました。DSNが不正である可能性があります。エラー: %v\n", err)
	}
	log.Println("データベースへのPingを試みます...")
	if err := _db.Ping(); err != nil {
		maskedDsn := dsn
		if mysqlUserPwd != "" {
			maskedDsn = strings.Replace(dsn, mysqlUserPwd, "[PASSWORD_MASKED]", 1)
		}
		log.Panicf("致命的エラー: _db.Ping に失敗しました。データベースへの接続を確認できません。\n  エラー詳細: %v\n  DSN (マスク済): %s\n", err, maskedDsn)
	}
	db = _db
	log.Println("✅ DB接続に成功しました。初期化処理を完了します。")
}

// ★★★ 修正箇所2 ★★★
// handler関数を、構造体の変更に合わせて完全に修正します
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


// postsGetHandler はトップレベルの投稿を、いいね数やリプライ数と共に取得してJSONで返します。
func postsGetHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
        return
    }

    currentUserID := "test-user-123" 

    // ★★★ SELECT句に p.image_url を追加 ★★★
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
        // ★★★ Scanに &p.ImageURL を追加 ★★★
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
// postCreateHandler は新しい投稿を作成します。
func postCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// ★ 1. リクエストボディの型にImageURLを追加
	var requestBody struct {
		Content  string `json:"content"`
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
		ImageURL string `json:"image_url,omitempty"` // omitemptyで空の場合は無視される
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		log.Printf("エラー: リクエストボディのデコードに失敗しました: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 画像がない場合でもテキストが空でなければ投稿できるように修正
	if requestBody.Content == "" && requestBody.ImageURL == "" {
		log.Println("バリデーションエラー: 投稿内容が空です。")
		http.Error(w, "投稿内容が空です", http.StatusBadRequest)
		return
	}

	// トランザクションを開始
	tx, err := db.Begin()
	if err != nil {
		log.Printf("エラー: db.Begin (トランザクション開始) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// deferを使って、エラー時に必ずロールバックするようにする
	defer tx.Rollback()

	// ユーザーがDBに存在しない場合に新規作成するロジック (変更なし)
	var userTableID string
	err = tx.QueryRow("SELECT id FROM user WHERE firebase_uid = ?", requestBody.UserID).Scan(&userTableID)
	if err == sql.ErrNoRows {
		log.Printf("ユーザーがDBに存在しないため、新規作成します: firebase_uid=%s", requestBody.UserID)
		newULID := ulid.Make().String()
		_, insertErr := tx.Exec(
			"INSERT INTO user (id, name, firebase_uid) VALUES (?, ?, ?)",
			newULID,
			requestBody.UserName,
			requestBody.UserID,
		)
		if insertErr != nil {
			log.Printf("エラー: userテーブルへのINSERTに失敗: %v", insertErr)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		log.Printf("ユーザー作成成功: id=%s, firebase_uid=%s", newULID, requestBody.UserID)
	} else if err != nil {
		log.Printf("エラー: ユーザーの存在確認クエリに失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// ★ 2. image_urlをDBに保存する準備
	// ImageURLが空文字列の場合はDBにNULLを保存する
	var imageUrlToSave sql.NullString
	if requestBody.ImageURL != "" {
		imageUrlToSave.String = requestBody.ImageURL
		imageUrlToSave.Valid = true
	}

	// ★ 3. 投稿を作成するSQLを修正
	postID := ulid.Make().String()
	_, err = tx.Exec("INSERT INTO posts (post_id, user_id, user_name, content, image_url) VALUES (?, ?, ?, ?, ?)",
		postID,
		requestBody.UserID,
		requestBody.UserName,
		requestBody.Content,
		imageUrlToSave, // image_urlも保存する
	)

	if err != nil {
		log.Printf("エラー: postsテーブルへのINSERTに失敗: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// 全ての処理が成功したら、トランザクションをコミット
	if err := tx.Commit(); err != nil {
		log.Printf("エラー: tx.Commit (トランザクションコミット) に失敗しました: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	log.Printf("投稿作成成功: post_id=%s\n", postID)
	json.NewEncoder(w).Encode(map[string]string{"post_id": postID})
}

// ★★★ 投稿を削除するハンドラ関数をここに追加 ★★★
func postDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETEメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// パスから投稿IDを取得 (例: /api/posts/delete/{postID})
	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 5 || pathSegments[4] == "" { // ID部分が空でないかチェック
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

	// 投稿を削除する (もしリプライなども同時に削除したい場合は、ここに追加のDELETE文を書く)
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
	w.WriteHeader(http.StatusNoContent) // 成功時は 204 No Content を返す
}


// likeHandler はいいねの作成と削除を処理します
func likeHandler(w http.ResponseWriter, r *http.Request) {
    // (likeHandler関数の内容は変更なしのため省略)
	pathSegments := strings.Split(r.URL.Path, "/")
    if len(pathSegments) < 5 { 
        http.Error(w, "投稿IDが指定されていません", http.StatusBadRequest)
        return
    }
    postID := pathSegments[4]

    userID := "test-user-123" 

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

// replyCreateHandlerは特定の投稿への新しいリプライを作成します
func replyCreateHandler(w http.ResponseWriter, r *http.Request) {
	// (replyCreateHandler関数の内容は変更なしのため省略)
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	pathSegments := strings.Split(r.URL.Path, "/")
	if len(pathSegments) < 5 { 
		http.Error(w, "親となる投稿IDがパスに含まれていません", http.StatusBadRequest)
		return
	}
	parentPostID := pathSegments[4] 

	var requestBody struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if requestBody.Content == "" {
		http.Error(w, "リプライ内容が空です", http.StatusBadRequest)
		return
	}

	userID := "test-user-123" 
	userName := "テストリプライユーザー"

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
	log.Printf("リプライ作成成功: reply_id=%s, parent_id=%s", replyID, parentPostID)
	json.NewEncoder(w).Encode(map[string]string{"reply_id": replyID})
}

// repliesGetHandler はリプライの一覧を取得します
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
    currentUserID := "test-user-123" 

    // ★★★ SELECT句に p.image_url を追加 ★★★
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
        // ★★★ Scanに &p.ImageURL を追加 ★★★
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
	// (geminiSuggestReplyHandler関数の内容は変更なしのため省略)
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

// userPostsHandlerは特定のユーザーの投稿一覧を取得します
func userPostsHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "GETメソッドのみが許可されています", http.StatusMethodNotAllowed)
        return
    }

    // URLパスからユーザーIDを取得 (例: /api/users/ここにIDが入る/)
    pathSegments := strings.Split(r.URL.Path, "/")
    if len(pathSegments) < 4 || pathSegments[3] == "" {
        http.Error(w, "ユーザーIDが指定されていません", http.StatusBadRequest)
        return
    }
    userID := pathSegments[3]

    log.Printf("特定ユーザーの投稿検索を開始: user_id=%s\n", userID)

    // ログイン中のユーザーID（いいね判定用、今回は仮）
    currentUserID := "test-user-123"

    // ★★★ SELECT句に p.image_url を追加 ★★★
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
        // ★★★ Scanに &p.ImageURL を追加 ★★★
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

// imageUploadHandler は画像を受け取りGCSにアップロードします
func imageUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POSTメソッドのみが許可されています", http.StatusMethodNotAllowed)
		return
	}

	// 1. リクエストから画像ファイルを取得
	file, _, err := r.FormFile("image") // "image"はフロントエンドから送る際のキー名
	if err != nil {
		log.Printf("画像の取得に失敗: %v", err)
		http.Error(w, "画像の取得に失敗しました", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 2. GCSのバケット名とクライアントを準備
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

	// 3. GCSに保存する際のファイル名を生成（ULIDでユニークな名前を付ける）
	objectName := ulid.Make().String() + ".png" // 拡張子は適宜変更
	
	// 4. GCSへの書き込み準備
	writer := client.Bucket(bucketName).Object(objectName).NewWriter(ctx)
	// この設定で、アップロードした画像が一般公開される
	writer.ACL = []storage.ACLRule{{Entity: storage.AllUsers, Role: storage.RoleReader}}

	// 5. ファイルをGCSにコピー（アップロード）
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

	// 6. フロントエンドに、公開された画像のURLを返す
	publicURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucketName, objectName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"imageUrl": publicURL})
	log.Printf("画像アップロード成功: %s", publicURL)
}

// main関数はアプリケーションのエントリポイントです。
func main() {
    log.Println("main 関数を開始します...")
    log.Println("DEBUG: main function started successfully. Proceeding to setup router and CORS.")

    mux := http.NewServeMux()
    mux.HandleFunc("/user", handler)
    log.Println("/user エンドポイントのハンドラを設定しました。")

	mux.HandleFunc("/posts", postsGetHandler)
	mux.HandleFunc("/post", postCreateHandler)
    mux.HandleFunc("/api/posts/like/", likeHandler)
    mux.HandleFunc("/api/posts/reply/", replyCreateHandler)
	mux.HandleFunc("/api/posts/replies/", repliesGetHandler)
	mux.HandleFunc("/api/posts/suggest-reply", geminiSuggestReplyHandler)
	mux.HandleFunc("/api/posts/delete/", postDeleteHandler)
	mux.HandleFunc("/api/users/", userPostsHandler)
	mux.HandleFunc("/api/post/image", imageUploadHandler)
	


    log.Println("DEBUG: CORS middleware configuration point.") 
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
        Debug:            true,
    })

    handlerWithCORS := c.Handler(mux)
    log.Println("DEBUG: CORS middleware applied to handler.")

    closeDBWithSysCall()
    log.Println("システムコールによるDBクローズ処理を設定しました。")

    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
        log.Printf("環境変数 PORT が未設定のため、デフォルトの %s を使用します。\n", port)
    }

    log.Printf("HTTPサーバーをポート %s で起動します...\n", port)
    log.Printf("DEBUG: About to call ListenAndServe. Port: %s", port)

    if err := http.ListenAndServe(":"+port, handlerWithCORS); err != nil {
        log.Fatalf("致命的エラー: ListenAndServe に失敗しました。ポート %s を使用できませんでした: %v", port, err)
    }
}

// closeDBWithSysCall関数はOSのシグナル(SIGTERM, SIGINT)を補足し、DB接続を安全にクローズします。
func closeDBWithSysCall() {
	// (closeDBWithSysCall関数の内容は変更なしのため省略)
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