package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// --- テスト用のヘルパー関数 ---

// クリーンアップ関数：テスト前にテーブルを空にする.d
func cleanupTables(t *testing.T) {
	t.Helper()
	// 依存関係の逆順で削除する (likes -> posts)
	if _, err := db.Exec("DELETE FROM likes"); err != nil {
		t.Fatalf("likesテーブルのクリーンアップに失敗: %v", err)
	}
	if _, err := db.Exec("DELETE FROM posts"); err != nil {
		t.Fatalf("postsテーブルのクリーンアップに失敗: %v", err)
	}
}

// --- 各ハンドラのテスト ---

func TestPostHandlers(t *testing.T) {
	// 各テストの実行前にDBをクリーンアップ
	cleanupTables(t)

	var createdPostID string // 作成した投稿IDを後続のテストで使うために保持

	t.Run("POST /post - 投稿作成", func(t *testing.T) {
		postJSON := `{"content": "テスト投稿", "user_id": "test-user", "user_name": "テストユーザー"}`
		req := httptest.NewRequest("POST", "/post", bytes.NewBufferString(postJSON))
		rr := httptest.NewRecorder()
		postCreateHandler(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("ステータスコードが不正です: got %v want %v", rr.Code, http.StatusCreated)
		}
		var body map[string]string
		json.Unmarshal(rr.Body.Bytes(), &body)
		createdPostID = body["post_id"]
		if createdPostID == "" {
			t.Fatal("レスポンスに post_id がありません")
		}
	})

	t.Run("GET /posts - 投稿一覧取得", func(t *testing.T) {
		// このテストの前に投稿が1件だけ作成されている状態
		req := httptest.NewRequest("GET", "/posts", nil)
		rr := httptest.NewRecorder()
		postsGetHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("ステータスコードが不正です: got %v want %v", rr.Code, http.StatusOK)
		}
		var posts []Post
		json.Unmarshal(rr.Body.Bytes(), &posts)
		// 直前のテストで作成した1件のみのはず
		if len(posts) != 1 {
			t.Errorf("取得した投稿の数が不正です: got %d want %d", len(posts), 1)
		}
	})

	t.Run("DELETE /api/posts/delete/:id - 投稿削除", func(t *testing.T) {
		if createdPostID == "" {
			t.Fatal("削除対象の投稿IDがありません")
		}
		req := httptest.NewRequest("DELETE", "/api/posts/delete/"+createdPostID, nil)
		req.URL.Path = "/api/posts/delete/" + createdPostID
		rr := httptest.NewRecorder()
		postDeleteHandler(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("ステータスコードが不正です: got %v want %v", rr.Code, http.StatusNoContent)
		}
	})
}


func TestReplyHandlers(t *testing.T) {
	cleanupTables(t)

	// 準備: 親となる投稿を作成
	_, err := db.Exec("INSERT INTO posts (post_id, user_id, user_name, content) VALUES ('parent-post-id', 'p-user', '親投稿者', '親')")
	if err != nil {
		t.Fatalf("親投稿の準備に失敗: %v", err)
	}

	t.Run("POST /api/posts/reply/:id - リプライ作成", func(t *testing.T) {
		replyURL := "/api/posts/reply/parent-post-id"
		replyJSON := `{"content": "テストリプライです"}`
		req := httptest.NewRequest("POST", replyURL, bytes.NewBufferString(replyJSON))
		req.URL.Path = replyURL
		rr := httptest.NewRecorder()
		replyCreateHandler(rr, req)

		if rr.Code != http.StatusCreated {
			t.Errorf("ステータスコードが不正です: got %v want %v", rr.Code, http.StatusCreated)
		}
	})

	t.Run("GET /api/posts/replies/:id - リプライ一覧取得", func(t *testing.T) {
		// 準備: 追加のリプライを作成
		_, err := db.Exec("INSERT INTO posts (post_id, user_id, user_name, content, parent_post_id) VALUES ('reply-2', 'r-user2', '返信者2', '返信2', 'parent-post-id')")
		if err != nil {
			t.Fatalf("追加リプライの準備に失敗: %v", err)
		}

		repliesURL := "/api/posts/replies/parent-post-id"
		req := httptest.NewRequest("GET", repliesURL, nil)
		req.URL.Path = repliesURL
		rr := httptest.NewRecorder()
		repliesGetHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("ステータスコードが不正です: got %v want %v", rr.Code, http.StatusOK)
		}
		var replies []Post
		json.Unmarshal(rr.Body.Bytes(), &replies)
		if len(replies) != 2 {
			t.Errorf("取得したリプライの数が不正です: got %d want %d", len(replies), 2)
		}
	})
}

func TestLikeHandler(t *testing.T) {
	cleanupTables(t)
	_, err := db.Exec("INSERT INTO posts (post_id, user_id, user_name, content) VALUES ('like-post-id', 'l-user', 'いいね用投稿者', 'いいね！')")
	if err != nil {
		t.Fatalf("いいね用投稿の準備に失敗: %v", err)
	}
	likeURL := "/api/posts/like/like-post-id"

	// いいね作成
	reqLike := httptest.NewRequest("POST", likeURL, nil)
	rrLike := httptest.NewRecorder()
	likeHandler(rrLike, reqLike)
	if rrLike.Code != http.StatusCreated {
		t.Errorf("いいね作成時のステータスコードが不正です: got %v want %v", rrLike.Code, http.StatusCreated)
	}

	// いいね削除
	reqUnlike := httptest.NewRequest("DELETE", likeURL, nil)
	rrUnlike := httptest.NewRecorder()
	likeHandler(rrUnlike, reqUnlike)
	if rrUnlike.Code != http.StatusNoContent {
		t.Errorf("いいね削除時のステータスコードが不正です: got %v want %v", rrUnlike.Code, http.StatusNoContent)
	}
}

func TestGeminiSuggestReplyHandler_NoAPIKey(t *testing.T) {
	originalKey := os.Getenv("GEMINI_API_KEY")
	os.Setenv("GEMINI_API_KEY", "")
	defer os.Setenv("GEMINI_API_KEY", originalKey)

	reqBody := `{"original_post_content": "テスト投稿"}`
	req := httptest.NewRequest("POST", "/api/posts/suggest-reply", bytes.NewBufferString(reqBody))
	rr := httptest.NewRecorder()
	geminiSuggestReplyHandler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("APIキーがない場合、500エラーを期待していましたが、 got %v", rr.Code)
	}
}