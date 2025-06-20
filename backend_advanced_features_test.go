package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAdvancedFeatures(t *testing.T) {
	db, mock := setupTest(t)
	defer db.Close()

	userID := "advanced-user-id"

	// --- 検索機能のテスト ---
	t.Run("Search handler", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"post_id"}).AddRow("search-result-post")
		mock.ExpectQuery("SELECT p.post_id").WithArgs(userID, userID, userID, "%検索語%", "%検索語%").WillReturnRows(rows)
		
		req := httptest.NewRequest("GET", "/api/search?q=検索語", nil)
		ctx := context.WithValue(req.Context(), userIDKey, userID)
		rr := httptest.NewRecorder()
		searchHandler(rr, req.WithContext(ctx))

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("検索ハンドラのステータスコードが不正です: got %v want %v", status, http.StatusOK)
		}
	})

	// --- 通知機能のテスト ---
	t.Run("Notification handlers", func(t *testing.T) {
		// 未読件数取得
		mock.ExpectQuery("SELECT COUNT").WithArgs(userID).WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(3))
		reqCount := httptest.NewRequest("GET", "/api/notifications/unread-count", nil)
		ctx := context.WithValue(reqCount.Context(), userIDKey, userID)
		getUnreadNotificationCountHandler(httptest.NewRecorder(), reqCount.WithContext(ctx))

		// 既読化
		mock.ExpectExec("UPDATE notifications SET is_read = TRUE").WithArgs(userID).WillReturnResult(sqlmock.NewResult(1, 1))
		reqRead := httptest.NewRequest("POST", "/api/notifications/read", nil)
		markNotificationsAsReadHandler(httptest.NewRecorder(), reqRead.WithContext(ctx))
	})

	// --- OGP取得機能のテスト ---
	t.Run("OGP handler", func(t *testing.T) {
		// このテストは外部サイトにアクセスするため、モック化が困難です。
		// ここでは、URLパラメータがない場合に正しくエラーを返すかだけをテストします。
		req := httptest.NewRequest("GET", "/api/ogp?url=", nil)
		rr := httptest.NewRecorder()
		ogpHandler(rr, req)
		if status := rr.Code; status != http.StatusBadRequest {
			t.Errorf("OGPハンドラ(パラメータなし)のステータスコードが不正です: got %v want %v", status, http.StatusBadRequest)
		}
	})

	// --- Gemini関連のテスト ---
	t.Run("Gemini experience handler", func(t *testing.T) {
		// このテストはGemini APIを実際に呼び出すため、ここではDBとの連携部分のみをテストします
		botRows := sqlmock.NewRows([]string{"id", "firebase_uid", "name"}).AddRow("bot-id", "bot-uid", "テストボット")
		mock.ExpectQuery("SELECT id, firebase_uid, name FROM user WHERE firebase_uid LIKE 'bot_%'").WillReturnRows(botRows)
		mock.ExpectExec("INSERT INTO bads").WillReturnResult(sqlmock.NewResult(1,1)) // 例としてわるいね

		reqBody := `{"targetPostId": "post1", "type": "negative"}`
		req := httptest.NewRequest("POST", "/api/bot/experience-action", strings.NewReader(reqBody))
		rr := httptest.NewRecorder()
		HandleExperienceAction(rr, req)
		if status := rr.Code; status != http.StatusOK {
			t.Errorf("不正なステータスコード: got %v want %v", status, http.StatusOK)
		}
	})
	
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("期待されたDB処理が実行されませんでした: %s", err)
	}
}