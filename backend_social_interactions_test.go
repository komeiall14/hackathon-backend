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

func TestSocialInteractions(t *testing.T) {
	db, mock := setupTest(t)
	defer db.Close()

	userID := "user-a"
	otherUserID := "user-b"
	postID := "post-123"

	// --- いいね、よくないね機能のテスト ---
	t.Run("Like and Bad actions", func(t *testing.T) {
		// いいね
		mock.ExpectExec("INSERT INTO likes").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery("SELECT user_id FROM posts").WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(otherUserID))
		mock.ExpectExec("INSERT INTO notifications").WillReturnResult(sqlmock.NewResult(1, 1))
		reqLike := httptest.NewRequest("POST", "/api/posts/like/"+postID, nil)
		ctx := context.WithValue(reqLike.Context(), userIDKey, userID)
		likeHandler(httptest.NewRecorder(), reqLike.WithContext(ctx))

		// よくないね
		mock.ExpectExec("INSERT INTO bads").WillReturnResult(sqlmock.NewResult(1, 1))
		reqBad := httptest.NewRequest("POST", "/api/posts/bad/"+postID, nil)
		badHandler(httptest.NewRecorder(), reqBad.WithContext(ctx))
	})

	// --- フォロー機能のテスト ---
	t.Run("Follow action", func(t *testing.T) {
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO follows")).WithArgs(userID, otherUserID).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO notifications")).WillReturnResult(sqlmock.NewResult(1, 1))
		req := httptest.NewRequest("POST", "/api/users/"+otherUserID+"/follow", nil)
		ctx := context.WithValue(req.Context(), userIDKey, userID)
		followHandler(httptest.NewRecorder(), req.WithContext(ctx))
	})

	// --- リプライとリツイート機能のテスト ---
	t.Run("Reply and Retweet actions", func(t *testing.T) {
		// リプライ
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO posts (post_id, user_id, user_name, content, parent_post_id)")).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT p.user_id")).WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(otherUserID))
		mock.ExpectExec("INSERT INTO notifications").WillReturnResult(sqlmock.NewResult(1, 1))
		reqBody := `{"content": "リプライです"}`
		reqReply := httptest.NewRequest("POST", "/api/posts/reply/"+postID, strings.NewReader(reqBody))
		ctxReply := context.WithValue(reqReply.Context(), userIDKey, userID)
		replyCreateHandler(httptest.NewRecorder(), reqReply.WithContext(ctxReply))
		
		// リツイート
		mock.ExpectQuery("SELECT name FROM user").WithArgs(userID).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("リツイートした人"))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO posts (post_id, user_id, user_name, original_post_id)")).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery("SELECT p.post_id").WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow("rt-id"))
		reqRetweet := httptest.NewRequest("POST", "/api/retweet/"+postID, nil)
		ctxRetweet := context.WithValue(reqRetweet.Context(), userIDKey, userID)
		retweetHandler(httptest.NewRecorder(), reqRetweet.WithContext(ctxRetweet))
	})

	// --- ブックマーク機能のテスト ---
	t.Run("Bookmark actions", func(t *testing.T) {
		mock.ExpectExec("INSERT INTO bookmarks").WithArgs(userID, postID).WillReturnResult(sqlmock.NewResult(1, 1))
		reqAdd := httptest.NewRequest("POST", "/api/posts/bookmark/"+postID, nil)
		ctx := context.WithValue(reqAdd.Context(), userIDKey, userID)
		bookmarkHandler(httptest.NewRecorder(), reqAdd.WithContext(ctx))
		
		mock.ExpectExec("DELETE FROM bookmarks").WithArgs(userID, postID).WillReturnResult(sqlmock.NewResult(1, 1))
		reqDelete := httptest.NewRequest("DELETE", "/api/posts/bookmark/"+postID, nil)
		bookmarkHandler(httptest.NewRecorder(), reqDelete.WithContext(ctx))
	})
	
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("期待されたDB処理が実行されませんでした: %s", err)
	}
}