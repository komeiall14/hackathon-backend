package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestBookmarkHandlers(t *testing.T) {
	db, mock := setupTest(t)
	defer db.Close()

	userID := "user-bookmark-test"
	postID := "post-to-bookmark"
	
	t.Run("Add and Remove Bookmark", func(t *testing.T) {
		// ブックマーク追加
		mock.ExpectExec("INSERT INTO bookmarks").WithArgs(userID, postID).WillReturnResult(sqlmock.NewResult(1, 1))
		reqAdd := httptest.NewRequest("POST", "/api/posts/bookmark/"+postID, nil)
		ctx := context.WithValue(reqAdd.Context(), userIDKey, userID)
		rrAdd := httptest.NewRecorder()
		bookmarkHandler(rrAdd, reqAdd.WithContext(ctx))
		if status := rrAdd.Code; status != http.StatusCreated {
			t.Errorf("ブックマーク追加のステータスコードが不正です: got %v want %v", status, http.StatusCreated)
		}

		// ブックマーク削除
		mock.ExpectExec("DELETE FROM bookmarks").WithArgs(userID, postID).WillReturnResult(sqlmock.NewResult(1, 1))
		reqDelete := httptest.NewRequest("DELETE", "/api/posts/bookmark/"+postID, nil)
		rrDelete := httptest.NewRecorder()
		bookmarkHandler(rrDelete, reqDelete.WithContext(ctx))
		if status := rrDelete.Code; status != http.StatusNoContent {
			t.Errorf("ブックマーク削除のステータスコードが不正です: got %v want %v", status, http.StatusNoContent)
		}
	})

	t.Run("Get Bookmarks", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"post_id"}).AddRow(postID)
		mock.ExpectQuery("SELECT p.post_id").WithArgs(userID, userID, userID, userID).WillReturnRows(rows)

		req := httptest.NewRequest("GET", "/api/bookmarks", nil)
		ctx := context.WithValue(req.Context(), userIDKey, userID)
		rr := httptest.NewRecorder()
		getBookmarksHandler(rr, req.WithContext(ctx))

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("ブックマーク一覧取得のステータスコードが不正です: got %v want %v", status, http.StatusOK)
		}
	})

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("期待されたDB処理が実行されませんでした: %s", err)
	}
}