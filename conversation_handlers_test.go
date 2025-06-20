package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestConversationHandlers(t *testing.T) {
	db, mock := setupTest(t)
	defer db.Close()

	userID := "user-dm-test"
	otherUserID := "user-dm-other"
	convID := "conv-123"

	// --- Test 1: 会話一覧取得 ---
	t.Run("GetConversationsHandler", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"conversation_id", "updated_at", "firebase_uid", "name", "profile_image_url", "id", "sender_id", "content", "created_at"}).
			AddRow(convID, "2025-06-20T10:00:00Z", otherUserID, "相手", nil, "msg1", otherUserID, "こんにちは", "2025-06-20T10:00:00Z")

		mock.ExpectQuery("SELECT c.id AS conversation_id").WithArgs(userID, userID).WillReturnRows(rows)
		
		req := httptest.NewRequest("GET", "/api/conversations", nil)
		ctx := context.WithValue(req.Context(), userIDKey, userID)
		rr := httptest.NewRecorder()
		getConversationsHandler(rr, req.WithContext(ctx))

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("不正なステータスコード: got %v want %v", status, http.StatusOK)
		}
	})

	// --- Test 2: メッセージ送信 ---
	t.Run("SendMessageHandler", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO messages").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE conversations SET").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		mock.ExpectQuery("SELECT id, conversation_id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("new-msg-id"))

		reqBody := `{"content": "テストメッセージ"}`
		req := httptest.NewRequest("POST", "/api/conversations/"+convID+"/messages", strings.NewReader(reqBody))
		ctx := context.WithValue(req.Context(), userIDKey, userID)
		rr := httptest.NewRecorder()
		sendMessageHandler(rr, req.WithContext(ctx))

		if status := rr.Code; status != http.StatusCreated {
			t.Errorf("不正なステータスコード: got %v want %v", status, http.StatusCreated)
		}
	})
	
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("期待されたDB処理が実行されませんでした: %s", err)
	}
}