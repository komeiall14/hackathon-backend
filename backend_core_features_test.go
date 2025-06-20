package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCoreFeatures(t *testing.T) {
	db, mock := setupTest(t)
	defer db.Close()

	// --- ユーザー関連のテスト ---
	t.Run("User CRUD", func(t *testing.T) {
		userID := "core-test-user-id"
		userName := "コアテストユーザー"

		// プロフィール取得
		t.Run("Get User Profile", func(t *testing.T) {
			rows := sqlmock.NewRows([]string{"id", "name"}).AddRow("1", userName)
			mock.ExpectQuery("SELECT id, name").WithArgs("", userID).WillReturnRows(rows)
			req := httptest.NewRequest("GET", "/api/users/"+userID, nil)
			getUserProfileHandler(httptest.NewRecorder(), req)
		})

		// プロフィール更新
		t.Run("Update User Profile", func(t *testing.T) {
			mock.ExpectExec("UPDATE user SET").
				WithArgs("新しい名前", "新しい自己紹介", "", "", userID).
				WillReturnResult(sqlmock.NewResult(1, 1))
			reqBody := `{"name": "新しい名前", "bio": "新しい自己紹介"}`
			req := httptest.NewRequest("PUT", "/api/profile", strings.NewReader(reqBody))
			ctx := context.WithValue(req.Context(), userIDKey, userID)
			updateUserProfileHandler(httptest.NewRecorder(), req.WithContext(ctx))
		})
	})

	// --- 投稿関連のテスト ---
	t.Run("Post CRUD", func(t *testing.T) {
		userID := "core-post-user-id"
		userName := "投稿テストユーザー"
		postID := "core-post-123"

		// 投稿作成
		t.Run("Create Post", func(t *testing.T) {
			mock.ExpectQuery("SELECT name FROM user").WithArgs(userID).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow(userName))
			mock.ExpectExec("INSERT INTO posts").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectQuery("SELECT p.post_id").WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow(postID))
			
			reqBody := `{"content": "コア機能の投稿"}`
			req := httptest.NewRequest("POST", "/post", strings.NewReader(reqBody))
			ctx := context.WithValue(req.Context(), userIDKey, userID)
			postCreateHandler(httptest.NewRecorder(), req.WithContext(ctx))
		})

		// 投稿取得
		t.Run("Get Single Post", func(t *testing.T) {
			rows := sqlmock.NewRows([]string{"post_id"}).AddRow(postID)
			mock.ExpectQuery("SELECT p.post_id").WithArgs("", "", "", "", postID).WillReturnRows(rows)
			req := httptest.NewRequest("GET", "/api/post/"+postID, nil)
			postGetHandler(httptest.NewRecorder(), req)
		})

		// 投稿削除
		t.Run("Delete Post", func(t *testing.T) {
			mock.ExpectBegin()
			mock.ExpectExec("DELETE FROM posts").WithArgs(postID).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()

			req := httptest.NewRequest("DELETE", "/api/posts/delete/"+postID, nil)
			rr := httptest.NewRecorder()
			postDeleteHandler(rr, req)
			if status := rr.Code; status != http.StatusNoContent {
				t.Errorf("投稿削除のステータスコードが不正です: got %v want %v", status, http.StatusNoContent)
			}
		})
	})
	
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("期待されたDB処理が実行されませんでした: %s", err)
	}
}