package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUploadHandlers(t *testing.T) {
	// GCSとの連携はモックしないため、DBモックは不要

	t.Run("imageUploadHandler returns error if no file is provided", func(t *testing.T) {
		// "image"というキーでファイルが送られてこないリクエストを作成
		req := httptest.NewRequest("POST", "/api/post/image", nil)
		rr := httptest.NewRecorder()
		imageUploadHandler(rr, req)

		if status := rr.Code; status != http.StatusBadRequest {
			t.Errorf("画像ファイルなしの場合のステータスコードが不正です: got %v want %v", status, http.StatusBadRequest)
		}
	})

	t.Run("videoUploadHandler returns error if no file is provided", func(t *testing.T) {
		// "video"というキーでファイルが送られてこないリクエストを作成
		req := httptest.NewRequest("POST", "/api/post/video", nil)
		rr := httptest.NewRecorder()
		videoUploadHandler(rr, req)

		if status := rr.Code; status != http.StatusBadRequest {
			t.Errorf("動画ファイルなしの場合のステータスコードが不正です: got %v want %v", status, http.StatusBadRequest)
		}
	})
}